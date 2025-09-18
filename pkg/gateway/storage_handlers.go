package gateway

import (
	"encoding/json"
	"net/http"

	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/pubsub"
)

// Database HTTP handlers
func (g *Gateway) dbQueryHandler(w http.ResponseWriter, r *http.Request) {
	if g.client == nil {
		writeError(w, http.StatusServiceUnavailable, "client not initialized")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		SQL  string `json:"sql"`
		Args []any  `json:"args"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.SQL == "" {
		writeError(w, http.StatusBadRequest, "invalid body: {sql, args?}")
		return
	}
	ctx := client.WithInternalAuth(r.Context())
	res, err := g.client.Database().Query(ctx, body.SQL, body.Args...)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, res)
}

func (g *Gateway) dbTransactionHandler(w http.ResponseWriter, r *http.Request) {
	if g.client == nil {
		writeError(w, http.StatusServiceUnavailable, "client not initialized")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		Statements []string `json:"statements"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || len(body.Statements) == 0 {
		writeError(w, http.StatusBadRequest, "invalid body: {statements:[...]}")
		return
	}
	ctx := client.WithInternalAuth(r.Context())
	if err := g.client.Database().Transaction(ctx, body.Statements); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (g *Gateway) dbSchemaHandler(w http.ResponseWriter, r *http.Request) {
	if g.client == nil {
		writeError(w, http.StatusServiceUnavailable, "client not initialized")
		return
	}
	if r.Method != http.MethodGet {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	ctx := client.WithInternalAuth(r.Context())
	schema, err := g.client.Database().GetSchema(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, schema)
}

func (g *Gateway) dbCreateTableHandler(w http.ResponseWriter, r *http.Request) {
	if g.client == nil {
		writeError(w, http.StatusServiceUnavailable, "client not initialized")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		Schema string `json:"schema"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Schema == "" {
		writeError(w, http.StatusBadRequest, "invalid body: {schema}")
		return
	}
	ctx := client.WithInternalAuth(r.Context())
	if err := g.client.Database().CreateTable(ctx, body.Schema); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusCreated, map[string]any{"status": "ok"})
}

func (g *Gateway) dbDropTableHandler(w http.ResponseWriter, r *http.Request) {
	if g.client == nil {
		writeError(w, http.StatusServiceUnavailable, "client not initialized")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		Table string `json:"table"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Table == "" {
		writeError(w, http.StatusBadRequest, "invalid body: {table}")
		return
	}
	ctx := client.WithInternalAuth(r.Context())
	if err := g.client.Database().DropTable(ctx, body.Table); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (g *Gateway) networkStatusHandler(w http.ResponseWriter, r *http.Request) {
	if g.client == nil {
		writeError(w, http.StatusServiceUnavailable, "client not initialized")
		return
	}
	ctx := r.Context()
	status, err := g.client.Network().GetStatus(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, status)
}

func (g *Gateway) networkPeersHandler(w http.ResponseWriter, r *http.Request) {
	if g.client == nil {
		writeError(w, http.StatusServiceUnavailable, "client not initialized")
		return
	}
	ctx := r.Context()
	peers, err := g.client.Network().GetPeers(ctx)
	if err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, peers)
}

func (g *Gateway) networkConnectHandler(w http.ResponseWriter, r *http.Request) {
	if g.client == nil {
		writeError(w, http.StatusServiceUnavailable, "client not initialized")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		Multiaddr string `json:"multiaddr"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.Multiaddr == "" {
		writeError(w, http.StatusBadRequest, "invalid body: expected {multiaddr}")
		return
	}
	if err := g.client.Network().ConnectToPeer(r.Context(), body.Multiaddr); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (g *Gateway) networkDisconnectHandler(w http.ResponseWriter, r *http.Request) {
	if g.client == nil {
		writeError(w, http.StatusServiceUnavailable, "client not initialized")
		return
	}
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	var body struct {
		PeerID string `json:"peer_id"`
	}
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil || body.PeerID == "" {
		writeError(w, http.StatusBadRequest, "invalid body: expected {peer_id}")
		return
	}
	if err := g.client.Network().DisconnectFromPeer(r.Context(), body.PeerID); err != nil {
		writeError(w, http.StatusInternalServerError, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{"status": "ok"})
}

func (g *Gateway) validateNamespaceParam(r *http.Request) bool {
	qns := r.URL.Query().Get("namespace")
	if qns == "" {
		return true
	}
	if v := r.Context().Value(pubsub.CtxKeyNamespaceOverride); v != nil {
		if s, ok := v.(string); ok && s != "" {
			return s == qns
		}
	}
	// If no namespace in context, disallow explicit namespace param
	return false
}
