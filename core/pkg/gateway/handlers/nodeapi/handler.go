// Package nodeapi serves the endpoints a node uses to record facts about
// itself in the core cluster.
//
// A node used to write those rows directly, with the rqlite handle it holds:
// `INSERT INTO dns_nodes ...`, `UPDATE dns_nodes SET last_seen ...`. The row is
// a promise — every consumer routes real traffic on `status = 'active' AND
// last_seen > ?` — and nothing checked who made it, because there was no
// request to check. These endpoints are that request.
//
// What they change today is where the claim is checked, not who may make it:
// the stamp is keyed by the cluster secret, which every node holds. What they
// make possible is the rest — the node id acted on comes from the stamp rather
// than the body, and the key is resolved per node, so a per-node credential is
// a change of resolver.
package nodeapi

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/auth"
	"github.com/DeBrosOfficial/network/pkg/nodeapi"
	"github.com/DeBrosOfficial/network/pkg/rqlite"
	"github.com/libp2p/go-libp2p/core/peer"
	"go.uber.org/zap"
)

// maxBodyBytes bounds what is read before the stamp is checked. The MAC covers
// the body, so the body has to be read first; this is what stops a caller from
// making the gateway buffer whatever it likes.
const maxBodyBytes = 64 << 10

// maxFieldLength bounds every free-text field a node sends. These land in
// columns read by DNS answers and by the operator CLI; a field this long is a
// caller that is not a node.
const maxFieldLength = 256

// sshUserPattern is what a POSIX login name looks like.
//
// `ssh_user` is not free text, however much it looks like it. The column is
// read back by the operator CLI and concatenated into `<user>@<host>`, which is
// handed to ssh(1) as one argv entry — so a value beginning with `-` is parsed
// as an option instead of a destination, and `-oProxyCommand=…` runs a command
// of the caller's choosing on the *operator's* machine. That machine holds the
// RootWallet and the fleet's SSH keys, which makes it a worse place to land
// than anything inside the cluster.
var sshUserPattern = regexp.MustCompile(`^[a-z_][a-z0-9_-]{0,31}$`)

// Handler serves the node self-registration endpoints.
type Handler struct {
	logger *zap.Logger
	db     rqlite.Client
	keyFor auth.NodeAPIKeyFor
	now    func() time.Time
}

// NewHandler builds the handler.
//
// keyFor is nil on a gateway with no cluster secret. That gateway refuses these
// calls rather than serving them unauthenticated, which is the failure the
// WireGuard peer endpoint shipped with for a year: it read
// `if secret != "" && mismatch`, so a gateway configured without one let
// everything through.
func NewHandler(logger *zap.Logger, db rqlite.Client, keyFor auth.NodeAPIKeyFor) *Handler {
	return &Handler{logger: logger, db: db, keyFor: keyFor, now: time.Now}
}

// HandleRegister serves POST /v1/internal/node/register.
func (h *Handler) HandleRegister(w http.ResponseWriter, r *http.Request) {
	nodeID, body, ok := h.authenticate(w, r)
	if !ok {
		return
	}

	var req nodeapi.RegisterRequest
	if err := json.Unmarshal(body, &req); err != nil {
		h.refuse(w, nodeID, "the registration body could not be read", err)
		return
	}
	if err := validate(req); err != nil {
		h.refuse(w, nodeID, err.Error(), err)
		return
	}
	if err := h.overlayAddressAgrees(r.Context(), nodeID, req.InternalIP); err != nil {
		h.refuse(w, nodeID, err.Error(), err)
		return
	}

	const query = `
		INSERT INTO dns_nodes (id, ip_address, internal_ip, region, status, ssh_user, environment, operator_wallet, last_seen, created_at, updated_at)
		VALUES (?, ?, ?, ?, 'active', ?, ?, ?, datetime('now'), datetime('now'), datetime('now'))
		ON CONFLICT(id) DO UPDATE SET
			ip_address = excluded.ip_address,
			internal_ip = excluded.internal_ip,
			region = excluded.region,
			status = 'active',
			ssh_user = COALESCE(NULLIF(excluded.ssh_user, ''), dns_nodes.ssh_user),
			environment = COALESCE(NULLIF(excluded.environment, ''), dns_nodes.environment),
			operator_wallet = COALESCE(NULLIF(excluded.operator_wallet, ''), dns_nodes.operator_wallet),
			last_seen = datetime('now'),
			updated_at = datetime('now')`

	if _, err := h.db.Exec(r.Context(), query,
		nodeID, req.IPAddress, req.InternalIP, req.Region, req.SSHUser, req.Environment, req.OperatorWallet,
	); err != nil {
		h.logger.Error("node registration failed", zap.String("node_id", nodeID), zap.Error(err))
		http.Error(w, "failed to record this node", http.StatusInternalServerError)
		return
	}

	h.logger.Info("node registered",
		zap.String("node_id", nodeID),
		zap.String("ip_address", req.IPAddress),
		zap.String("region", req.Region))
	w.WriteHeader(http.StatusNoContent)
}

// HandleHeartbeat serves POST /v1/internal/node/heartbeat.
//
// It re-asserts `active` as well as refreshing last_seen: a live, heartbeating
// node must count as active, and this is what heals a node that was reaped to
// `inactive` during a restart window.
func (h *Handler) HandleHeartbeat(w http.ResponseWriter, r *http.Request) {
	nodeID, _, ok := h.authenticate(w, r)
	if !ok {
		return
	}

	const query = `UPDATE dns_nodes SET status = 'active', last_seen = datetime('now'), updated_at = datetime('now') WHERE id = ?`
	res, err := h.db.Exec(r.Context(), query, nodeID)
	if err != nil {
		h.logger.Error("node heartbeat failed", zap.String("node_id", nodeID), zap.Error(err))
		http.Error(w, "failed to record this heartbeat", http.StatusInternalServerError)
		return
	}

	// A driver that cannot report the count is not evidence the row exists, and
	// answering `registered: true` on no evidence would leave a node that never
	// registered heartbeating into nothing forever.
	registered := false
	if res != nil {
		if affected, aerr := res.RowsAffected(); aerr == nil && affected > 0 {
			registered = true
		}
	}

	h.writeJSON(w, nodeID, nodeapi.HeartbeatResponse{Registered: registered})
}

// authenticate decides whether to act on a request, and on whose behalf.
func (h *Handler) authenticate(w http.ResponseWriter, r *http.Request) (string, []byte, bool) {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return "", nil, false
	}

	// Caddy reverse-proxies every path on this node's domains to the gateway,
	// so without this these endpoints are reachable from the internet and the
	// stamp is the only thing between a leaked cluster secret and a stranger
	// rewriting the table the cluster routes on. The sibling `/v1/internal/wg/*`
	// endpoints carry an overlay check for exactly this reason; this one cannot
	// use it verbatim, because the caller is a process on this host rather than
	// a peer across the mesh.
	//
	// 404 rather than 403: an endpoint the public has no business reaching
	// should not confirm that it exists.
	if !auth.IsNodeLocal(r) {
		http.Error(w, "not found", http.StatusNotFound)
		return "", nil, false
	}

	if h.keyFor == nil {
		h.logger.Warn("refusing a node self-registration: this gateway has no cluster secret " +
			"configured, so the caller cannot be authenticated")
		http.Error(w, "node registration unavailable: no cluster secret configured", http.StatusServiceUnavailable)
		return "", nil, false
	}

	// The body has to be read before the stamp is checked, because the MAC
	// covers it. That is why it is bounded here rather than in each handler.
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxBodyBytes))
	if err != nil {
		h.logger.Warn("a node request body could not be read", zap.Error(err))
		http.Error(w, "invalid request body", http.StatusBadRequest)
		return "", nil, false
	}

	nodeID, ok := auth.VerifyNodeAPI(h.keyFor, r, body, h.now())
	if !ok {
		// One answer for every reason it failed. Telling a caller whether a
		// node id is known, or whether the id was right and only the stamp was
		// wrong, is an oracle for both.
		h.logger.Warn("a node request arrived without a valid stamp", zap.String("path", r.URL.Path))
		http.Error(w, "unauthorized", http.StatusUnauthorized)
		return "", nil, false
	}

	// The id is what the row is keyed on and what every consumer reads back as
	// a peer id, so a malformed one is refused rather than stored.
	if _, err := peer.Decode(nodeID); err != nil {
		h.logger.Warn("a node stamped a request with something that is not a peer id",
			zap.String("node_id", nodeID), zap.Error(err))
		http.Error(w, "node id must be a valid libp2p peer id", http.StatusBadRequest)
		return "", nil, false
	}
	return nodeID, body, true
}

// overlayAddressAgrees refuses an internal_ip that contradicts the address the
// cluster allocated to this node.
//
// The overlay address of a node is what every other node dials it on — raft
// joins, namespace cluster membership, storage eviction all resolve through
// `dns_nodes.internal_ip`. It is allocated by the cluster and recorded in
// `wireguard_peers`, so a node asserting a different one is asserting something
// it does not get to decide, and the effect would be to redirect inter-node
// traffic at an address of its choosing.
//
// A node with no peer row yet is registering before its mesh row landed. There
// is nothing to check it against, and saying so is not the same as accepting a
// contradiction.
func (h *Handler) overlayAddressAgrees(ctx context.Context, nodeID, claimed string) error {
	var rows []struct {
		WGIP string `db:"wg_ip"`
	}
	if err := h.db.Query(ctx, &rows,
		`SELECT wg_ip FROM wireguard_peers WHERE node_id = ?`, nodeID); err != nil {
		return fmt.Errorf("this node's overlay address could not be read: %w", err)
	}
	if len(rows) == 0 || strings.TrimSpace(rows[0].WGIP) == "" {
		return nil
	}
	if allocated := strings.TrimSpace(rows[0].WGIP); allocated != claimed {
		return fmt.Errorf("internal_ip is %s, but this node was allocated %s on the overlay", claimed, allocated)
	}
	return nil
}

// validate refuses a claim that could not have come from a node.
func validate(r nodeapi.RegisterRequest) error {
	// A routable address, not just something that parses. The node used to
	// invent 127.0.0.1 when it could not work out its own IP, and a row saying
	// `active` at that address is handed to every consumer that routes traffic.
	public := net.ParseIP(r.IPAddress)
	if public == nil {
		return fmt.Errorf("ip_address must be an IP address")
	}
	if public.IsLoopback() || public.IsUnspecified() || public.IsMulticast() {
		return fmt.Errorf("ip_address must be an address other nodes can reach, not %s", r.IPAddress)
	}
	internal := net.ParseIP(r.InternalIP)
	if internal == nil {
		return fmt.Errorf("internal_ip must be an IP address")
	}
	if internal.IsLoopback() || internal.IsUnspecified() || internal.IsMulticast() {
		return fmt.Errorf("internal_ip must be an address other nodes can reach, not %s", r.InternalIP)
	}
	if strings.TrimSpace(r.Region) == "" {
		return fmt.Errorf("region is required")
	}
	if r.SSHUser != "" && !sshUserPattern.MatchString(r.SSHUser) {
		return fmt.Errorf("ssh_user must be a POSIX login name")
	}
	for name, value := range map[string]string{
		"region":          r.Region,
		"environment":     r.Environment,
		"operator_wallet": r.OperatorWallet,
	} {
		if len(value) > maxFieldLength {
			return fmt.Errorf("%s is longer than %d characters", name, maxFieldLength)
		}
		// These are rendered into operator output and into DNS answers; a
		// newline in one is a caller trying to write a second line somewhere.
		if strings.ContainsAny(value, "\n\r\x00") {
			return fmt.Errorf("%s contains invalid characters", name)
		}
	}
	return nil
}

// refuse answers a bad request and says on the gateway which node sent it, so a
// node stuck in a loop leaves a trace on both sides rather than only its own.
func (h *Handler) refuse(w http.ResponseWriter, nodeID, message string, err error) {
	h.logger.Warn("refused a node's claim about itself",
		zap.String("node_id", nodeID),
		zap.String("reason", message),
		zap.Error(err))
	http.Error(w, message, http.StatusBadRequest)
}

func (h *Handler) writeJSON(w http.ResponseWriter, nodeID string, v any) {
	w.Header().Set("Content-Type", "application/json")
	if err := json.NewEncoder(w).Encode(v); err != nil {
		// The status is already sent, so there is nothing to tell the caller.
		// The node reads a truncated answer as "not registered" and registers,
		// which is the safe reading — but it should be visible here.
		h.logger.Warn("the answer to a node could not be written",
			zap.String("node_id", nodeID), zap.Error(err))
	}
}
