package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	nodeauth "github.com/DeBrosOfficial/network/pkg/auth"
	gwauth "github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/secrets"
)

type rotateSecretsRequest struct {
	Rotate bool `json:"rotate"`
}

type rotateSecretsResponse struct {
	KeyID      string             `json:"key_id"`
	PreviousID string             `json:"previous_id,omitempty"`
	Rotated    bool               `json:"rotated"`
	Index      secrets.WalkResult `json:"index"`
	Namespaces []nsWalkResult     `json:"namespaces,omitempty"`
}

type nsWalkResult struct {
	Namespace string             `json:"namespace"`
	Target    string             `json:"target"`
	Result    secrets.WalkResult `json:"result"`
	Error     string             `json:"error,omitempty"`
}

type internalReencryptRequest struct {
	Root secrets.Root `json:"root"`
}

// handleRotateSecrets serves POST /v1/operator/rotate-secrets.
//
// Without {"rotate":true} it rewrites leftover plaintext and the legacy enc:
// envelope onto enc:v1:<id>: under the current IKM. With rotate:true it
// generates a new IKM first, so a captured cluster-secret / old encryption-root
// cannot open the new rows.
//
// Run this only after every gateway is on a binary that can read enc:v1:.
func (g *Gateway) handleRotateSecrets(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if g.operatorHandler == nil {
		writeError(w, http.StatusServiceUnavailable, "operator endpoints are not enabled on this gateway")
		return
	}
	if _, ok := g.operatorHandler.Authorize(w, r); !ok {
		return
	}
	if g.cfg == nil || g.cfg.StateDir == "" {
		writeError(w, http.StatusServiceUnavailable, "this gateway has no state directory to persist the encryption root in")
		return
	}

	var req rotateSecretsRequest
	if r.Body != nil {
		_ = json.NewDecoder(io.LimitReader(r.Body, 1<<12)).Decode(&req)
	}

	ctx := r.Context()
	registry := g.registryStore()
	dir := g.cfg.StateDir

	if g.encHolder == nil {
		g.encHolder = secrets.NewHolder(secrets.Root{})
	}
	root := g.encHolder.Get()
	if root.CurrentIKM == "" {
		seed := ""
		if g.cfg.DataDir != "" {
			seed = secrets.SecretsDir(g.cfg.DataDir)
		}
		loaded, err := secrets.LoadOrMaterialize(ctx, registry, dir, seed, g.cfg.ClusterSecret)
		if err != nil {
			writeError(w, http.StatusInternalServerError, "encryption root: "+err.Error())
			return
		}
		root = loaded
		g.encHolder.Swap(root)
	}

	var err error
	if req.Rotate {
		root, err = secrets.Rotate(ctx, registry, dir, root)
	} else {
		root, err = secrets.EnableVersionedWrites(ctx, registry, dir, root)
	}
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	if g.encHolder == nil {
		g.encHolder = secrets.NewHolder(root)
	} else {
		g.encHolder.Swap(root)
	}

	cols := secrets.NamespaceColumns()
	if !isNamespaceGateway(g.cfg) {
		cols = append(secrets.IndexColumns(), secrets.NamespaceColumns()...)
	}
	localRes, err := secrets.Walk(ctx, g.ormClient, root, cols)
	if err != nil {
		writeError(w, http.StatusInternalServerError, "walk: "+err.Error())
		return
	}
	if isNamespaceGateway(g.cfg) {
		respondRotate(w, r, g, root, req.Rotate, localRes, []nsWalkResult{{
			Namespace: g.cfg.ClientNamespace,
			Target:    "local",
			Result:    localRes,
		}})
		return
	}

	nsResults := g.fanoutReencrypt(ctx, root)
	respondRotate(w, r, g, root, req.Rotate, localRes, nsResults)
}

func respondRotate(w http.ResponseWriter, r *http.Request, g *Gateway, root secrets.Root, rotated bool, index secrets.WalkResult, ns []nsWalkResult) {
	if g.authService != nil {
		g.authService.Audit().RecordFromRequest(r.Context(), r, gwauth.AuditEvent{
			Actor:    gwauth.ActorFromRequest(r),
			Action:   gwauth.AuditOperatorAction,
			Resource: "secrets.rotate",
			Result:   gwauth.AuditSuccess,
			Metadata: map[string]string{"key_id": root.CurrentID, "rotated": fmt.Sprintf("%v", rotated)},
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(rotateSecretsResponse{
		KeyID:      root.CurrentID,
		PreviousID: root.PreviousID,
		Rotated:    rotated,
		Index:      index,
		Namespaces: ns,
	})
}

func (g *Gateway) handleInternalReencrypt(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	if !g.verifyCoordination(r) {
		unauthorized(w, CodeAuthMissing, "this route is reached from inside the cluster and the caller did not present what it requires", nil)
		return
	}
	var req internalReencryptRequest
	if err := json.NewDecoder(io.LimitReader(r.Body, 1<<16)).Decode(&req); err != nil || req.Root.CurrentIKM == "" {
		writeError(w, http.StatusBadRequest, "root is required")
		return
	}
	root := req.Root
	// Cached before it is used. A gateway that swapped to a root it could not
	// persist would boot next time — registry unreachable — on the previous
	// one, and fail on everything encrypted in between. The failure goes back
	// to the index's fan-out, which reports it per namespace.
	if g.cfg == nil || g.cfg.StateDir == "" {
		writeError(w, http.StatusServiceUnavailable, "this gateway has no state directory to persist the encryption root in")
		return
	}
	if err := secrets.Persist(g.cfg.StateDir, root); err != nil {
		writeError(w, http.StatusInternalServerError, "persist the rotated encryption root: "+err.Error())
		return
	}
	if g.encHolder == nil {
		g.encHolder = secrets.NewHolder(root)
	} else {
		g.encHolder.Swap(root)
	}

	cols := secrets.NamespaceColumns()
	if !isNamespaceGateway(g.cfg) {
		cols = secrets.IndexColumns()
	}
	res, err := secrets.Walk(r.Context(), g.ormClient, req.Root, cols)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(res)
}

func (g *Gateway) fanoutReencrypt(ctx context.Context, root secrets.Root) []nsWalkResult {
	type target struct {
		Namespace  string `db:"namespace_name"`
		InternalIP string `db:"internal_ip"`
		Port       int    `db:"gateway_http_port"`
	}
	var rows []target
	q := `
		SELECT DISTINCT nc.namespace_name,
		       COALESCE(dn.internal_ip, dn.ip_address) AS internal_ip,
		       pa.gateway_http_port
		  FROM namespace_clusters nc
		  JOIN namespace_port_allocations pa ON pa.namespace_cluster_id = nc.id
		  JOIN dns_nodes dn ON dn.id = pa.node_id
		 WHERE pa.gateway_http_port IS NOT NULL AND pa.gateway_http_port > 0`
	if err := g.ormClient.Query(ctx, &rows, q); err != nil {
		return []nsWalkResult{{Error: err.Error()}}
	}

	body, err := json.Marshal(internalReencryptRequest{Root: root})
	if err != nil {
		return []nsWalkResult{{Error: err.Error()}}
	}
	key, err := nodeauth.CoordinationKey(g.cfg.ClusterSecret)
	if err != nil {
		return []nsWalkResult{{Error: err.Error()}}
	}

	out := make([]nsWalkResult, 0, len(rows))
	seen := map[string]bool{}
	client := &http.Client{Timeout: 60 * time.Second}
	for _, row := range rows {
		if row.InternalIP == "" || row.Port == 0 {
			continue
		}
		url := fmt.Sprintf("http://%s:%d/v1/internal/secrets/reencrypt", row.InternalIP, row.Port)
		if seen[url] {
			continue
		}
		seen[url] = true
		nr := nsWalkResult{Namespace: row.Namespace, Target: url}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, url, bytes.NewReader(body))
		if err != nil {
			nr.Error = err.Error()
			out = append(out, nr)
			continue
		}
		req.Header.Set("Content-Type", "application/json")
		if err := nodeauth.SignCoordination(key, req, time.Now()); err != nil {
			nr.Error = err.Error()
			out = append(out, nr)
			continue
		}
		resp, err := client.Do(req)
		if err != nil {
			nr.Error = err.Error()
			out = append(out, nr)
			continue
		}
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			nr.Error = strings.TrimSpace(string(raw))
			if nr.Error == "" {
				nr.Error = resp.Status
			}
			out = append(out, nr)
			continue
		}
		_ = json.Unmarshal(raw, &nr.Result)
		out = append(out, nr)
	}
	return out
}

func (g *Gateway) registryStore() secrets.Store {
	if g == nil {
		return nil
	}
	// Namespace gateways must persist the root on the cluster registry.
	if isNamespaceGateway(g.cfg) && g.globalORM() != nil {
		return g.globalORM()
	}
	return g.ormClient
}

func (g *Gateway) globalORM() secrets.Store {
	return g.registry
}
