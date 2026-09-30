//go:build e2e_fleet

package sdkgo

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"io"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/eventually"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
	"github.com/DeBrosOfficial/network/pkg/client"
	orerrors "github.com/DeBrosOfficial/network/pkg/errors"
)

// Pin states the SDK reports (docs/GO_CLIENT_SDK.md "Check Pin Status").
var pinStates = map[string]bool{"pinned": true, "pinning": true, "queued": true, "unpinned": true, "error": true}

// payloadBytes spans several IPFS chunks' worth of random bytes.
const payloadBytes = 300 * 1024

func payload(t testing.TB) []byte {
	t.Helper()
	b := make([]byte, payloadBytes)
	if _, err := rand.Read(b); err != nil {
		t.Fatal(err)
	}
	return b
}

func getAll(t testing.TB, c client.NetworkClient, cid string) []byte {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), callBudget)
	defer cancel()
	rc, err := c.Storage().Get(ctx, cid)
	if err != nil {
		t.Fatalf("Storage().Get(%s): %v", cid, err)
	}
	defer rc.Close()
	data, err := io.ReadAll(rc)
	if err != nil {
		t.Fatalf("reading %s: %v", cid, err)
	}
	return data
}

// TestGoClientStorage_uploadGetPinStatusUnpin walks docs/GO_CLIENT_SDK.md
// "Storage Client" end to end as the namespace owner: Upload returns a CID
// and size, Get returns the same bytes, Pin and Status report a documented
// state that becomes pinned, and Unpin succeeds.
func TestGoClientStorage_uploadGetPinStatusUnpin(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	c := newClient(t, n, owner(n))
	data := payload(t)
	ctx := t.Context()
	up, err := c.Storage().Upload(ctx, bytes.NewReader(data), "e2e-sdk.bin")
	if err != nil {
		t.Fatalf("Storage().Upload: %v", err)
	}
	if up.Cid == "" || up.Size != int64(len(data)) {
		t.Fatalf("upload result %+v, want a CID and size %d", up, len(data))
	}
	if got := getAll(t, c, up.Cid); !bytes.Equal(got, data) {
		t.Fatalf("Get returned %d bytes that differ from the %d uploaded", len(got), len(data))
	}
	pin, err := c.Storage().Pin(ctx, up.Cid, "e2e-sdk.bin")
	if err != nil || pin.Cid != up.Cid {
		t.Fatalf("Storage().Pin: %+v, %v", pin, err)
	}
	eventually.Require(t, pollEvery, pinBudget, "upload pinned", func() (bool, error) {
		st, err := c.Storage().Status(ctx, up.Cid)
		if err != nil {
			return false, err
		}
		if !pinStates[st.Status] {
			return false, eventually.Stop(fmt.Errorf("status %q is not a documented pin state", st.Status))
		}
		if st.Status == "pinned" {
			return true, nil
		}
		return false, fmt.Errorf("status %s", st.Status)
	})
	if err := c.Storage().Unpin(ctx, up.Cid); err != nil {
		t.Fatalf("Storage().Unpin: %v", err)
	}
}

// TestGoClientStorage_emptyAndUnicodeNames: an empty file and a file whose
// name is right-to-left, combining and emoji text round-trip unchanged.
func TestGoClientStorage_emptyAndUnicodeNames(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	c := newClient(t, n, owner(n))
	for name, data := range map[string][]byte{
		"empty.txt":               {},
		"‮שלום-é-\U0001f600.txt": []byte("unicode name"),
	} {
		up, err := c.Storage().Upload(t.Context(), bytes.NewReader(data), name)
		if err != nil {
			t.Errorf("Upload %q: %v", name, err)
			continue
		}
		if got := getAll(t, c, up.Cid); !bytes.Equal(got, data) {
			t.Errorf("%q round-tripped to %q", name, got)
		}
		if err := c.Storage().Unpin(t.Context(), up.Cid); err != nil {
			t.Errorf("Unpin %q: %v", name, err)
		}
	}
}

// TestGoClientStorage_garbageTokenIsTypedUnauthorized: "All client methods
// return typed errors" and errors.IsUnauthorized recognises an auth failure
// (docs/GO_CLIENT_SDK.md "Error Handling").
func TestGoClientStorage_garbageTokenIsTypedUnauthorized(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	c := newClient(t, n, credential{jwt: forgedJWT(n.Name)})
	_, err := c.Storage().Upload(t.Context(), strings.NewReader("x"), "x.txt")
	if err == nil {
		t.Fatal("Upload with a garbage JWT succeeded")
	}
	if !orerrors.IsUnauthorized(err) {
		t.Errorf("errors.IsUnauthorized(%v) = false; the documented error helpers cannot classify the SDK's own errors", err)
	}
}

// forgedJWT is JWT-shaped, names namespace (the SDK reads the claim without
// verifying), and carries a signature no gateway made.
func forgedJWT(namespace string) string {
	enc := base64.RawURLEncoding.EncodeToString
	return enc([]byte(`{"alg":"EdDSA","typ":"JWT"}`)) + "." +
		enc([]byte(`{"Namespace":"`+namespace+`","sub":"0x0000000000000000000000000000000000000000"}`)) + "." +
		enc([]byte("not a signature"))
}

// TestGoClientStorage_runtimeKeyAloneRefused: storage requires a genuine
// logged-in user; an app-runtime API key alone reaches none of it
// (core/pkg/gateway/route_policy.go "storage, webrtc and proxy additionally
// require a genuine logged-in user"). docs/GO_CLIENT_SDK.md's Quick Start
// configures only an API key for its upload, which this contradicts.
func TestGoClientStorage_runtimeKeyAloneRefused(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	c := newClient(t, n, credential{apiKey: tenancy.APIKey(t, n, "app-runtime")})
	if _, err := c.Storage().Upload(t.Context(), strings.NewReader("x"), "x.txt"); err == nil {
		t.Fatal("an app-runtime API key alone uploaded to storage")
	}
}

// TestGoClientStorage_otherNamespaceTokenRefused: a session of namespace B
// cannot use namespace A's storage through A's gateway.
func TestGoClientStorage_otherNamespaceTokenRefused(t *testing.T) {
	t.Parallel()
	pair := tenancy.Namespaces(t, harness.Fleet(t), 2, ns.Options{})
	c := newClient(t, pair[0], owner(pair[1]))
	if _, err := c.Storage().Upload(t.Context(), strings.NewReader("x"), "x.txt"); err == nil {
		t.Fatal("namespace B's token uploaded to namespace A")
	}
}

// TestGoClientStorage_noCredentialRefusedLocally: without an API key or JWT
// the client refuses before sending anything (client.requireAccess).
func TestGoClientStorage_noCredentialRefusedLocally(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	c := newClient(t, n, credential{})
	if _, err := c.Storage().Upload(t.Context(), strings.NewReader("x"), "x.txt"); err == nil ||
		!strings.Contains(err.Error(), "API key or JWT required") {
		t.Fatalf("Upload with no credential: %v", err)
	}
}
