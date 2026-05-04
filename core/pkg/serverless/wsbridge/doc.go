// Package wsbridge wires PubSub topics directly to WebSocket clients,
// bypassing the per-event WASM invocation overhead.
//
// A function that wants to forward many high-frequency PubSub events to a
// connected client calls ws_pubsub_bridge(clientID, topic) once. The
// gateway then auto-forwards every matching message to that client's WS
// without invoking the WASM module per event.
//
// Subscriptions are namespace-scoped and reference-counted: when the first
// client in namespace N bridges topic T, a libp2p subscription is opened.
// Subsequent clients reuse it. When the last client unbridges (or
// disconnects), the libp2p subscription is dropped.
//
// See plan: core/plans/platform/10_WS_PUBSUB_BRIDGE.md
package wsbridge
