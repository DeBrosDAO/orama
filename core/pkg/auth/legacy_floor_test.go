package auth

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestVersionAtLeast(t *testing.T) {
	for _, tc := range []struct {
		have, min string
		want      bool
	}{
		{"0.3.1", "0.3.1", true},
		{"0.3.2", "0.3.1", true},
		{"0.4.0", "0.3.1", true},
		{"1.0.0", "0.3.1", true},
		{"v0.3.1", "0.3.1", true},
		{"0.3.1-rc1", "0.3.1", true},
		{"0.3.1+build5", "0.3.1", true},
		{"0.4.0.20261008", "0.3.1", true},
		{"0.3.1.1", "0.3.1", true},
		{"0.3.0", "0.3.1", false},
		{"0.3", "0.3.1", false},
		{"0.2.9", "0.3.1", false},
		{"0.122.116", "0.3.1", true},
		{"", "0.3.1", false},
		{"dev", "0.3.1", false},
		{"0.x.1", "0.3.1", false},
		{"-1.0.0", "0.3.1", false},
	} {
		if got := versionAtLeast(tc.have, tc.min); got != tc.want {
			t.Errorf("versionAtLeast(%q, %q) = %v, want %v", tc.have, tc.min, got, tc.want)
		}
	}
}

// floorOf is a LegacyFloor over the versions in *versions, counting reads.
func floorOf(versions *[]string, clock *time.Time, reads *atomic.Int32) *LegacyFloor {
	return &LegacyFloor{
		Now: func() time.Time { return *clock },
		Read: func(context.Context) ([]string, error) {
			reads.Add(1)
			return *versions, nil
		},
	}
}

func TestLegacyFloor_acceptsWhileSomeNodeIsOlderThanTheNoncedRelease(t *testing.T) {
	clock := time.Now()
	var reads atomic.Int32
	for name, tc := range map[string]struct {
		versions []string
		accepts  bool
	}{
		"every node older":                  {[]string{"0.3.0", "0.3.0"}, true},
		"one node older among new ones":     {[]string{NoncedStampsRelease, "0.3.0", "0.4.0"}, true},
		"a node that has not said":          {[]string{NoncedStampsRelease, ""}, true},
		"a node whose version is no number": {[]string{NoncedStampsRelease, "dev"}, true},
		"every node at the release":         {[]string{NoncedStampsRelease, NoncedStampsRelease}, false},
		"every node at or past it":          {[]string{NoncedStampsRelease, "0.4.0", "1.0.0"}, false},
		"one node alone, at the release":    {[]string{NoncedStampsRelease}, false},
		"a cluster with no node recorded":   {nil, false},
	} {
		t.Run(name, func(t *testing.T) {
			versions := tc.versions
			f := floorOf(&versions, &clock, &reads)
			if got := f.Accepts(); got != tc.accepts {
				t.Fatalf("Accepts() = %v, want %v", got, tc.accepts)
			}
		})
	}
}

func TestLegacyFloor_readsTheRegistryOncePerTTLAndFollowsAnUpgrade(t *testing.T) {
	clock := time.Now()
	var reads atomic.Int32
	versions := []string{"0.3.0", NoncedStampsRelease}
	f := floorOf(&versions, &clock, &reads)

	if !f.Accepts() {
		t.Fatal("the older stamps were refused with a node still on 0.3.0")
	}
	versions = []string{NoncedStampsRelease, NoncedStampsRelease}
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
	if f.Accepts() {
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
	versions := []string{NoncedStampsRelease}
	f := &LegacyFloor{
		Now: func() time.Time { return clock },
		Read: func(context.Context) ([]string, error) {
			if fail != nil {
				return nil, fail
			}
			return versions, nil
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
	if f.Accepts() {
		t.Fatal("every node is at the release and the registry answered, but the older stamps are accepted")
	}
	fail = errors.New("no leader")
	clock = clock.Add(legacyFloorTTL)
	if f.Accepts() {
		t.Fatal("a failed read turned the older stamps back on")
	}
	if len(logs) != 2 || !strings.Contains(logs[1], "keeping the last answer") {
		t.Fatalf("logs = %v", logs)
	}
}

// installFloor makes the verifiers and signers of this process follow a floor
// over versions, for the test.
func installFloor(t *testing.T, versions ...string) {
	t.Helper()
	clock := time.Now()
	var reads atomic.Int32
	InstallLegacyFloor(floorOf(&versions, &clock, &reads))
	t.Cleanup(func() { InstallLegacyFloor(nil) })
}

// Below the floor the older forms verify (a rolling upgrade); at it, a request
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

	t.Run("below the floor", func(t *testing.T) {
		installFloor(t, "0.3.0", NoncedStampsRelease)
		if v, ok := CheckCoordination(key, strippedToV2(), now, testAudience); !ok || v != CoordinationV2 {
			t.Fatalf("a v2 stamp from an older node: version %d ok=%v", v, ok)
		}
		if v, ok := CheckCoordination(key, strippedToV1(), now, testAudience); !ok || v != CoordinationV1 {
			t.Fatalf("a v1 stamp from an older node: version %d ok=%v", v, ok)
		}
	})
	t.Run("at the floor", func(t *testing.T) {
		installFloor(t, NoncedStampsRelease, "0.4.0")
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
	installFloor(t, "0.3.0")
	below := signedForPort(t, key, "http://10.0.0.1:10104/x")
	for _, h := range []string{CoordinationMACV3Header, CoordinationMACV2Header, CoordinationMACHeader, CoordinationNonceHeader} {
		if below.Header.Get(h) == "" {
			t.Errorf("below the floor, %s was not written", h)
		}
	}
	installFloor(t, NoncedStampsRelease)
	at := signedForPort(t, key, "http://10.0.0.1:10104/x")
	if at.Header.Get(CoordinationMACV3Header) == "" || at.Header.Get(CoordinationNonceHeader) == "" {
		t.Error("at the floor, the v3 stamp was not written")
	}
	for _, h := range []string{CoordinationMACV2Header, CoordinationMACHeader} {
		if at.Header.Get(h) != "" {
			t.Errorf("at the floor, %s was still written", h)
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
	t.Run("below the floor", func(t *testing.T) {
		installFloor(t, "0.3.0")
		if !VerifyACME(key, stripped(), body, now) {
			t.Fatal("an older Caddy's stamp was refused with an older node in the cluster")
		}
		if signedACME(t, key, body, now).Header.Get(CoordinationMACHeader) == "" {
			t.Fatal("the unnonced stamp was not written")
		}
	})
	t.Run("at the floor", func(t *testing.T) {
		installFloor(t, NoncedStampsRelease)
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
	t.Run("below the floor", func(t *testing.T) {
		installFloor(t, "0.3.0")
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
	t.Run("at the floor", func(t *testing.T) {
		installFloor(t, NoncedStampsRelease)
		r := signedRequest(t, http.MethodPost, nodeRegisterPath, "node-a", body, now)
		if r.Header.Get(NodeStampHeader) != "" {
			t.Fatal("the unnonced stamp was still written")
		}
		// A captured request, stripped of its nonced stamp and given the
		// unnonced one a replayer made from an older capture.
		old := signedRequest(t, http.MethodPost, nodeRegisterPath, "node-a", body, now)
		installFloor(t, "0.3.0")
		legacy := signedRequest(t, http.MethodPost, nodeRegisterPath, "node-a", body, now)
		installFloor(t, NoncedStampsRelease)
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

func TestRegistryLegacyFloor_readsTheVersionsOfTheRegisteredNodes(t *testing.T) {
	var gotQuery string
	var gotArgs []any
	f := RegistryLegacyFloor(versionQuerier(func(_ context.Context, dest any, query string, args ...any) error {
		gotQuery, gotArgs = query, args
		rows := dest.(*[]struct {
			Version string `db:"node_version"`
		})
		*rows = append(*rows, struct {
			Version string `db:"node_version"`
		}{NoncedStampsRelease}, struct {
			Version string `db:"node_version"`
		}{"0.3.0"})
		return nil
	}), nil)
	versions, err := f.Read(context.Background())
	if err != nil || len(versions) != 2 || versions[1] != "0.3.0" {
		t.Fatalf("versions %v, %v", versions, err)
	}
	if !strings.Contains(gotQuery, "FROM dns_nodes") || len(gotArgs) != 1 {
		t.Fatalf("query %q args %v: retired nodes must be left out", gotQuery, gotArgs)
	}
	f = RegistryLegacyFloor(versionQuerier(func(context.Context, any, string, ...any) error { return errors.New("no such column: node_version") }), nil)
	if _, err := f.Read(context.Background()); err == nil || !strings.Contains(err.Error(), "node_version") {
		t.Fatalf("err = %v", err)
	}
}

type versionQuerier func(ctx context.Context, dest any, query string, args ...any) error

func (q versionQuerier) Query(ctx context.Context, dest any, query string, args ...any) error {
	return q(ctx, dest, query, args...)
}
