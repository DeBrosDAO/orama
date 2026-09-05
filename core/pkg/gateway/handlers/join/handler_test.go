package join

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"go.uber.org/zap"
)

func TestWgPeersContainsIP_found(t *testing.T) {
	peers := []WGPeerInfo{
		{PublicKey: "key1", Endpoint: "1.2.3.4:51820", AllowedIP: "10.0.0.1/32"},
		{PublicKey: "key2", Endpoint: "5.6.7.8:51820", AllowedIP: "10.0.0.2/32"},
	}

	if !wgPeersContainsIP(peers, "10.0.0.1") {
		t.Error("expected to find 10.0.0.1 in peer list")
	}
	if !wgPeersContainsIP(peers, "10.0.0.2") {
		t.Error("expected to find 10.0.0.2 in peer list")
	}
}

func TestWgPeersContainsIP_not_found(t *testing.T) {
	peers := []WGPeerInfo{
		{PublicKey: "key1", Endpoint: "1.2.3.4:51820", AllowedIP: "10.0.0.1/32"},
	}

	if wgPeersContainsIP(peers, "10.0.0.2") {
		t.Error("did not expect to find 10.0.0.2 in peer list")
	}
}

func TestWgPeersContainsIP_empty_list(t *testing.T) {
	if wgPeersContainsIP(nil, "10.0.0.1") {
		t.Error("did not expect to find any IP in nil peer list")
	}
	if wgPeersContainsIP([]WGPeerInfo{}, "10.0.0.1") {
		t.Error("did not expect to find any IP in empty peer list")
	}
}

func TestAssignWGIP_format(t *testing.T) {
	// Verify the WG IP format used in the handler matches what wgPeersContainsIP expects
	wgIP := "10.0.0.1"
	allowedIP := fmt.Sprintf("%s/32", wgIP)
	peers := []WGPeerInfo{{AllowedIP: allowedIP}}

	if !wgPeersContainsIP(peers, wgIP) {
		t.Errorf("format mismatch: wgPeersContainsIP(%q, %q) should match", allowedIP, wgIP)
	}
}

func TestValidatePublicIP(t *testing.T) {
	tests := []struct {
		name  string
		ip    string
		valid bool
	}{
		{"valid IPv4", "46.225.234.112", true},
		{"loopback", "127.0.0.1", true},
		{"invalid string", "not-an-ip", false},
		{"empty", "", false},
		{"IPv6", "::1", false},
		{"with newline", "1.2.3.4\n5.6.7.8", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			parsed := net.ParseIP(tt.ip)
			isValid := parsed != nil && parsed.To4() != nil && !strings.ContainsAny(tt.ip, "\n\r")
			if isValid != tt.valid {
				t.Errorf("IP %q: expected valid=%v, got %v", tt.ip, tt.valid, isValid)
			}
		})
	}
}

func TestValidateWGPublicKey(t *testing.T) {
	// Valid WireGuard key: 32 bytes, base64 encoded = 44 chars
	validKey := base64.StdEncoding.EncodeToString(make([]byte, 32))

	tests := []struct {
		name  string
		key   string
		valid bool
	}{
		{"valid 32-byte key", validKey, true},
		{"too short", base64.StdEncoding.EncodeToString(make([]byte, 16)), false},
		{"too long", base64.StdEncoding.EncodeToString(make([]byte, 64)), false},
		{"not base64", "not-a-valid-base64-key!!!", false},
		{"empty", "", false},
		{"newline injection", validKey + "\n[Peer]", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if strings.ContainsAny(tt.key, "\n\r") {
				if tt.valid {
					t.Errorf("key %q contains newlines but expected valid", tt.key)
				}
				return
			}
			decoded, err := base64.StdEncoding.DecodeString(tt.key)
			isValid := err == nil && len(decoded) == 32
			if isValid != tt.valid {
				t.Errorf("key %q: expected valid=%v, got %v", tt.key, tt.valid, isValid)
			}
		})
	}
}

// recordingClient captures the statements a handler runs.
type recordingClient struct {
	rqlite.Client
	execs   []string
	execErr error
}

func (c *recordingClient) Exec(_ context.Context, query string, _ ...any) (sql.Result, error) {
	c.execs = append(c.execs, query)
	return nil, c.execErr
}

func writeSecrets(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Join(dir, "secrets"), 0o700); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	for name, content := range files {
		if err := os.WriteFile(filepath.Join(dir, "secrets", name), []byte(content), 0o600); err != nil {
			t.Fatalf("write %s: %v", name, err)
		}
	}
}

func TestReadJoinSecrets_readsEverythingPresent(t *testing.T) {
	dir := t.TempDir()
	writeSecrets(t, dir, map[string]string{
		"cluster-secret":         "cs\n",
		"swarm.key":              "sk\n",
		"api-key-hmac-secret":    "hmac\n",
		"rqlite-password":        "pw\n",
		"secrets-encryption-key": "sek\n",
		"turn-secret":            "turn\n",
	})

	h := &Handler{logger: zap.NewNop(), oramaDir: dir}
	got, err := h.readJoinSecrets()
	if err != nil {
		t.Fatalf("readJoinSecrets: %v", err)
	}

	want := joinSecrets{
		ClusterSecret: "cs", SwarmKey: "sk", APIKeyHMACSecret: "hmac",
		RQLitePassword: "pw", SecretsEncryptionKey: "sek", TURNSecret: "turn",
	}
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestReadJoinSecrets_missingRequiredSecretIsAnError(t *testing.T) {
	// A join that cannot hand over the cluster secret or the swarm key produces
	// a node that will never work. Failing here — before the token is consumed
	// and before a peer row exists — is the entire point of the reordering.
	for _, missing := range []string{"cluster-secret", "swarm.key"} {
		t.Run(missing, func(t *testing.T) {
			files := map[string]string{"cluster-secret": "cs", "swarm.key": "sk"}
			delete(files, missing)

			dir := t.TempDir()
			writeSecrets(t, dir, files)

			h := &Handler{logger: zap.NewNop(), oramaDir: dir}
			if _, err := h.readJoinSecrets(); err == nil {
				t.Fatalf("expected an error when %s is missing", missing)
			}
		})
	}
}

func TestReadJoinSecrets_optionalSecretsMayBeAbsent(t *testing.T) {
	// A cluster installed before these files existed has none of them, and a
	// joining node handles their absence.
	dir := t.TempDir()
	writeSecrets(t, dir, map[string]string{"cluster-secret": "cs", "swarm.key": "sk"})

	h := &Handler{logger: zap.NewNop(), oramaDir: dir}
	got, err := h.readJoinSecrets()
	if err != nil {
		t.Fatalf("readJoinSecrets: %v", err)
	}
	if got.APIKeyHMACSecret != "" || got.RQLitePassword != "" ||
		got.SecretsEncryptionKey != "" || got.TURNSecret != "" {
		t.Fatalf("expected the optional secrets to be empty, got %+v", got)
	}
}

func TestReleaseToken_unConsumesTheToken(t *testing.T) {
	c := &recordingClient{}
	h := &Handler{logger: zap.NewNop(), rqliteClient: c}

	h.releaseToken(context.Background(), "tok")

	if len(c.execs) != 1 {
		t.Fatalf("expected one statement, got %d", len(c.execs))
	}
	stmt := c.execs[0]
	for _, want := range []string{"UPDATE invite_tokens", "used_at = NULL", "WHERE token = ?"} {
		if !strings.Contains(stmt, want) {
			t.Fatalf("statement %q does not contain %q", stmt, want)
		}
	}
}

func TestReleaseToken_logsRatherThanPanickingOnFailure(t *testing.T) {
	// The release is itself the error path; a failure here must not take down
	// the handler, only cost the operator the token.
	c := &recordingClient{execErr: errors.New("no leader")}
	h := &Handler{logger: zap.NewNop(), rqliteClient: c}

	h.releaseToken(context.Background(), "tok")
}

// claimQuery answers refuseIfClaimed's lookup with a fixed set of rows.
type claimQuery struct {
	rqlite.Client
	rows     []claimRow
	queryErr error
	tokenErr error
	// tokenDead makes the liveness gate find no row at all: the token was
	// never issued.
	tokenDead bool
	// tokenUsed and tokenExpired make the row exist but not be live, which is
	// what the two refusals the operator can act on look like.
	tokenUsed    bool
	tokenExpired bool
	// insertErr fails the peer INSERT, the one step after the token is spent.
	insertErr error
	sawQuery  string
	sawArgs   []any
	execs     []string
}

type claimRow struct {
	NodeID    string `db:"node_id"`
	PublicIP  string `db:"public_ip"`
	PublicKey string `db:"public_key"`
}

func (c *claimQuery) Exec(_ context.Context, query string, _ ...any) (sql.Result, error) {
	c.execs = append(c.execs, query)
	if c.insertErr != nil && strings.Contains(query, "INSERT INTO wireguard_peers") {
		return nil, c.insertErr
	}
	return oneRow{}, nil
}

// oneRow is a sql.Result reporting a single affected row, which is what
// consumeToken checks to decide the token was genuinely claimed.
type oneRow struct{}

func (oneRow) LastInsertId() (int64, error) { return 0, nil }
func (oneRow) RowsAffected() (int64, error) { return 1, nil }

func (c *claimQuery) Query(_ context.Context, dest any, query string, args ...any) error {
	// The handler asks two questions. Route on the table so a test can drive
	// each independently.
	if strings.Contains(query, "invite_tokens") {
		if c.tokenErr != nil {
			return c.tokenErr
		}
		if c.tokenDead {
			// No row at all: the token was never issued.
			return nil
		}
		out, ok := dest.(*[]struct {
			Used    int `db:"used"`
			Expired int `db:"expired"`
		})
		if !ok {
			return fmt.Errorf("unexpected token destination type %T", dest)
		}
		row := struct {
			Used    int `db:"used"`
			Expired int `db:"expired"`
		}{}
		if c.tokenUsed {
			row.Used = 1
		}
		if c.tokenExpired {
			row.Expired = 1
		}
		*out = append(*out, row)
		return nil
	}

	out, ok := dest.(*[]struct {
		NodeID    string `db:"node_id"`
		PublicIP  string `db:"public_ip"`
		PublicKey string `db:"public_key"`
	})
	if !ok {
		// Some other read on wireguard_peers — the peer list, or the allocator
		// scanning for a free address. An empty result is a fine answer.
		return nil
	}

	c.sawQuery = query
	c.sawArgs = args
	if c.queryErr != nil {
		return c.queryErr
	}
	for _, r := range c.rows {
		*out = append(*out, struct {
			NodeID    string `db:"node_id"`
			PublicIP  string `db:"public_ip"`
			PublicKey string `db:"public_key"`
		}{r.NodeID, r.PublicIP, r.PublicKey})
	}
	return nil
}

func TestRefuseIfClaimed_allowsAnUnclaimedIdentity(t *testing.T) {
	h := &Handler{logger: zap.NewNop(), rqliteClient: &claimQuery{}}

	err := h.refuseIfClaimed(context.Background(), JoinRequest{
		PublicIP: "203.0.113.9", WGPublicKey: "key", PeerID: "12D3KooWSomething",
	})
	if err != nil {
		t.Fatalf("a fresh identity was refused: %v", err)
	}
}

func TestRefuseIfClaimed_refusesEachClaimedField(t *testing.T) {
	tests := []struct {
		name string
		row  claimRow
		req  JoinRequest
		want string
	}{
		{
			// This is the eviction primitive: naming a running node's public IP
			// so the cleanup below deletes it.
			name: "public IP of a running node",
			row:  claimRow{NodeID: "12D3KooWVictim", PublicIP: "203.0.113.1", PublicKey: "victim-key"},
			req:  JoinRequest{PublicIP: "203.0.113.1", WGPublicKey: "attacker-key"},
			want: "already registered at this public IP",
		},
		{
			name: "WireGuard key of a running node",
			row:  claimRow{NodeID: "12D3KooWVictim", PublicIP: "203.0.113.1", PublicKey: "victim-key"},
			req:  JoinRequest{PublicIP: "203.0.113.9", WGPublicKey: "victim-key"},
			want: "public key is already registered",
		},
		{
			name: "peer id of a running node",
			row:  claimRow{NodeID: "12D3KooWVictim", PublicIP: "203.0.113.1", PublicKey: "victim-key"},
			req:  JoinRequest{PublicIP: "203.0.113.9", WGPublicKey: "attacker-key", PeerID: "12D3KooWVictim"},
			want: "peer id is already registered",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			h := &Handler{logger: zap.NewNop(), rqliteClient: &claimQuery{rows: []claimRow{tc.row}}}

			err := h.refuseIfClaimed(context.Background(), tc.req)
			if err == nil {
				t.Fatal("the join was allowed to displace a node that is up")
			}
			if !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("error %q does not mention %q", err, tc.want)
			}
		})
	}
}

func TestRefuseIfClaimed_onlyConsidersRowsOfNodesThatAreUp(t *testing.T) {
	// A machine retrying after a failed install must be able to reuse its own
	// residue, so the lookup is restricted to live rows. Losing that
	// restriction would block every reinstall; losing the restriction in the
	// other direction — see the predicate test below — reopens the eviction
	// hole, so the query text is asserted directly.
	c := &claimQuery{}
	h := &Handler{logger: zap.NewNop(), rqliteClient: c}

	if err := h.refuseIfClaimed(context.Background(), JoinRequest{
		PublicIP: "203.0.113.9", WGPublicKey: "key",
	}); err != nil {
		t.Fatalf("unexpected refusal: %v", err)
	}
	// Three identities, plus the public IP again for the "rows the cleanup will
	// remove" exemption.
	if len(c.sawArgs) != 4 {
		t.Fatalf("expected the lookup to be parameterised on all three identities plus the cleanup "+
			"exemption, got %d args", len(c.sawArgs))
	}
	assertLivenessPredicate(t, c.sawQuery, "the claim check")
	if !strings.Contains(c.sawQuery, "AND NOT (") {
		t.Fatalf("the claim check must exempt only the rows the cleanup will delete, "+
			"otherwise an unconfirmed row elsewhere still collides with the INSERT and "+
			"releases the token; statement was:\n%s", c.sawQuery)
	}
}

// assertLivenessPredicate checks that a statement restricts itself to rows of
// nodes that are up, by both signals.
//
// confirmed_at alone is not enough: a node still on the old binary nulls its own
// confirmed_at every 60s, so during a rolling upgrade every un-upgraded node
// would read as free to displace. dns_nodes alone is not enough either, since a
// node mid-join has no dns_nodes row yet.
func assertLivenessPredicate(t *testing.T, stmt, what string) {
	t.Helper()
	for _, want := range []string{"confirmed_at IS NOT NULL", "dns_nodes"} {
		if !strings.Contains(stmt, want) {
			t.Fatalf("%s must test %q; statement was:\n%s", what, want, stmt)
		}
	}
}

func TestRemoveUnfinishedJoinRows_neverTouchesALiveNodesRow(t *testing.T) {
	// The public IP is caller-supplied and nothing compares it to the source
	// address, so this delete is the eviction primitive if it is unscoped.
	c := &claimQuery{}
	h := &Handler{logger: zap.NewNop(), rqliteClient: c}

	if err := h.removeUnfinishedJoinRows(context.Background(), "203.0.113.9"); err != nil {
		t.Fatalf("removeUnfinishedJoinRows: %v", err)
	}
	if len(c.execs) != 1 {
		t.Fatalf("expected one statement, got %d", len(c.execs))
	}
	assertLivenessPredicate(t, c.execs[0], "the cleanup delete")
	if !strings.Contains(c.execs[0], "NOT ") {
		t.Fatalf("the cleanup must delete rows that are NOT live; statement was:\n%s", c.execs[0])
	}
}

func TestHandleJoin_conflictDoesNotConsumeTheToken(t *testing.T) {
	// The whole reason releasing a token on failure is safe is that a request
	// naming a live node is refused BEFORE the token is spent. If the order
	// ever inverts, one invite becomes an unlimited eviction primitive.
	c := &claimQuery{rows: []claimRow{
		{NodeID: "12D3KooWVictim", PublicIP: "203.0.113.1", PublicKey: "victim-key"},
	}}
	h := &Handler{logger: zap.NewNop(), rqliteClient: c, oramaDir: t.TempDir()}

	body, err := json.Marshal(JoinRequest{
		Token:       "a-valid-token",
		PublicIP:    "203.0.113.1", // the victim's address
		WGPublicKey: base64.StdEncoding.EncodeToString(make([]byte, 32)),
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	rec := httptest.NewRecorder()
	h.HandleJoin(rec, httptest.NewRequest(http.MethodPost, "/v1/internal/join", bytes.NewReader(body)))

	if rec.Code != http.StatusConflict {
		t.Fatalf("got %d, want %d — a join naming a live node must be refused", rec.Code, http.StatusConflict)
	}
	for _, stmt := range c.execs {
		if strings.Contains(stmt, "invite_tokens") {
			t.Fatalf("the token was touched on a refused join: %q", stmt)
		}
		if strings.Contains(stmt, "DELETE FROM wireguard_peers") {
			t.Fatalf("the refused join still deleted peer rows: %q", stmt)
		}
	}
}

func TestHandleJoin_unreadableClaimCheckIsNotAConflict(t *testing.T) {
	// A backend outage must not be reported as a conflict, and its detail must
	// not reach an unauthenticated caller.
	c := &claimQuery{queryErr: errors.New("rqlite: no leader, node 10.0.0.7:10101 unreachable")}
	h := &Handler{logger: zap.NewNop(), rqliteClient: c, oramaDir: t.TempDir()}

	body, err := json.Marshal(JoinRequest{
		Token:       "a-valid-token",
		PublicIP:    "203.0.113.1",
		WGPublicKey: base64.StdEncoding.EncodeToString(make([]byte, 32)),
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	rec := httptest.NewRecorder()
	h.HandleJoin(rec, httptest.NewRequest(http.MethodPost, "/v1/internal/join", bytes.NewReader(body)))

	if rec.Code != http.StatusServiceUnavailable {
		t.Fatalf("got %d, want %d", rec.Code, http.StatusServiceUnavailable)
	}
	if strings.Contains(rec.Body.String(), "10.0.0.7") {
		t.Fatalf("the response leaked internal state: %q", rec.Body.String())
	}
	if len(c.execs) != 0 {
		t.Fatalf("a request that could not be checked still wrote: %v", c.execs)
	}
}

func TestRefuseIfClaimed_readFailureRefusesTheJoin(t *testing.T) {
	// If we cannot tell whether the identity is claimed, we must not proceed to
	// a cleanup that deletes rows.
	h := &Handler{logger: zap.NewNop(), rqliteClient: &claimQuery{queryErr: errors.New("no leader")}}

	if err := h.refuseIfClaimed(context.Background(), JoinRequest{
		PublicIP: "203.0.113.9", WGPublicKey: "key",
	}); err == nil {
		t.Fatal("a failed check must refuse the join, not allow it")
	}
}

func TestHandleJoin_refusesBeforeReachingTheClaimCheckWithoutALiveToken(t *testing.T) {
	// The claim check answers whether an IP, key or peer id belongs to a live
	// node. Reachable without a token, that is a fleet-enumeration oracle.
	c := &claimQuery{tokenDead: true}
	h := &Handler{logger: zap.NewNop(), rqliteClient: c, oramaDir: t.TempDir()}

	body, err := json.Marshal(JoinRequest{
		Token:       "not-a-real-token",
		PublicIP:    "203.0.113.1",
		WGPublicKey: base64.StdEncoding.EncodeToString(make([]byte, 32)),
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	rec := httptest.NewRecorder()
	h.HandleJoin(rec, httptest.NewRequest(http.MethodPost, "/v1/internal/join", bytes.NewReader(body)))

	if rec.Code != http.StatusUnauthorized {
		t.Fatalf("got %d, want %d", rec.Code, http.StatusUnauthorized)
	}
	if c.sawQuery != "" {
		t.Fatalf("the claim check ran without a live token: %q", c.sawQuery)
	}
}

func TestHandleJoin_conflictResponseNamesNoFleetState(t *testing.T) {
	c := &claimQuery{rows: []claimRow{
		{NodeID: "12D3KooWVictim", PublicIP: "203.0.113.1", PublicKey: "victim-key"},
	}}
	h := &Handler{logger: zap.NewNop(), rqliteClient: c, oramaDir: t.TempDir()}

	body, err := json.Marshal(JoinRequest{
		Token:       "a-valid-token",
		PublicIP:    "203.0.113.1",
		WGPublicKey: base64.StdEncoding.EncodeToString(make([]byte, 32)),
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	rec := httptest.NewRecorder()
	h.HandleJoin(rec, httptest.NewRequest(http.MethodPost, "/v1/internal/join", bytes.NewReader(body)))

	for _, leak := range []string{"public IP", "public key", "peer id", "12D3KooWVictim"} {
		if strings.Contains(rec.Body.String(), leak) {
			t.Fatalf("the 409 body names which identity collided (%q): %q", leak, rec.Body.String())
		}
	}
}

// joinableHandler builds a Handler whose local state reads all succeed, so a
// test can exercise HandleJoin past the reads and into the writes.
func joinableHandler(t *testing.T, c rqlite.Client) *Handler {
	t.Helper()
	dir := t.TempDir()
	writeSecrets(t, dir, map[string]string{
		"cluster-secret": "cs",
		"swarm.key":      "sk",
		"wg-public-key":  "local-key",
	})

	restore := func(ip func() (string, error), key func(string) (string, error), pub func() (string, error)) {
		readLocalWGIP, readLocalWGPublicKey, readLocalPublicIP = ip, key, pub
	}
	prevIP, prevKey, prevPub := readLocalWGIP, readLocalWGPublicKey, readLocalPublicIP
	t.Cleanup(func() { restore(prevIP, prevKey, prevPub) })

	readLocalWGIP = func() (string, error) { return "10.0.0.1", nil }
	readLocalWGPublicKey = func(string) (string, error) { return "local-key", nil }
	readLocalPublicIP = func() (string, error) { return "203.0.113.250", nil }

	return &Handler{logger: zap.NewNop(), rqliteClient: c, oramaDir: dir}
}

func joinBody(t *testing.T, publicIP string) []byte {
	t.Helper()
	body, err := json.Marshal(JoinRequest{
		Token:       "a-valid-token",
		PublicIP:    publicIP,
		WGPublicKey: base64.StdEncoding.EncodeToString(make([]byte, 32)),
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	return body
}

func TestHandleJoin_identityConflictDoesNotReleaseTheToken(t *testing.T) {
	// Belt and braces behind the pre-check. If a uniqueness conflict still
	// reaches the INSERT, the request named a bad identity — a caller error.
	// Releasing the token on it is what made one invite replayable for ever:
	// collide on purpose, get the token back, repeat.
	c := &claimQuery{insertErr: errors.New("UNIQUE constraint failed: wireguard_peers.public_key")}
	h := joinableHandler(t, c)

	rec := httptest.NewRecorder()
	h.HandleJoin(rec, httptest.NewRequest(http.MethodPost, "/v1/internal/join",
		bytes.NewReader(joinBody(t, "203.0.113.9"))))

	if rec.Code != http.StatusConflict {
		t.Fatalf("got %d, want %d (body %q, statements %v)", rec.Code, http.StatusConflict,
			rec.Body.String(), c.execs)
	}
	for _, stmt := range c.execs {
		if strings.Contains(stmt, "used_at = NULL") {
			t.Fatalf("the token was released on an identity conflict, making it replayable: %q", stmt)
		}
	}
}

func TestHandleJoin_serverFailureDoesReleaseTheToken(t *testing.T) {
	// The other side of the same rule: a genuine cluster fault is not the
	// operator's fault, and must not cost them the invite.
	c := &claimQuery{insertErr: errors.New("database is locked")}
	h := joinableHandler(t, c)

	rec := httptest.NewRecorder()
	h.HandleJoin(rec, httptest.NewRequest(http.MethodPost, "/v1/internal/join",
		bytes.NewReader(joinBody(t, "203.0.113.9"))))

	if rec.Code != http.StatusInternalServerError {
		t.Fatalf("got %d, want %d", rec.Code, http.StatusInternalServerError)
	}
	released := false
	for _, stmt := range c.execs {
		if strings.Contains(stmt, "used_at = NULL") {
			released = true
		}
	}
	if !released {
		t.Fatal("a cluster fault burned the operator's token")
	}
}

// Removing a node revokes its key rather than deleting the row, so the machine
// cannot re-admit itself. That makes the join the only way back, and the join
// has to clear the row — otherwise `orama node remove` is a one-way door and a
// rebuilt machine with the same peer id can never register again.
//
// The same applies to a node that lost its key file while keeping its
// identity.key: its new key would not match the recorded one, and enrolment
// refuses that.
func TestHandleJoin_clearsTheRecordedKeyOfTheJoiningNode(t *testing.T) {
	c := &claimQuery{}
	h := joinableHandler(t, c)

	body, err := json.Marshal(JoinRequest{
		Token:       "a-valid-token",
		PublicIP:    "203.0.113.9",
		PeerID:      "12D3KooWEyoppNCUx8Yx66oV9fJnriXwCcXwDDUA2kj6vnc6iDEg",
		WGPublicKey: base64.StdEncoding.EncodeToString(make([]byte, 32)),
	})
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	rec := httptest.NewRecorder()
	h.HandleJoin(rec, httptest.NewRequest(http.MethodPost, "/v1/internal/join", bytes.NewReader(body)))

	cleared := false
	for _, stmt := range c.execs {
		if strings.Contains(stmt, "DELETE FROM node_credentials") {
			cleared = true
		}
	}
	if !cleared {
		t.Errorf("the join did not clear the joining node's recorded key, so a removed or rebuilt "+
			"machine can never register again: %q", c.execs)
	}
}

// A join that names no peer id has no credential to clear, and must not issue a
// delete with an empty id — every row with an empty node_id would go.
func TestHandleJoin_withNoPeerIdClearsNothing(t *testing.T) {
	c := &claimQuery{}
	h := joinableHandler(t, c)

	rec := httptest.NewRecorder()
	h.HandleJoin(rec, httptest.NewRequest(http.MethodPost, "/v1/internal/join",
		bytes.NewReader(joinBody(t, "203.0.113.9"))))

	for _, stmt := range c.execs {
		if strings.Contains(stmt, "node_credentials") {
			t.Errorf("a join with no peer id touched node_credentials: %q", stmt)
		}
	}
}
