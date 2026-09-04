package join

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"os"
	"os/exec"
	"strings"
	"time"

	"path/filepath"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/overlay"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/libp2p/go-libp2p/core/crypto"
	"github.com/libp2p/go-libp2p/core/peer"
	"go.uber.org/zap"
)

// JoinRequest is the request body for node join
type JoinRequest struct {
	Token       string `json:"token"`
	WGPublicKey string `json:"wg_public_key"`
	PublicIP    string `json:"public_ip"`

	// PeerID is the joining node's libp2p peer id. Optional, because an older
	// installer does not send one; when absent the row falls back to a
	// synthetic id, which is what every row used to carry.
	PeerID string `json:"peer_id,omitempty"`
}

// JoinResponse contains everything a joining node needs
type JoinResponse struct {
	// WireGuard
	WGIP    string       `json:"wg_ip"`
	WGPeers []WGPeerInfo `json:"wg_peers"`

	// Secrets
	ClusterSecret    string `json:"cluster_secret"`
	SwarmKey         string `json:"swarm_key"`
	APIKeyHMACSecret string `json:"api_key_hmac_secret,omitempty"`
	RQLitePassword   string `json:"rqlite_password,omitempty"`
	// Unused: Olric v0.7.0 YAML loader ignores encryptionKey (bugboard #246).
	// Kept so older joiners still decode the field.
	OlricEncryptionKey string `json:"olric_encryption_key,omitempty"`
	// Serverless secrets encryption key (bugboard #837) — must be identical on
	// every node so namespace function secrets decrypt cluster-wide.
	SecretsEncryptionKey string `json:"secrets_encryption_key,omitempty"`
	// TURN shared secret (feat-124 #913) — must be identical on every node so
	// WebRTC TURN credentials validate cluster-wide.
	TURNSecret string `json:"turn_secret,omitempty"`

	// Cluster join info (all using WG IPs)
	RQLiteJoinAddress  string   `json:"rqlite_join_address"`
	IPFSPeer           PeerInfo `json:"ipfs_peer"`
	IPFSClusterPeer    PeerInfo `json:"ipfs_cluster_peer"`
	IPFSClusterPeerIDs []string `json:"ipfs_cluster_peer_ids,omitempty"`
	BootstrapPeers     []string `json:"bootstrap_peers"`

	// Olric seed peers (WG IP:port for memberlist)
	OlricPeers []string `json:"olric_peers,omitempty"`

	// Domain
	BaseDomain string `json:"base_domain"`
}

// WGPeerInfo represents a WireGuard peer
type WGPeerInfo struct {
	PublicKey string `json:"public_key"`
	Endpoint  string `json:"endpoint"`
	AllowedIP string `json:"allowed_ip"`
}

// PeerInfo represents an IPFS/Cluster peer
type PeerInfo struct {
	ID    string   `json:"id"`
	Addrs []string `json:"addrs"`
}

// Handler handles the node join endpoint
type Handler struct {
	logger       *zap.Logger
	rqliteClient rqlite.Client
	oramaDir     string // e.g., /opt/orama/.orama
}

// NewHandler creates a new join handler
func NewHandler(logger *zap.Logger, rqliteClient rqlite.Client, oramaDir string) *Handler {
	return &Handler{
		logger:       logger,
		rqliteClient: rqliteClient,
		oramaDir:     oramaDir,
	}
}

// HandleJoin handles POST /v1/internal/join
func (h *Handler) HandleJoin(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return
	}

	r.Body = http.MaxBytesReader(w, r.Body, 1<<20) // 1MB
	var req JoinRequest
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return
	}

	if req.Token == "" || req.WGPublicKey == "" || req.PublicIP == "" {
		http.Error(w, "token, wg_public_key, and public_ip are required", http.StatusBadRequest)
		return
	}

	// Validate public IP format
	if net.ParseIP(req.PublicIP) == nil || net.ParseIP(req.PublicIP).To4() == nil {
		http.Error(w, "public_ip must be a valid IPv4 address", http.StatusBadRequest)
		return
	}

	// Validate WireGuard public key: must be base64-encoded 32 bytes (Curve25519)
	// Also reject control characters (newlines) to prevent config injection
	if strings.ContainsAny(req.WGPublicKey, "\n\r") {
		http.Error(w, "wg_public_key contains invalid characters", http.StatusBadRequest)
		return
	}
	wgKeyBytes, err := base64.StdEncoding.DecodeString(req.WGPublicKey)
	if err != nil || len(wgKeyBytes) != 32 {
		http.Error(w, "wg_public_key must be a valid base64-encoded 32-byte key", http.StatusBadRequest)
		return
	}

	// The peer id becomes this row's primary key and is read back as a peer id
	// by every consumer of the table. It is validated here with the other
	// fields rather than deep inside the write, so a malformed one costs
	// nothing: past this point the token is spent and rows have been deleted.
	if req.PeerID != "" {
		if _, err := peer.Decode(req.PeerID); err != nil {
			http.Error(w, "peer_id must be a valid libp2p peer id", http.StatusBadRequest)
			return
		}
	}

	ctx := r.Context()

	// Refuse a join that would displace a node that is already up.
	//
	// This is a validation, so it runs with the others, before anything is read
	// off disk and long before anything is written. It is what keeps the token
	// honest: with it, nothing an attacker controls can make the mutating half
	// below fail, so releasing the token on failure cannot be farmed. Without
	// it, a token holder could name any running node's public IP, have the
	// cleanup evict it, collide on the public key so the registration failed,
	// get the token back, and repeat — evicting any node from the mesh,
	// indefinitely, from one invite.
	// Prove the token is live before doing anything on its behalf.
	//
	// It is NOT consumed here — that stays atomic and happens once, below. This
	// only stops an anonymous caller reaching the work that follows: the claim
	// check answers whether a given IP, key or peer id belongs to a live node,
	// which is a fleet-enumeration oracle if anyone can ask it, and the reads
	// after it load every cluster secret off disk and shell out to `ip`.
	if err := h.assertTokenLive(ctx, req.Token); err != nil {
		if !isTokenRefusal(err) {
			// Not being able to read the table is an outage, not a bad token.
			// Answering 401 would tell the operator to mint a new invite,
			// which would fail the same way.
			h.logger.Error("could not verify the invite token", zap.Error(err))
			http.Error(w, "cannot verify the invite right now, retry shortly", http.StatusServiceUnavailable)
			return
		}
		h.logger.Warn("join rejected", zap.Error(err))
		http.Error(w, tokenRefusal(err), http.StatusUnauthorized)
		return
	}

	if err := h.refuseIfClaimed(ctx, req); err != nil {
		if errors.Is(err, errClaimCheckUnavailable) {
			// A backend outage is not a conflict, and its detail is not for an
			// unauthenticated caller.
			h.logger.Error("could not check whether the joining identity is already registered",
				zap.Error(err))
			http.Error(w, "cannot verify the request right now, retry shortly", http.StatusServiceUnavailable)
			return
		}
		// The reason is logged, not returned: which of the three identities
		// collided is fleet state, and this is still a pre-consume path.
		h.logger.Warn("join rejected: the identity is already registered",
			zap.Error(err), zap.String("public_ip", req.PublicIP))
		http.Error(w, "identity already registered", http.StatusConflict)
		return
	}

	// Everything that can fail WITHOUT touching cluster state happens first:
	// the secrets, this node's own WireGuard identity, the peer list. Only then
	// is the token burned and the peer row written.
	//
	// The old order was the other way round — consume token, write the row, add
	// the peer to wg0, and only THEN read six files off disk. Any failure after
	// the write (an unreadable swarm.key, a joining node that crashed
	// mid-install, a tunnel verification that never came back) left a consumed
	// token the operator could not reuse and a wireguard_peers row every
	// survivor re-applied to its interface every 60 seconds, for ever, with
	// nothing able to tell it from a real peer.
	secrets, err := h.readJoinSecrets()
	if err != nil {
		h.logger.Error("failed to read join secrets", zap.Error(err))
		http.Error(w, "internal error reading secrets", http.StatusInternalServerError)
		return
	}

	myWGIP, err := readLocalWGIP()
	if err != nil {
		h.logger.Error("failed to get local WG IP", zap.Error(err))
		http.Error(w, "internal error", http.StatusInternalServerError)
		return
	}

	wgPeers, err := h.getWGPeers(ctx, req.WGPublicKey)
	if err != nil {
		h.logger.Error("failed to list WG peers", zap.Error(err))
		http.Error(w, "failed to list peers", http.StatusInternalServerError)
		return
	}

	// Ensure this node (the join handler's host) is in the peer list.
	// On a fresh genesis node, the WG sync loop may not have self-registered
	// into wireguard_peers yet, causing 0 peers to be returned.
	if !wgPeersContainsIP(wgPeers, myWGIP) {
		myPubKey, err := readLocalWGPublicKey(h.oramaDir)
		if err != nil {
			h.logger.Error("failed to get local WG public key", zap.Error(err))
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		myPublicIP, err := readLocalPublicIP()
		if err != nil {
			h.logger.Error("failed to get local public IP", zap.Error(err))
			http.Error(w, "internal error", http.StatusInternalServerError)
			return
		}
		wgPeers = append([]WGPeerInfo{{
			PublicKey: myPubKey,
			Endpoint:  fmt.Sprintf("%s:%d", myPublicIP, 51820),
			AllowedIP: fmt.Sprintf("%s/32", myWGIP),
		}}, wgPeers...)
		h.logger.Info("self-injected into WG peer list (sync loop hasn't registered yet)",
			zap.String("wg_ip", myWGIP))
	}

	// From here on the request mutates cluster state.

	// 1. Validate and consume the invite token (atomic single-use)
	if err := h.consumeToken(ctx, req.Token, req.PublicIP); err != nil {
		h.logger.Warn("join token validation failed", zap.Error(err))
		http.Error(w, "unauthorized: invalid or expired token", http.StatusUnauthorized)
		return
	}

	// 1b. Look up the operator wallet from the consumed token (may be empty for legacy tokens)
	operatorWallet := h.tokenOperatorWallet(ctx, req.Token)

	// 2. Clean up the residue of this machine's previous, unfinished joins.
	if err := h.removeUnfinishedJoinRows(ctx, req.PublicIP); err != nil {
		h.logger.Warn("failed to clean up stale WG entries", zap.Error(err))
		// Non-fatal: proceed with join
	}

	// 3. Allocate an overlay address and register the peer, as one retryable step.
	wgIP, err := overlay.Register(ctx, h.rqliteClient, overlay.Peer{
		NodeID:         req.PeerID,
		PublicKey:      req.WGPublicKey,
		PublicIP:       req.PublicIP,
		OperatorWallet: operatorWallet,
	})
	if err != nil {
		// A uniqueness conflict here means the request named an identity the
		// pre-check just cleared. That is the caller's problem, not the
		// cluster's, and releasing the token on it is precisely what would
		// make the token replayable.
		if overlay.IsConflict(err) {
			h.logger.Warn("join rejected: identity conflict after the pre-check",
				zap.Error(err), zap.String("public_ip", req.PublicIP))
			http.Error(w, "identity already registered", http.StatusConflict)
			return
		}
		h.logger.Error("failed to register WG peer", zap.Error(err))
		h.releaseToken(ctx, req.Token)
		http.Error(w, "failed to register peer", http.StatusInternalServerError)
		return
	}

	// 4. Add peer to local WireGuard interface immediately.
	//    A failure here is not fatal — the sync loop applies the row within a
	//    minute — but it is the last step that can fail, so anything worse than
	//    a warning would have to undo the row above.
	if err := h.addWGPeerLocally(req.WGPublicKey, req.PublicIP, wgIP); err != nil {
		h.logger.Warn("failed to add WG peer to local interface", zap.Error(err))
	}

	// 5. Query IPFS and IPFS Cluster peer info
	ipfsPeer := h.queryIPFSPeerInfo(myWGIP)
	ipfsClusterPeer := h.queryIPFSClusterPeerInfo(myWGIP)

	// 6. Get this node's libp2p peer ID for bootstrap peers
	bootstrapPeers := h.buildBootstrapPeers(myWGIP, ipfsPeer.ID)

	// 7. Read base domain from config
	baseDomain := h.readBaseDomain()

	// 8. Read IPFS Cluster trusted peer IDs
	ipfsClusterPeerIDs := h.readIPFSClusterTrustedPeers()

	// Build Olric seed peers from all existing WG peer IPs (memberlist port 3322)
	var olricPeers []string
	for _, p := range wgPeers {
		peerIP := strings.TrimSuffix(p.AllowedIP, "/32")
		olricPeers = append(olricPeers, fmt.Sprintf("%s:3322", peerIP))
	}
	// Include this node too
	olricPeers = append(olricPeers, fmt.Sprintf("%s:3322", myWGIP))

	resp := JoinResponse{
		WGIP:                 wgIP,
		WGPeers:              wgPeers,
		ClusterSecret:        secrets.ClusterSecret,
		SwarmKey:             secrets.SwarmKey,
		APIKeyHMACSecret:     secrets.APIKeyHMACSecret,
		RQLitePassword:       secrets.RQLitePassword,
		SecretsEncryptionKey: secrets.SecretsEncryptionKey,
		TURNSecret:           secrets.TURNSecret,
		RQLiteJoinAddress:    constants.RQLiteRaftAddrFor(myWGIP),
		IPFSPeer:             ipfsPeer,
		IPFSClusterPeer:      ipfsClusterPeer,
		IPFSClusterPeerIDs:   ipfsClusterPeerIDs,
		BootstrapPeers:       bootstrapPeers,
		OlricPeers:           olricPeers,
		BaseDomain:           baseDomain,
	}

	w.Header().Set("Content-Type", "application/json")
	json.NewEncoder(w).Encode(resp)

	h.logger.Info("node joined cluster",
		zap.String("wg_ip", wgIP),
		zap.String("public_ip", req.PublicIP))
}

// assertTokenLive reports whether the token exists, is unused and unexpired,
// WITHOUT consuming it.
//
// Consumption stays atomic and single, in consumeToken. This is only a gate so
// that unauthenticated callers cannot reach the work that follows.
func (h *Handler) assertTokenLive(ctx context.Context, token string) error {
	var rows []struct {
		Used    int `db:"used"`
		Expired int `db:"expired"`
	}
	// The row is fetched with the two predicates as columns rather than as a
	// filter, so a token that exists can be told apart from one that does not,
	// and a used one from an expired one. The single "invalid or expired"
	// answer sent an operator whose install had failed part way through
	// looking for a clock problem when the token had simply been spent.
	//
	// Saying which is not an oracle: distinguishing them requires already
	// holding a 32-byte random token, so anyone who can ask has nothing left
	// to learn.
	if err := h.rqliteClient.Query(ctx, &rows,
		`SELECT CASE WHEN used_at IS NOT NULL THEN 1 ELSE 0 END AS used,
		        CASE WHEN expires_at <= CURRENT_TIMESTAMP THEN 1 ELSE 0 END AS expired
		   FROM invite_tokens WHERE token = ?`,
		token); err != nil {
		return fmt.Errorf("could not read the invite token: %w", err)
	}

	if len(rows) == 0 {
		return errTokenUnknown
	}
	if rows[0].Used == 1 {
		return errTokenUsed
	}
	if rows[0].Expired == 1 {
		return errTokenExpired
	}
	return nil
}

// Why a token was refused. Each one tells the operator something different
// about what to do next.
var (
	// errTokenUnknown means no invite has ever had this value.
	errTokenUnknown = errors.New("no invite matches this token")
	// errTokenUsed means a node already joined with it. Invites are
	// single-use, so this is what a retry after a partly-failed install sees.
	errTokenUsed = errors.New("this invite has already been used")
	// errTokenExpired means it was never used and its window has passed.
	errTokenExpired = errors.New("this invite has expired")
)

// isTokenRefusal reports whether the error is a verdict about the token, as
// opposed to not having been able to reach a verdict at all.
func isTokenRefusal(err error) bool {
	return errors.Is(err, errTokenUsed) ||
		errors.Is(err, errTokenExpired) ||
		errors.Is(err, errTokenUnknown)
}

// tokenRefusal renders why a token was refused, for the joining node.
func tokenRefusal(err error) string {
	switch {
	case errors.Is(err, errTokenUsed):
		return "unauthorized: this invite has already been used. Invites are single-use — mint another with 'orama invite'"
	case errors.Is(err, errTokenExpired):
		return "unauthorized: this invite has expired. Mint another with 'orama invite'"
	default:
		return "unauthorized: no invite matches this token"
	}
}

// errClaimCheckUnavailable marks a refusal caused by not being able to read the
// table, as opposed to a genuine conflict. The two get very different responses.
var errClaimCheckUnavailable = errors.New("claim check unavailable")

// liveRowPredicate matches wireguard_peers rows belonging to a node that is up.
//
// Liveness is `confirmed_at IS NOT NULL` OR a dns_nodes row at the same overlay
// address, and it needs both halves.
//
// confirmed_at alone is not enough during a rolling upgrade of this very
// change. A node still on the old binary self-registers with INSERT OR REPLACE,
// which deletes and re-inserts its row, so it nulls its own confirmed_at every
// 60 seconds — within a minute of migration 038 backfilling it. The deploy
// order this change requires (upgrade the join-serving node first) is exactly
// the arrangement where a new gateway would then read every old node as
// unconfirmed and let a token holder evict it.
//
// dns_nodes is written the same way by both binaries, so it has no such hole.
// Neither signal is sufficient alone — a node mid-join has no dns_nodes row
// yet — so the check is the disjunction.
const liveRowPredicate = `(w.confirmed_at IS NOT NULL
       OR EXISTS (SELECT 1 FROM dns_nodes n WHERE n.internal_ip = w.wg_ip))`

// refuseIfClaimed reports an error when any identity in the request already
// belongs to a node that is up.
//
// Only live rows count. A row that is neither confirmed nor backed by a
// dns_nodes entry is the residue of a join that did not finish — possibly this
// machine's own previous attempt — and standing aside for it would make a retry
// after a failed install impossible.
func (h *Handler) refuseIfClaimed(ctx context.Context, req JoinRequest) error {
	var rows []struct {
		NodeID    string `db:"node_id"`
		PublicIP  string `db:"public_ip"`
		PublicKey string `db:"public_key"`
	}
	// The refusal set is "every row matching one of these identities that the
	// cleanup below will NOT remove". Restricting it to live rows was not
	// enough: an unconfirmed row at a DIFFERENT public IP is invisible to both
	// the check and the cleanup, yet still collides with the INSERT — which
	// released the token, making one invite replayable for ever.
	//
	// Defining the two in terms of each other is what closes that: the only
	// rows allowed to survive this check are exactly the ones about to be
	// deleted, so nothing the caller supplies can reach a constraint.
	if err := h.rqliteClient.Query(ctx, &rows,
		`SELECT w.node_id, w.public_ip, w.public_key FROM wireguard_peers w
		  WHERE (w.public_ip = ? OR w.public_key = ? OR w.node_id = ?)
		    AND NOT (w.public_ip = ? AND NOT `+liveRowPredicate+`)`,
		req.PublicIP, req.WGPublicKey, req.PeerID, req.PublicIP); err != nil {
		return fmt.Errorf("%w: %v", errClaimCheckUnavailable, err)
	}
	if len(rows) == 0 {
		return nil
	}

	// Name the field that collided, not the row: the caller does not need the
	// rest of the fleet's registration details to fix its own request.
	for _, row := range rows {
		switch {
		case row.PublicIP == req.PublicIP:
			return fmt.Errorf("a node is already registered at this public IP; decommission it before rejoining")
		case row.PublicKey == req.WGPublicKey:
			return fmt.Errorf("this WireGuard public key is already registered to another node")
		case req.PeerID != "" && row.NodeID == req.PeerID:
			return fmt.Errorf("this peer id is already registered to another node")
		}
	}
	return fmt.Errorf("this identity is already registered to another node")
}

// removeUnfinishedJoinRows deletes rows this machine left behind by a previous
// join that did not complete.
//
// It carries the same liveness predicate as refuseIfClaimed, and that is the
// enforcement — the check reports a clear error, this predicate is what makes
// the delete safe. It used to remove every row with the given public IP, and
// the public IP is a string the caller chooses that nothing compares against
// the source address, so the cleanup doubled as a way to evict any node in the
// fleet by naming it. Re-evaluating liveness here also closes the window
// between the check and the delete.
func (h *Handler) removeUnfinishedJoinRows(ctx context.Context, publicIP string) error {
	if _, err := h.rqliteClient.Exec(ctx,
		`DELETE FROM wireguard_peers WHERE wg_ip IN (
		   SELECT w.wg_ip FROM wireguard_peers w
		    WHERE w.public_ip = ? AND NOT `+liveRowPredicate+`)`,
		publicIP); err != nil {
		return fmt.Errorf("delete unfinished join rows for %s: %w", publicIP, err)
	}
	return nil
}

// joinSecrets is everything a joining node needs that lives on disk.
type joinSecrets struct {
	ClusterSecret        string
	SwarmKey             string
	APIKeyHMACSecret     string
	RQLitePassword       string
	SecretsEncryptionKey string
	TURNSecret           string
}

// readJoinSecrets loads every secret the response carries.
//
// Read before anything is written, so a missing or unreadable file fails the
// join with the token still usable and no peer row left behind.
func (h *Handler) readJoinSecrets() (joinSecrets, error) {
	var out joinSecrets

	clusterSecret, err := os.ReadFile(h.oramaDir + "/secrets/cluster-secret")
	if err != nil {
		return out, fmt.Errorf("read cluster secret: %w", err)
	}
	out.ClusterSecret = strings.TrimSpace(string(clusterSecret))

	swarmKey, err := os.ReadFile(h.oramaDir + "/secrets/swarm.key")
	if err != nil {
		return out, fmt.Errorf("read swarm key: %w", err)
	}
	out.SwarmKey = strings.TrimSpace(string(swarmKey))

	// The rest are optional: a cluster installed before they existed has none,
	// and a joining node handles their absence.
	for _, opt := range []struct {
		file string
		dst  *string
	}{
		{"api-key-hmac-secret", &out.APIKeyHMACSecret},
		{"rqlite-password", &out.RQLitePassword},
		{"secrets-encryption-key", &out.SecretsEncryptionKey},
		{"turn-secret", &out.TURNSecret},
	} {
		if data, err := os.ReadFile(h.oramaDir + "/secrets/" + opt.file); err == nil {
			*opt.dst = strings.TrimSpace(string(data))
		}
	}

	return out, nil
}

// releaseToken un-consumes an invite token after a join that failed partway.
//
// The token is still single-use in the sense that matters — a successful join
// consumes it for good — but an attempt that got as far as burning it and then
// could not finish should not cost the operator a token and a support round
// trip. Releasing is safe because the row it guards was rolled back with it:
// there is no half-joined node for a replay to collide with.
func (h *Handler) releaseToken(ctx context.Context, token string) {
	// used_by_ip is deliberately kept: it is the record of who tried, and a
	// failed attempt is exactly when that record matters.
	if _, err := h.rqliteClient.Exec(ctx,
		"UPDATE invite_tokens SET used_at = NULL WHERE token = ?", token); err != nil {
		h.logger.Error("could not release the invite token after a failed join; it will need to be reissued",
			zap.Error(err))
		return
	}
	h.logger.Info("released the invite token after a failed join")
}

// consumeToken validates and marks an invite token as used (atomic single-use)
func (h *Handler) consumeToken(ctx context.Context, token, usedByIP string) error {
	// Atomically mark as used — only succeeds if token exists, is unused, and not expired
	result, err := h.rqliteClient.Exec(ctx,
		"UPDATE invite_tokens SET used_at = datetime('now'), used_by_ip = ? WHERE token = ? AND used_at IS NULL AND expires_at > datetime('now')",
		usedByIP, token)
	if err != nil {
		return fmt.Errorf("database error: %w", err)
	}

	rowsAffected, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("failed to check result: %w", err)
	}

	if rowsAffected == 0 {
		return fmt.Errorf("token invalid, expired, or already used")
	}

	return nil
}

// tokenOperatorWallet looks up the operator_wallet from a consumed invite token.
// Returns empty string if the token has no operator (legacy tokens).
func (h *Handler) tokenOperatorWallet(ctx context.Context, token string) string {
	var rows []struct {
		Wallet string `db:"operator_wallet"`
	}
	if err := h.rqliteClient.Query(ctx, &rows,
		"SELECT COALESCE(operator_wallet, '') AS operator_wallet FROM invite_tokens WHERE token = ?", token); err != nil {
		return ""
	}
	if len(rows) > 0 {
		return rows[0].Wallet
	}
	return ""
}

// addWGPeerLocally adds a peer to the local wg0 interface and persists to config
func (h *Handler) addWGPeerLocally(pubKey, publicIP, wgIP string) error {
	// Add to running interface with persistent-keepalive
	cmd := exec.Command("wg", "set", "wg0",
		"peer", pubKey,
		"endpoint", fmt.Sprintf("%s:51820", publicIP),
		"allowed-ips", fmt.Sprintf("%s/32", wgIP),
		"persistent-keepalive", "25")
	if output, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("wg set failed: %w\n%s", err, string(output))
	}

	// Persist to wg0.conf so peer survives wg-quick restart.
	// Read current config, append peer section, write back.
	confPath := "/etc/wireguard/wg0.conf"
	data, err := os.ReadFile(confPath)
	if err != nil {
		h.logger.Warn("could not read wg0.conf for persistence", zap.Error(err))
		return nil // non-fatal: runtime peer is added
	}

	// Check if peer already in config
	if strings.Contains(string(data), pubKey) {
		return nil // already persisted
	}

	peerSection := fmt.Sprintf("\n[Peer]\nPublicKey = %s\nEndpoint = %s:51820\nAllowedIPs = %s/32\nPersistentKeepalive = 25\n",
		pubKey, publicIP, wgIP)

	newConf := string(data) + peerSection
	writeCmd := exec.Command("tee", confPath)
	writeCmd.Stdin = strings.NewReader(newConf)
	if output, err := writeCmd.CombinedOutput(); err != nil {
		h.logger.Warn("could not persist peer to wg0.conf", zap.Error(err), zap.String("output", string(output)))
		return nil
	}
	if err := os.Chmod(confPath, 0o600); err != nil {
		h.logger.Warn("could not chmod wg0.conf 0600 after tee", zap.Error(err))
		return nil
	}
	fi, err := os.Stat(confPath)
	if err != nil {
		return nil
	}
	if fi.Mode().Perm() != 0o600 {
		h.logger.Warn("wg0.conf mode not 0600 after chmod", zap.String("mode", fi.Mode().Perm().String()))
	}

	return nil
}

// wgPeersContainsIP checks if any peer in the list has the given WG IP
func wgPeersContainsIP(peers []WGPeerInfo, wgIP string) bool {
	target := fmt.Sprintf("%s/32", wgIP)
	for _, p := range peers {
		if p.AllowedIP == target {
			return true
		}
	}
	return false
}

// getWGPeers returns all WG peers except the requesting node
func (h *Handler) getWGPeers(ctx context.Context, excludePubKey string) ([]WGPeerInfo, error) {
	type peerRow struct {
		WGIP      string `db:"wg_ip"`
		PublicKey string `db:"public_key"`
		PublicIP  string `db:"public_ip"`
		WGPort    int    `db:"wg_port"`
	}

	var rows []peerRow
	err := h.rqliteClient.Query(ctx, &rows,
		"SELECT wg_ip, public_key, public_ip, wg_port FROM wireguard_peers ORDER BY wg_ip")
	if err != nil {
		return nil, err
	}

	var peers []WGPeerInfo
	for _, row := range rows {
		if row.PublicKey == excludePubKey {
			continue // don't include the requesting node itself
		}
		port := row.WGPort
		if port == 0 {
			port = 51820
		}
		peers = append(peers, WGPeerInfo{
			PublicKey: row.PublicKey,
			Endpoint:  fmt.Sprintf("%s:%d", row.PublicIP, port),
			AllowedIP: fmt.Sprintf("%s/32", row.WGIP),
		})
	}

	return peers, nil
}

// getMyWGIP gets this node's WireGuard IP from the wg0 interface
// readLocalWGIP reads this node's own overlay address off wg0. A package-level
// var so a test can drive HandleJoin's ordering — which token is spent, which
// rows are written, in what order — without a WireGuard interface.
var readLocalWGIP = defaultLocalWGIP

func defaultLocalWGIP() (string, error) {
	out, err := exec.Command("ip", "-4", "addr", "show", "wg0").CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("failed to get wg0 info: %w", err)
	}

	// Parse "inet 10.0.0.1/32" from output
	for _, line := range strings.Split(string(out), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "inet ") {
			parts := strings.Fields(line)
			if len(parts) >= 2 {
				ip := strings.Split(parts[1], "/")[0]
				return ip, nil
			}
		}
	}

	return "", fmt.Errorf("could not find wg0 IP address")
}

// getMyWGPublicKey reads the local WireGuard public key from the orama secrets
// directory. The key is saved there during install by Phase6SetupWireGuard.
// This avoids needing root/CAP_NET_ADMIN permissions that `wg show wg0` requires.
// readLocalWGPublicKey and readLocalPublicIP are seams for the same reason.
var readLocalWGPublicKey = defaultLocalWGPublicKey

func defaultLocalWGPublicKey(oramaDir string) (string, error) {
	data, err := os.ReadFile(oramaDir + "/secrets/wg-public-key")
	if err != nil {
		return "", fmt.Errorf("failed to read WG public key from %s/secrets/wg-public-key: %w", oramaDir, err)
	}
	key := strings.TrimSpace(string(data))
	if key == "" {
		return "", fmt.Errorf("WG public key file is empty")
	}
	return key, nil
}

// getMyPublicIP determines this node's public IP by connecting to a public server
var readLocalPublicIP = defaultLocalPublicIP

func defaultLocalPublicIP() (string, error) {
	conn, err := net.DialTimeout("udp", "8.8.8.8:80", 3*time.Second)
	if err != nil {
		return "", fmt.Errorf("failed to determine public IP: %w", err)
	}
	defer conn.Close()
	addr := conn.LocalAddr().(*net.UDPAddr)
	return addr.IP.String(), nil
}

// queryIPFSPeerInfo gets the local IPFS node's peer ID and builds addrs with WG IP
func (h *Handler) queryIPFSPeerInfo(myWGIP string) PeerInfo {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Post("http://localhost:10107/api/v0/id", "", nil)
	if err != nil {
		h.logger.Warn("failed to query IPFS peer info", zap.Error(err))
		return PeerInfo{}
	}
	defer resp.Body.Close()

	var result struct {
		ID string `json:"ID"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		h.logger.Warn("failed to decode IPFS peer info", zap.Error(err))
		return PeerInfo{}
	}

	return PeerInfo{
		ID: result.ID,
		Addrs: []string{
			fmt.Sprintf("/ip4/%s/tcp/%d/p2p/%s", myWGIP, constants.IPFSSwarmPort, result.ID),
		},
	}
}

// queryIPFSClusterPeerInfo gets the local IPFS Cluster peer ID and builds addrs with WG IP
func (h *Handler) queryIPFSClusterPeerInfo(myWGIP string) PeerInfo {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get("http://localhost:10108/id")
	if err != nil {
		h.logger.Warn("failed to query IPFS Cluster peer info", zap.Error(err))
		return PeerInfo{}
	}
	defer resp.Body.Close()

	var result struct {
		ID string `json:"id"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		h.logger.Warn("failed to decode IPFS Cluster peer info", zap.Error(err))
		return PeerInfo{}
	}

	return PeerInfo{
		ID: result.ID,
		Addrs: []string{
			fmt.Sprintf("/ip4/%s/tcp/9100/p2p/%s", myWGIP, result.ID),
		},
	}
}

// buildBootstrapPeers constructs bootstrap peer multiaddrs using WG IPs
// Uses the node's LibP2P peer ID (port 4001), NOT the IPFS peer ID (IPFS swarm port)
func (h *Handler) buildBootstrapPeers(myWGIP, ipfsPeerID string) []string {
	// Read the node's LibP2P identity from disk
	keyPath := filepath.Join(h.oramaDir, "data", "identity.key")
	keyData, err := os.ReadFile(keyPath)
	if err != nil {
		h.logger.Warn("Failed to read node identity for bootstrap peers", zap.Error(err))
		return nil
	}

	priv, err := crypto.UnmarshalPrivateKey(keyData)
	if err != nil {
		h.logger.Warn("Failed to unmarshal node identity key", zap.Error(err))
		return nil
	}

	peerID, err := peer.IDFromPublicKey(priv.GetPublic())
	if err != nil {
		h.logger.Warn("Failed to derive peer ID from identity key", zap.Error(err))
		return nil
	}

	return []string{
		fmt.Sprintf("/ip4/%s/tcp/4001/p2p/%s", myWGIP, peerID.String()),
	}
}

// readIPFSClusterTrustedPeers reads IPFS Cluster trusted peer IDs from the secrets file
func (h *Handler) readIPFSClusterTrustedPeers() []string {
	data, err := os.ReadFile(h.oramaDir + "/secrets/ipfs-cluster-trusted-peers")
	if err != nil {
		return nil
	}
	var peers []string
	for _, line := range strings.Split(strings.TrimSpace(string(data)), "\n") {
		line = strings.TrimSpace(line)
		if line != "" {
			peers = append(peers, line)
		}
	}
	return peers
}

// readBaseDomain reads the base domain from node config
func (h *Handler) readBaseDomain() string {
	data, err := os.ReadFile(h.oramaDir + "/configs/node.yaml")
	if err != nil {
		return ""
	}

	// Simple parse — look for base_domain field
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if strings.HasPrefix(line, "base_domain:") {
			val := strings.TrimPrefix(line, "base_domain:")
			val = strings.TrimSpace(val)
			val = strings.Trim(val, `"'`)
			return val
		}
	}

	return ""
}
