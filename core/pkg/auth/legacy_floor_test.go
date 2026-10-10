package auth

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

// floorOf is a LegacyFloor over the stamp levels in *levels, counting reads.
func floorOf(levels *[]int, clock *time.Time, reads *atomic.Int32) *LegacyFloor {
	return &LegacyFloor{
		Now: func() time.Time { return *clock },
		Read: func(context.Context) ([]NodeStampLevel, error) {
			reads.Add(1)
			return nodeLevels(*levels), nil
		},
	}
}

// nodeLevels names the nodes node-0, node-1, ... for levels.
func nodeLevels(levels []int) []NodeStampLevel {
	out := make([]NodeStampLevel, len(levels))
	for i, l := range levels {
		out[i] = NodeStampLevel{ID: fmt.Sprintf("node-%d", i), Level: l}
	}
	return out
}

// settle waits for the reading in flight, if any.
func settle(f *LegacyFloor) {
	f.mu.Lock()
	done := f.refresh
	f.mu.Unlock()
	if done != nil {
		<-done
	}
}

// refreshed is Accepts once the call has had its reading: a stale answer is
// served while the registry is read again, so the new one is the next call's.
func refreshed(f *LegacyFloor) bool {
	f.Accepts()
	settle(f)
	return f.Accepts()
}

func TestLegacyFloor_acceptsWhileSomeNodeDoesNotSignTheNoncedStamps(t *testing.T) {
	clock := time.Now()
	var reads atomic.Int32
	for name, tc := range map[string]struct {
		levels  []int
		accepts bool
	}{
		"every node legacy":                           {[]int{StampLevelLegacy, StampLevelLegacy}, true},
		"a 0.122.x build (level 0) among nonced ones": {[]int{StampLevelNonced, StampLevelLegacy, StampLevelNonced}, true},
		"a node that never reported (default 0)":      {[]int{StampLevelNonced, 0}, true},
		"a level no build reports":                    {[]int{StampLevelNonced, -1}, true},
		"every node nonced":                           {[]int{StampLevelNonced, StampLevelNonced}, false},
		"a level past the nonced one":                 {[]int{StampLevelNonced, StampLevelNonced + 1}, false},
		"one node alone, nonced":                      {[]int{StampLevelNonced}, false},
		"a cluster with no node recorded":             {nil, false},
	} {
		t.Run(name, func(t *testing.T) {
			levels := tc.levels
			f := floorOf(&levels, &clock, &reads)
			if got := f.Accepts(); got != tc.accepts {
				t.Fatalf("Accepts() = %v, want %v", got, tc.accepts)
			}
		})
	}
}

func TestLegacyFloor_readsTheRegistryOncePerTTLAndFollowsAnUpgrade(t *testing.T) {
	clock := time.Now()
	var reads atomic.Int32
	levels := []int{StampLevelLegacy, StampLevelNonced}
	f := floorOf(&levels, &clock, &reads)

	if !f.Accepts() {
		t.Fatal("the older stamps were refused with a node still on the legacy stamps")
	}
	levels = []int{StampLevelNonced, StampLevelNonced}
	for i := 0; i < 5; i++ {
		clock = clock.Add(time.Second)
		if !f.Accepts() {
			t.Fatal("the answer changed inside the cache")
		}
	}
	if reads.Load() != 1 {
		t.Fatalf("%d reads inside the TTL, want 1", reads.Load())
	}
	clock = clock.Add(legacyFloorTTL)
	if refreshed(f) {
		t.Fatal("the older stamps are still accepted once every node is upgraded")
	}
	if reads.Load() != 2 {
		t.Fatalf("%d reads after the TTL, want 2", reads.Load())
	}
}

// A registry that cannot be read keeps the last answer; with none, the older
// stamps stay accepted, and the failure is said every time.
func TestLegacyFloor_aRegistryThatCannotBeReadKeepsTheLastAnswerAndSaysSo(t *testing.T) {
	clock := time.Now()
	var logs []string
	fail := errors.New("no leader")
	levels := []int{StampLevelNonced}
	f := &LegacyFloor{
		Now: func() time.Time { return clock },
		Read: func(context.Context) ([]NodeStampLevel, error) {
			if fail != nil {
				return nil, fail
			}
			return nodeLevels(levels), nil
		},
		Logf: func(format string, args ...any) { logs = append(logs, format) },
	}

	if !f.Accepts() {
		t.Fatal("with no reading yet, the older stamps must stay accepted")
	}
	if len(logs) != 1 {
		t.Fatalf("%d log lines for the failed read, want 1", len(logs))
	}
	fail = nil
	clock = clock.Add(legacyFloorTTL)
	if refreshed(f) {
		t.Fatal("every node is nonced and the registry answered, but the older stamps are accepted")
	}
	fail = errors.New("no leader")
	clock = clock.Add(legacyFloorTTL)
	if refreshed(f) {
		t.Fatal("a failed read turned the older stamps back on")
	}
	if len(logs) != 2 || !strings.Contains(logs[1], "keeping the last answer") {
		t.Fatalf("logs = %v", logs)
	}
}

// Once there is an answer, a slow registry holds no request: the answer is
// served while one goroutine reads again, and only one read is started.
func TestLegacyFloor_aSlowReadDoesNotBlockAcceptsOnceThereIsAnAnswer(t *testing.T) {
	clock := time.Now()
	var reads atomic.Int32
	release := make(chan struct{})
	var slow atomic.Bool
	levels := []NodeStampLevel{{"node-a", StampLevelNonced}, {"node-b", StampLevelLegacy}}
	f := &LegacyFloor{
		Now: func() time.Time { return clock },
		Read: func(context.Context) ([]NodeStampLevel, error) {
			reads.Add(1)
			if slow.Load() {
				<-release
			}
			return levels, nil
		},
	}
	if !f.Accepts() {
		t.Fatal("a legacy node is in the cluster")
	}
	slow.Store(true)
	levels = []NodeStampLevel{{"node-a", StampLevelNonced}, {"node-b", StampLevelNonced}}
	clock = clock.Add(legacyFloorTTL)

	answered := make(chan bool, 20)
	for i := 0; i < 20; i++ {
		go func() { answered <- f.Accepts() }()
	}
	for i := 0; i < 20; i++ {
		select {
		case got := <-answered:
			if !got {
				t.Fatal("the stale answer changed before the reading finished")
			}
		case <-time.After(5 * time.Second):
			t.Fatal("Accepts blocked on a registry read that has not finished")
		}
	}
	close(release)
	settle(f)
	if f.Accepts() {
		t.Fatal("the older stamps are still accepted after the reading found every node nonced")
	}
	if reads.Load() != 2 {
		t.Fatalf("%d reads, want 1 at the start and 1 shared by every call after the TTL", reads.Load())
	}
}

// The nodes that hold the floor open are named, once per reading, so an
// operator can see which one to upgrade; nothing is said when none does.
func TestLegacyFloor_namesTheNodesBelowTheNoncedLevelOncePerReading(t *testing.T) {
	clock := time.Now()
	var logs []string
	levels := []NodeStampLevel{{"node-a", StampLevelNonced}, {"node-b", StampLevelLegacy}, {"node-c", StampLevelLegacy}}
	f := &LegacyFloor{
		Now:  func() time.Time { return clock },
		Read: func(context.Context) ([]NodeStampLevel, error) { return levels, nil },
		Logf: func(format string, args ...any) { logs = append(logs, fmt.Sprintf(format, args...)) },
	}
	for i := 0; i < 5; i++ {
		f.Accepts()
	}
	if len(logs) != 1 || !strings.Contains(logs[0], "node-b") || !strings.Contains(logs[0], "node-c") || strings.Contains(logs[0], "node-a") {
		t.Fatalf("logs = %q", logs)
	}
	levels = []NodeStampLevel{{"node-a", StampLevelNonced}, {"node-b", StampLevelNonced}}
	clock = clock.Add(legacyFloorTTL)
	if refreshed(f) {
		t.Fatal("every node is nonced")
	}
	if len(logs) != 1 {
		t.Fatalf("a floor nobody holds open was reported: %q", logs)
	}
}

// installFloor makes the verifiers and signers of this process follow a floor
// over stamp levels, for the test.
func installFloor(t *testing.T, levels ...int) {
	t.Helper()
	clock := time.Now()
	var reads atomic.Int32
	InstallLegacyFloor(floorOf(&levels, &clock, &reads))
	t.Cleanup(func() { InstallLegacyFloor(nil) })
}

// With a legacy node in the cluster the older forms verify (a rolling
// upgrade); once every node is nonced, a request
// whose nonced stamp was stripped is refused, and so is every older form.
func TestCheckCoordination_theOlderStampsFollowTheFleetFloor(t *testing.T) {
	key, now := v2Key(t), time.Now()
	strippedToV2 := func() *http.Request {
		r := servedOn(signedForPort(t, key, "http://10.0.0.1:10104/x"), "10104")
		r.Header.Del(CoordinationMACV3Header)
		return r
	}
	strippedToV1 := func() *http.Request {
		r := servedOn(signedForPort(t, key, "http://10.0.0.1:10104/x"), "10104")
		r.Header.Del(CoordinationMACV3Header)
		r.Header.Del(CoordinationMACV2Header)
		r.Header.Del(CoordinationNonceHeader)
		return r
	}

	t.Run("a legacy node in the cluster", func(t *testing.T) {
		installFloor(t, StampLevelLegacy, StampLevelNonced)
		if v, ok := CheckCoordination(key, strippedToV2(), now, testAudience); !ok || v != CoordinationV2 {
			t.Fatalf("a v2 stamp from an older node: version %d ok=%v", v, ok)
		}
		if v, ok := CheckCoordination(key, strippedToV1(), now, testAudience); !ok || v != CoordinationV1 {
			t.Fatalf("a v1 stamp from an older node: version %d ok=%v", v, ok)
		}
	})
	t.Run("every node nonced", func(t *testing.T) {
		installFloor(t, StampLevelNonced, StampLevelNonced)
		if _, ok := CheckCoordination(key, strippedToV2(), now, testAudience); ok {
			t.Fatal("a request with its v3 stamp stripped verified on v2")
		}
		if _, ok := CheckCoordination(key, strippedToV1(), now, testAudience); ok {
			t.Fatal("a request with its nonced stamps stripped verified on v1")
		}
		whole := servedOn(signedForPort(t, key, "http://10.0.0.1:10104/x"), "10104")
		if v, ok := CheckCoordination(key, whole, now, testAudience); !ok || v != CoordinationV3 {
			t.Fatalf("an unstripped request: version %d ok=%v", v, ok)
		}
	})
	t.Run("no floor installed", func(t *testing.T) {
		if _, ok := CheckCoordination(key, strippedToV2(), now, testAudience); !ok {
			t.Fatal("a process with no floor stopped accepting the older stamp")
		}
	})
}

// Once every node signs the new stamps, nothing is written for the old ones to
// be replayed on.
func TestSignCoordination_writesTheOlderStampsOnlyWhileTheFloorIsBelowTheNoncedRelease(t *testing.T) {
	key := v2Key(t)
	installFloor(t, StampLevelLegacy)
	below := signedForPort(t, key, "http://10.0.0.1:10104/x")
	for _, h := range []string{CoordinationMACV3Header, CoordinationMACV2Header, CoordinationMACHeader, CoordinationNonceHeader} {
		if below.Header.Get(h) == "" {
			t.Errorf("with a legacy node, %s was not written", h)
		}
	}
	installFloor(t, StampLevelNonced)
	at := signedForPort(t, key, "http://10.0.0.1:10104/x")
	if at.Header.Get(CoordinationMACV3Header) == "" || at.Header.Get(CoordinationNonceHeader) == "" {
		t.Error("with every node nonced, the v3 stamp was not written")
	}
	for _, h := range []string{CoordinationMACV2Header, CoordinationMACHeader} {
		if at.Header.Get(h) != "" {
			t.Errorf("with every node nonced, %s was still written", h)
		}
	}
}

func TestACME_theUnnoncedStampFollowsTheFleetFloor(t *testing.T) {
	key, now, body := acmeKey(t), time.Now(), []byte(`{}`)
	stripped := func() *http.Request {
		r := signedACME(t, key, body, now)
		r.Header.Del(ACMEMACV2Header)
		r.Header.Del(ACMENonceHeader)
		return r
	}
	t.Run("a legacy node in the cluster", func(t *testing.T) {
		installFloor(t, StampLevelLegacy)
		if !VerifyACME(key, stripped(), body, now) {
			t.Fatal("an older Caddy's stamp was refused with an older node in the cluster")
		}
		if signedACME(t, key, body, now).Header.Get(CoordinationMACHeader) == "" {
			t.Fatal("the unnonced stamp was not written")
		}
	})
	t.Run("every node nonced", func(t *testing.T) {
		installFloor(t, StampLevelNonced)
		if VerifyACME(key, stripped(), body, now) {
			t.Fatal("a captured call with its nonce stripped verified")
		}
		whole := signedACME(t, key, body, now)
		if whole.Header.Get(CoordinationMACHeader) != "" {
			t.Fatal("the unnonced stamp was still written")
		}
		if !VerifyACME(key, whole, body, now) {
			t.Fatal("the nonced call was refused")
		}
	})
}

func TestNodeAPI_theUnnoncedStampFollowsTheFleetFloor(t *testing.T) {
	now, body := time.Now(), []byte(`{}`)
	t.Run("a legacy node in the cluster", func(t *testing.T) {
		installFloor(t, StampLevelLegacy)
		r := signedRequest(t, http.MethodPost, nodeRegisterPath, "node-a", body, now)
		if r.Header.Get(NodeStampHeader) == "" {
			t.Fatal("the unnonced stamp was not written")
		}
		r.Header.Del(NodeStampV2Header)
		r.Header.Del(NodeNonceHeader)
		if id, _, ok := VerifyNodeAPI(testVerifier, r, body, now); !ok || id != "node-a" {
			t.Fatalf("an old node's stamp was refused: %q %v", id, ok)
		}
	})
	t.Run("every node nonced", func(t *testing.T) {
		installFloor(t, StampLevelNonced)
		r := signedRequest(t, http.MethodPost, nodeRegisterPath, "node-a", body, now)
		if r.Header.Get(NodeStampHeader) != "" {
			t.Fatal("the unnonced stamp was still written")
		}
		// A captured request, stripped of its nonced stamp and given the
		// unnonced one a replayer made from an older capture.
		old := signedRequest(t, http.MethodPost, nodeRegisterPath, "node-a", body, now)
		installFloor(t, StampLevelLegacy)
		legacy := signedRequest(t, http.MethodPost, nodeRegisterPath, "node-a", body, now)
		installFloor(t, StampLevelNonced)
		old.Header.Set(NodeStampHeader, legacy.Header.Get(NodeStampHeader))
		old.Header.Del(NodeStampV2Header)
		old.Header.Del(NodeNonceHeader)
		if _, _, ok := VerifyNodeAPI(testVerifier, old, body, now); ok {
			t.Fatal("an unnonced stamp verified once every node is on the nonced one")
		}
		fresh := signedRequest(t, http.MethodPost, nodeRegisterPath, "node-a", body, now)
		if id, _, ok := VerifyNodeAPI(testVerifier, fresh, body, now); !ok || id != "node-a" {
			t.Fatalf("the nonced request was refused: %q %v", id, ok)
		}
	})
}

func TestRegistryLegacyFloor_readsTheStampLevelsOfTheRegisteredNodes(t *testing.T) {
	var gotQuery string
	var gotArgs []any
	f := RegistryLegacyFloor(levelQuerier(func(_ context.Context, dest any, query string, args ...any) error {
		gotQuery, gotArgs = query, args
		rows := dest.(*[]struct {
			ID    string `db:"id"`
			Level int    `db:"stamp_level"`
		})
		*rows = append(*rows, struct {
			ID    string `db:"id"`
			Level int    `db:"stamp_level"`
		}{"node-a", StampLevelNonced}, struct {
			ID    string `db:"id"`
			Level int    `db:"stamp_level"`
		}{"node-b", StampLevelLegacy})
		return nil
	}), nil)
	levels, err := f.Read(context.Background())
	want := []NodeStampLevel{{"node-a", StampLevelNonced}, {"node-b", StampLevelLegacy}}
	if err != nil || len(levels) != 2 || levels[0] != want[0] || levels[1] != want[1] {
		t.Fatalf("levels %v, %v", levels, err)
	}
	if !strings.Contains(gotQuery, "FROM dns_nodes") || len(gotArgs) != 1 {
		t.Fatalf("query %q args %v: retired nodes must be left out", gotQuery, gotArgs)
	}
	f = RegistryLegacyFloor(levelQuerier(func(context.Context, any, string, ...any) error { return errors.New("no such column: stamp_level") }), nil)
	if _, err := f.Read(context.Background()); err == nil || !strings.Contains(err.Error(), "stamp_level") {
		t.Fatalf("err = %v", err)
	}
}

type levelQuerier func(ctx context.Context, dest any, query string, args ...any) error

func (q levelQuerier) Query(ctx context.Context, dest any, query string, args ...any) error {
	return q(ctx, dest, query, args...)
}

func TestHasCoordinationStamp(t *testing.T) {
	for name, tc := range map[string]struct {
		header string
		want   bool
	}{
		"none":                         {"", false},
		"v1":                           {CoordinationMACHeader, true},
		"v2":                           {CoordinationMACV2Header, true},
		"v3 alone":                     {CoordinationMACV3Header, true},
		"a header that is not a stamp": {"X-Orama-Nonce", false},
	} {
		t.Run(name, func(t *testing.T) {
			r, _ := http.NewRequest(http.MethodGet, "http://10.0.0.1/x", nil)
			if tc.header != "" {
				r.Header.Set(tc.header, "1.00")
			}
			if got := HasCoordinationStamp(r); got != tc.want {
				t.Fatalf("HasCoordinationStamp = %v, want %v", got, tc.want)
			}
		})
	}
}
