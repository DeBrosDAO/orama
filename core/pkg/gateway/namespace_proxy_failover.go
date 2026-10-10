package gateway

import (
	"errors"
	"io"
	"net"
	"net/http"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/httputil"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"go.uber.org/zap"
)

// undialedBody is a proxied request's body that can be offered to another
// namespace gateway when the first could not be dialed. The transport reads a
// body only once its connection is up, so a body nothing has read from yet was
// never sent anywhere; Close is left to the server, which owns the inbound
// body, so a failed attempt's transport cannot close it under the next one.
type undialedBody struct {
	io.ReadCloser
	read bool
	// readErr is the error reading the body gave, other than its end: the
	// client's connection dropped, or the body was over its limit.
	readErr error
}

func (b *undialedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 {
		b.read = true
	}
	if err != nil && err != io.EOF {
		b.readErr = err
	}
	return n, err
}

// failedRead reports whether err, from sending the request, is the body's own
// read error: the client failed to deliver the request, which says nothing
// about the member it was being sent to.
func (b *undialedBody) failedRead(err error) bool {
	return b.readErr != nil && errors.Is(err, b.readErr)
}

func (b *undialedBody) Close() error { return nil }

// isDialFailure reports whether err is a failure to open the connection: the
// request never reached the upstream, so sending it to another one is safe for
// any method.
func isDialFailure(err error) bool {
	var opErr *net.OpError
	return errors.As(err, &opErr) && opErr.Op == "dial"
}

// errNoClusterSecret is returned by namespaceProxyRequest when the hop cannot
// be signed.
var errNoClusterSecret = errors.New("this gateway has no cluster secret, so it cannot authenticate itself to the namespace gateway")

// isUpstreamFailure reports whether resp proves the namespace gateway that sent
// it unhealthy, which is what a circuit breaker counts: a 502, 503 or 504 the
// gateway itself answered. The same statuses coming from a function, chosen by
// the tenant's code or reporting that one function could not be loaded
// (httputil.HeaderTenantOrigin), say nothing about the gateway: counting them
// let one function that returned 503 take its whole namespace out of rotation.
func isUpstreamFailure(resp *http.Response) bool {
	return IsResponseFailure(resp.StatusCode) && !hasTenantOriginMarker(resp.Header)
}

// hasTenantOriginMarker reports whether h carries the tenant-origin marker at
// all. A present marker counts even with an empty value, so nothing can turn a
// tenant's answer into the gateway's by blanking it.
func hasTenantOriginMarker(h http.Header) bool {
	return len(h.Values(httputil.HeaderTenantOrigin)) > 0
}

// retainBreakers drops the breakers of namespace's gateways on nodes the
// registry no longer lists as its members, so the registry holds one breaker
// per gateway that exists and a removed namespace or a moved member leaves none
// behind. targets is the registry's answer, empty for a namespace that is gone.
func (g *Gateway) retainBreakers(namespace string, targets []namespaceGatewayTarget) {
	if g.circuitBreakers == nil {
		return
	}
	ips := make([]string, len(targets))
	for i, t := range targets {
		ips[i] = t.ip
	}
	if dropped := g.circuitBreakers.RetainNamespaceMembers(namespace, ips); dropped > 0 {
		g.logger.ComponentInfo(logging.ComponentGeneral, "dropped the circuit breakers of namespace gateways that are no longer members",
			zap.String("namespace", namespace), zap.Int("dropped", dropped))
	}
}

// copyProxiedHeaders copies a proxied response's headers to w, leaving out the
// tenant-origin marker, which is for the gateway that forwarded the request and
// not for its client.
//
// Each header the namespace gateway sent replaces the one this gateway's own
// middleware already set: the response is the namespace gateway's. Added to it
// instead, the CORS headers both gateways set came out twice, and a browser
// refuses a response whose Access-Control-Allow-Origin holds two values, so
// every page calling a namespace's functions failed (the stagenet demo, 2026-10-10).
// A header with several values upstream, such as Set-Cookie, keeps all of them.
func copyProxiedHeaders(w http.ResponseWriter, resp *http.Response) {
	for key, values := range resp.Header {
		if strings.EqualFold(key, httputil.HeaderTenantOrigin) {
			continue
		}
		w.Header()[key] = append([]string(nil), values...)
	}
}

// hopBody is the body r is forwarded with to another node, and what tells a
// failure to read it (the client dropped) from one of the node. A request with
// no body is forwarded as it is.
func hopBody(r *http.Request) (io.ReadCloser, *undialedBody) {
	if r.Body == nil || r.Body == http.NoBody {
		return r.Body, nil
	}
	b := &undialedBody{ReadCloser: r.Body}
	return b, b
}

// recordHopError records on cb that the hop to a node did not complete. A
// client that left, or a body the client failed to deliver, says nothing about
// the node and gives back a half-open probe slot instead.
func recordHopError(cb *CircuitBreaker, r *http.Request, body *undialedBody, err error) {
	if r.Context().Err() != nil || (body != nil && body.failedRead(err)) {
		cb.Abandon()
		return
	}
	cb.RecordFailure(httputil.FailureReason(err))
}
