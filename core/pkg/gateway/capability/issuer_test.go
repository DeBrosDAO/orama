package capability

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/wssession"
)

// revokedTokensTable is the revoked_tokens table, enough of it for the list to
// record a revocation and read it back.
type revokedTokensTable struct {
	client.DatabaseClient
	mu   sync.Mutex
	rows [][]interface{}
}

func (t *revokedTokensTable) Query(_ context.Context, sql string, args ...interface{}) (*client.QueryResult, error) {
	t.mu.Lock()
	defer t.mu.Unlock()
	if strings.HasPrefix(strings.TrimSpace(sql), "INSERT") {
		t.rows = append(t.rows, []interface{}{args[0], args[1], args[2], args[3]})
		return &client.QueryResult{Count: 1}, nil
	}
	out := &client.QueryResult{}
	for _, row := range t.rows {
		jti, _ := row[0].(string)
		subject, _ := row[1].(string)
		out.Rows = append(out.Rows, []interface{}{jti, subject, row[2], row[3]})
	}
	out.Count = int64(len(out.Rows))
	return out, nil
}

func issuer(t *testing.T) (*Issuer, *auth.RevocationList) {
	t.Helper()
	table := &revokedTokensTable{}
	list := auth.NewRevocationList(func() client.DatabaseClient { return table }, nil)
	i, err := NewIssuer(authority(t), list)
	if err != nil {
		t.Fatalf("issuer: %v", err)
	}
	return i, list
}

func denied(list *auth.RevocationList, c *Claims) bool {
	return list.Denies(c.RevocationClaims(), nil)
}

func TestIssuer_MintReturnsWhatTheTokenGrants(t *testing.T) {
	i, _ := issuer(t)
	grant, token, err := i.Mint(context.Background(), "anchat", "rpc-router", "mailbox-7", "device-1", time.Hour)
	if err != nil {
		t.Fatalf("mint: %v", err)
	}
	claims, err := i.authority.Verify(token, "anchat", "rpc-router", time.Now())
	if err != nil {
		t.Fatalf("the minted token does not verify: %v", err)
	}
	if *grant != *claims.Grant() {
		t.Errorf("grant %+v, token %+v", grant, claims.Grant())
	}
}

// Revoked by its id, in its own namespace only; or with the device that
// issued it.
func TestIssuer_RevokeRefusesTheCapabilityAndNoOther(t *testing.T) {
	i, list := issuer(t)
	ctx := context.Background()
	token, c, _ := i.authority.Mint("anchat", "rpc-router", "m", "device-1", time.Hour, time.Now())
	_, sameIDElsewhere, _ := i.authority.Mint("other", "rpc-router", "m", "device-2", time.Hour, time.Now())
	sameIDElsewhere.ID = c.ID

	if err := i.Revoke(ctx, "anchat", token); err != nil {
		t.Fatalf("revoke: %v", err)
	}
	list.Refresh(ctx)
	if !denied(list, c) {
		t.Error("a revoked capability is still accepted")
	}
	if denied(list, sameIDElsewhere) {
		t.Error("revoking a capability in one namespace refused another namespace's")
	}

	_, fromDevice, _ := i.authority.Mint("anchat", "rpc-router", "m", "device-3", time.Hour, time.Now())
	if err := list.RevokeDevice(ctx, "device-3"); err != nil {
		t.Fatalf("revoke device: %v", err)
	}
	if !denied(list, fromDevice) {
		t.Error("a capability from a revoked device is still accepted")
	}
}

// The bug: a device's revocation was kept one access-token lifetime, an hour,
// then pruned — and a capability the device had issued, living up to a week,
// was accepted again. It must be kept as long as any capability can live.
func TestRevokeDevice_isRememberedAsLongAsACapabilityLives(t *testing.T) {
	table := &revokedTokensTable{}
	list := auth.NewRevocationList(func() client.DatabaseClient { return table }, nil)
	if err := list.RevokeDevice(context.Background(), "device-1"); err != nil {
		t.Fatalf("revoke device: %v", err)
	}
	if len(table.rows) != 1 {
		t.Fatalf("recorded %d rows", len(table.rows))
	}
	expiresAt, _ := table.rows[0][3].(int64)
	if until := time.Until(time.Unix(expiresAt, 0)); until < MaxTTL-time.Minute {
		t.Errorf("the device's revocation is kept %s; a capability it issued lives up to %s", until, MaxTTL)
	}
}

// Only a capability the namespace was issued can be put on the list: an id, a
// forgery or another namespace's capability would let a function fill a table
// every gateway reloads with rows of its choosing.
func TestIssuer_RevokeTakesOnlyThisNamespacesCapabilities(t *testing.T) {
	i, _ := issuer(t)
	forger, _ := NewAuthority("another-cluster")
	forged, _, _ := forger.Mint("anchat", "rpc-router", "m", "d", time.Hour, time.Now())
	elsewhere, c, _ := i.authority.Mint("other", "rpc-router", "m", "d", time.Hour, time.Now())
	for name, token := range map[string]string{
		"a bare id": c.ID, "empty": "", "a forgery": forged, "another namespace's": elsewhere,
		"oversized": strings.Repeat("a", maxTokenLength+1),
	} {
		if err := i.Revoke(context.Background(), "anchat", token); !errors.Is(err, ErrInvalid) {
			t.Errorf("revoking %s: %v, want ErrInvalid", name, err)
		}
	}
	if _, err := NewIssuer(nil, nil); err == nil {
		t.Error("an issuer with nothing to mint or revoke with was built")
	}
}

// A row lives as long as the capability does, and one capability is one row:
// revoking it again, or revoking one that expired, writes nothing.
func TestIssuer_RevokeWritesOneRowThatEndsWithTheCapability(t *testing.T) {
	table := &revokedTokensTable{}
	list := auth.NewRevocationList(func() client.DatabaseClient { return table }, nil)
	i, err := NewIssuer(authority(t), list)
	if err != nil {
		t.Fatalf("issuer: %v", err)
	}
	ctx := context.Background()
	token, c, _ := i.authority.Mint("anchat", "rpc-router", "m", "d", time.Hour, time.Now())
	expired, _, _ := i.authority.Mint("anchat", "rpc-router", "m", "d", time.Minute, time.Now().Add(-time.Hour))

	for range 3 {
		if err := i.Revoke(ctx, "anchat", token); err != nil {
			t.Fatalf("revoke: %v", err)
		}
	}
	if err := i.Revoke(ctx, "anchat", expired); err != nil {
		t.Fatalf("revoking an expired capability: %v", err)
	}
	if len(table.rows) != 1 {
		t.Fatalf("%d rows written, want 1", len(table.rows))
	}
	want := c.ExpiresAt + int64(wssession.ExpiryGrace/time.Second)
	if expiresAt, _ := table.rows[0][3].(int64); expiresAt != want {
		t.Errorf("the row expires at %d; the capability's sockets may last until %d", expiresAt, want)
	}
}
