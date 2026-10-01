package gateway

import (
	"bytes"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/DeBrosOfficial/network/pkg/constants"
	pushntfy "github.com/DeBrosOfficial/network/pkg/push/providers/ntfy"
)

const (
	// ntfyRelayTimeout bounds the hop to this node's loopback ntfy.
	ntfyRelayTimeout = 5 * time.Second

	// maxNtfyRelayBody caps a relayed publish. ntfy itself refuses messages over
	// 4 KiB by default; this only keeps a malformed peer from making the
	// gateway buffer more.
	maxNtfyRelayBody = 64 << 10

	// maxNtfyRelayErrBody caps how much of ntfy's refusal is echoed back to the
	// publishing node.
	maxNtfyRelayErrBody = 512
)

// ntfyTopicPattern is ntfy's own topic rule: one path segment of letters,
// digits, '_' and '-', at most 64 characters. Anything else would address a
// different ntfy route than the topic route.
var ntfyTopicPattern = regexp.MustCompile(`^[-_A-Za-z0-9]{1,64}$`)

// ntfyRelayHeaders are the publish headers relayed to the local ntfy: the ones
// the ntfy provider sets (pkg/push/providers/ntfy). Nothing else is forwarded,
// so a peer cannot reach ntfy features (Actions, Attach, Email, ...) the
// provider does not use.
var ntfyRelayHeaders = []string{"Title", "Priority", "Tags"}

// localNtfyURL is the base URL of this node's own ntfy, which listens on
// loopback only (Caddy is the sole public path to it).
func (g *Gateway) localNtfyURL() string {
	if g.ntfyLocalURL != "" {
		return g.ntfyLocalURL
	}
	return fmt.Sprintf("http://127.0.0.1:%d", constants.NtfyListenPort)
}

// handleInternalNtfyPublish serves POST /v1/internal/push/ntfy/<topic>, the
// per-node side of the push fan-out (bugboard #858). Each node's ntfy is
// independent, so a namespace gateway publishes to every node's internal
// gateway over the WireGuard overlay; this relays the publish to this node's
// loopback ntfy.
//
// The body is the notification, so only the v2 coordination stamp (which covers
// the body and names this node as the audience) is accepted, over a WireGuard
// source.
func (g *Gateway) handleInternalNtfyPublish(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		writeError(w, http.StatusMethodNotAllowed, "method not allowed")
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, maxNtfyRelayBody)
	if !g.verifyCoordinationV2(r) {
		unauthorized(w, CodeAuthMissing, "this route is reached from inside the cluster and the caller did not present what it requires", nil)
		return
	}
	topic := strings.TrimPrefix(r.URL.Path, pushntfy.FanoutPathPrefix)
	if !ntfyTopicPattern.MatchString(topic) {
		writeError(w, http.StatusBadRequest, "topic must be one path segment of letters, digits, '_' or '-', at most 64 characters")
		return
	}
	body, err := io.ReadAll(r.Body)
	if err != nil {
		writeError(w, http.StatusBadRequest, "read publish body: "+err.Error())
		return
	}

	req, err := http.NewRequestWithContext(r.Context(), http.MethodPost, g.localNtfyURL()+"/"+topic, bytes.NewReader(body))
	if err != nil {
		writeError(w, http.StatusInternalServerError, "build local ntfy request: "+err.Error())
		return
	}
	for _, h := range ntfyRelayHeaders {
		if v := r.Header.Get(h); v != "" {
			req.Header.Set(h, v)
		}
	}

	resp, err := (&http.Client{Timeout: ntfyRelayTimeout}).Do(req)
	if err != nil {
		writeError(w, http.StatusBadGateway, fmt.Sprintf("this node's ntfy did not answer on port %d, check that orama-namespace-ntfy@index is running: %v",
			constants.NtfyListenPort, err))
		return
	}
	defer resp.Body.Close()
	if resp.StatusCode >= http.StatusBadRequest {
		refusal, _ := io.ReadAll(io.LimitReader(resp.Body, maxNtfyRelayErrBody))
		writeError(w, http.StatusBadGateway, fmt.Sprintf("this node's ntfy refused the publish: http %d: %s",
			resp.StatusCode, strings.TrimSpace(string(refusal))))
		return
	}
	w.WriteHeader(http.StatusOK)
}
