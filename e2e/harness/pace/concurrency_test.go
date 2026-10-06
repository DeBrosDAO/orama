package pace

import (
	"bufio"
	"bytes"
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strconv"
	"sync"
	"testing"
	"time"
)

// Subprocess helper protocol: the parent re-runs this test binary with these
// variables; the child takes helperWaits tokens and prints each grant time.
const (
	envHelperPath  = "PACE_HELPER_STATE"
	helperWaits    = 8
	helperPerMin   = 1200
	helperBurst    = 2
	helperProcs    = 2
	helperBudget   = 30 * time.Second
	grantTolerance = 1e-9
)

var helperBudgets = Budgets{Cred: Budget{PerMinute: helperPerMin, Burst: helperBurst}, Challenge: Budget{1, 1}}

// checkBound asserts the token-bucket invariant: by any grant time r, at most
// burst + rate*(r - t0) grants were made. Each time is read after its grant,
// so it is never earlier than the grant itself and the bound is exact.
func checkBound(t *testing.T, t0 time.Time, grants []time.Time, b Budget) {
	t.Helper()
	slices.SortFunc(grants, func(a, b time.Time) int { return a.Compare(b) })
	for i, r := range grants {
		allowed := float64(b.Burst) + float64(r.Sub(t0))*perNano(b)
		if float64(i+1) > allowed+grantTolerance {
			t.Fatalf("grant %d at +%s exceeds the budget (%.3f allowed)", i+1, r.Sub(t0), allowed)
		}
	}
}

func TestWait_manyGoroutinesNeverExceedBudget(t *testing.T) {
	b := Budget{PerMinute: 30, Burst: 5}
	p, c := newTestPacer(t, Budgets{Cred: b, Challenge: Budget{1, 1}})
	const goroutines, each = 16, 4
	t0 := c.now()
	var mu sync.Mutex
	var grants []time.Time
	var wg sync.WaitGroup
	errs := make(chan error, goroutines)
	for g := 0; g < goroutines; g++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for i := 0; i < each; i++ {
				if err := p.Wait(context.Background(), "gw.example", BucketCred); err != nil {
					errs <- err
					return
				}
				mu.Lock()
				grants = append(grants, c.now())
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	close(errs)
	for err := range errs {
		t.Fatal(err)
	}
	if len(grants) != goroutines*each {
		t.Fatalf("%d grants, want %d", len(grants), goroutines*each)
	}
	checkBound(t, t0, grants, b)
}

// TestWaitHelper_subprocess is the child of TestWait_twoProcessesNeverExceedBudget;
// run directly it does nothing.
func TestWaitHelper_subprocess(t *testing.T) {
	path := os.Getenv(envHelperPath)
	if path == "" {
		return
	}
	p, err := New(path, helperBudgets)
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), helperBudget)
	defer cancel()
	for i := 0; i < helperWaits; i++ {
		if err := p.Wait(ctx, "gw.example", BucketCred); err != nil {
			t.Fatal(err)
		}
		fmt.Println(time.Now().UnixNano())
	}
}

func TestWait_twoProcessesNeverExceedBudget(t *testing.T) {
	path := filepath.Join(t.TempDir(), FileName)
	t0 := time.Now()
	outs := make([]*bytes.Buffer, helperProcs)
	cmds := make([]*exec.Cmd, helperProcs)
	for i := range cmds {
		outs[i] = &bytes.Buffer{}
		cmds[i] = exec.Command(os.Args[0], "-test.run=^TestWaitHelper_subprocess$", "-test.count=1")
		cmds[i].Env = append(os.Environ(), envHelperPath+"="+path)
		cmds[i].Stdout, cmds[i].Stderr = outs[i], outs[i]
		if err := cmds[i].Start(); err != nil {
			t.Fatal(err)
		}
	}
	var grants []time.Time
	for i, cmd := range cmds {
		if err := cmd.Wait(); err != nil {
			t.Fatalf("helper %d: %v\n%s", i, err, outs[i])
		}
		grants = append(grants, parseGrants(t, outs[i])...)
	}
	if len(grants) != helperProcs*helperWaits {
		t.Fatalf("%d grants, want %d", len(grants), helperProcs*helperWaits)
	}
	checkBound(t, t0, grants, helperBudgets.Cred)
}

func parseGrants(t *testing.T, out *bytes.Buffer) []time.Time {
	t.Helper()
	var grants []time.Time
	sc := bufio.NewScanner(bytes.NewReader(out.Bytes()))
	for sc.Scan() {
		if n, err := strconv.ParseInt(sc.Text(), 10, 64); err == nil {
			grants = append(grants, time.Unix(0, n))
		}
	}
	return grants
}
