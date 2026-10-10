package pace

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"syscall"
	"time"
)

// fileMode keeps the pacing state readable by the run's user only.
const fileMode = 0o600

// minDelay is the shortest wait Wait sleeps, so rounding never spins.
const minDelay = time.Millisecond

// bucketState is one bucket in the state file.
type bucketState struct {
	Tokens float64 `json:"tokens"`
	Last   int64   `json:"last_unix_nano"`
}

// fileState is the whole state file: bucket name -> bucket.
type fileState map[string]bucketState

// Wait blocks until a token of bucket is available and takes it. It returns
// ctx's error (wrapped) when ctx ends first.
func (p *Pacer) Wait(ctx context.Context, bucket string) error {
	if p == nil {
		return nil
	}
	b, err := p.budgetFor(bucket)
	if err != nil {
		return err
	}
	for {
		if err := ctx.Err(); err != nil {
			return fmt.Errorf("waiting for a %s token: %w", bucket, err)
		}
		delay, err := p.take(bucket, b, false)
		if err != nil {
			return err
		}
		if delay == 0 {
			return nil
		}
		if err := p.sleep(ctx, delay); err != nil {
			return fmt.Errorf("waiting %s for a %s token: %w", delay, bucket, err)
		}
	}
}

// Charge takes a token of bucket without waiting, driving the
// bucket negative when it is empty. It accounts for a credential call made
// by something the harness cannot pace beforehand (the CLI's own polling or
// session renewal): later Waits then wait for it.
func (p *Pacer) Charge(bucket string) error {
	if p == nil {
		return nil
	}
	b, err := p.budgetFor(bucket)
	if err != nil {
		return err
	}
	_, err = p.take(bucket, b, true)
	return err
}

// take refills bucket to now and takes a token when one is there (or always,
// with force). It returns 0 when it took one, else how long until one will be
// there.
func (p *Pacer) take(bucket string, b Budget, force bool) (time.Duration, error) {
	var delay time.Duration
	err := p.update(func(st fileState, now time.Time) {
		cur := refill(st[bucket], b, now)
		if force || cur.Tokens >= 1 {
			cur.Tokens--
		} else {
			delay = untilOne(cur.Tokens, b)
		}
		st[bucket] = cur
		prune(st, p.budgets, now, bucket)
	})
	return delay, err
}

// refill adds what the rate earned since the bucket's last update, capped at
// the burst. A bucket never seen (or pruned when it was full) starts full.
func refill(cur bucketState, b Budget, now time.Time) bucketState {
	if cur.Last == 0 {
		return bucketState{Tokens: float64(b.Burst), Last: now.UnixNano()}
	}
	elapsed := now.UnixNano() - cur.Last
	if elapsed < 0 {
		elapsed = 0
	}
	cur.Tokens = math.Min(float64(b.Burst), cur.Tokens+float64(elapsed)*perNano(b))
	cur.Last = now.UnixNano()
	return cur
}

func perNano(b Budget) float64 {
	return float64(b.PerMinute) / float64(time.Minute)
}

// untilOne is how long until a bucket at tokens holds one token.
func untilOne(tokens float64, b Budget) time.Duration {
	d := time.Duration(math.Ceil((1 - tokens) / perNano(b)))
	return max(d, minDelay)
}

// prune drops buckets that have refilled to full: absent means full, so the
// file stays as small as the set of recently used buckets.
func prune(st fileState, budgets Budgets, now time.Time, keep string) {
	for bucket, cur := range st {
		if bucket == keep {
			continue
		}
		b := budgets.Challenge
		if bucket == BucketCred {
			b = budgets.Cred
		}
		if refill(cur, b, now).Tokens >= float64(b.Burst) {
			delete(st, bucket)
		}
	}
}

// update runs fn on the state file under an exclusive flock and writes the
// result back. A state file that does not parse is an error, not a reset: a
// reset would hand out a full burst the run already spent.
func (p *Pacer) update(fn func(fileState, time.Time)) (err error) {
	f, err := os.OpenFile(p.path, os.O_RDWR|os.O_CREATE, fileMode)
	if err != nil {
		return fmt.Errorf("failed to open the pacing state %s: %w", p.path, err)
	}
	defer func() { err = errors.Join(err, closeErr(f, p.path)) }()
	if err := flock(f, syscall.LOCK_EX); err != nil {
		return fmt.Errorf("failed to lock the pacing state %s: %w", p.path, err)
	}
	st, err := readState(f, p.path)
	if err != nil {
		return err
	}
	fn(st, p.now())
	return writeState(f, p.path, st)
}

func readState(f *os.File, path string) (fileState, error) {
	raw, err := io.ReadAll(f)
	if err != nil {
		return nil, fmt.Errorf("failed to read the pacing state %s: %w", path, err)
	}
	st := fileState{}
	if len(raw) == 0 {
		return st, nil
	}
	if err := json.Unmarshal(raw, &st); err != nil {
		return nil, fmt.Errorf("the pacing state %s is corrupt (delete it only when no package of the run is running): %w", path, err)
	}
	return st, nil
}

func writeState(f *os.File, path string, st fileState) error {
	raw, err := json.Marshal(st)
	if err != nil {
		return fmt.Errorf("failed to encode the pacing state: %w", err)
	}
	if err := f.Truncate(0); err != nil {
		return fmt.Errorf("failed to truncate the pacing state %s: %w", path, err)
	}
	if _, err := f.WriteAt(raw, 0); err != nil {
		return fmt.Errorf("failed to write the pacing state %s: %w", path, err)
	}
	return nil
}

// closeErr closes f, which also releases its flock.
func closeErr(f *os.File, path string) error {
	if err := f.Close(); err != nil {
		return fmt.Errorf("failed to close the pacing state %s: %w", path, err)
	}
	return nil
}

// flock takes a lock on f, retrying when a signal interrupts the call (the Go
// runtime's preemption signals do).
func flock(f *os.File, how int) error {
	for {
		err := syscall.Flock(int(f.Fd()), how)
		if !errors.Is(err, syscall.EINTR) {
			return err
		}
	}
}
