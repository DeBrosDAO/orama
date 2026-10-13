package serverless

import "net/http"

const (
	// wsPubsubDeliveryParam is the query parameter a client adds to a
	// function's WebSocket URL to receive bridged pub/sub messages as
	// platform-stamped frames (wsbridge.Delivery) instead of raw publisher
	// bytes. Opt-in, so a client written before it keeps working unchanged.
	wsPubsubDeliveryParam = "pubsub_delivery"
	// wsPubsubDeliveryStamped is the value of wsPubsubDeliveryParam that opts in.
	wsPubsubDeliveryStamped = "stamped"
)

// registerBridgeClient records, at upgrade, which namespace owns a WS client
// and whether it asked for stamped delivery. The caller removes the client with
// wsBridge.RemoveClient when the connection ends.
func (h *ServerlessHandlers) registerBridgeClient(r *http.Request, clientID, namespace string) {
	if h.wsBridge == nil {
		return
	}
	h.wsBridge.SetClientNamespace(clientID, namespace)
	if r.URL.Query().Get(wsPubsubDeliveryParam) == wsPubsubDeliveryStamped {
		h.wsBridge.SetClientDeliveryEnvelope(clientID)
	}
}
