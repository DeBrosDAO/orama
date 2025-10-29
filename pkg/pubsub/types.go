package pubsub

// MessageHandler represents a message handler function signature.
// Each handler is called when a message arrives on a subscribed topic.
// Multiple handlers can be registered for the same topic, and each will
// receive a copy of the message. Handlers should return an error only for
// critical failures; the error is logged but does not stop other handlers.
// This matches the client.MessageHandler type to avoid circular imports.
type MessageHandler func(topic string, data []byte) error

// HandlerID uniquely identifies a handler registration.
// Each call to Subscribe generates a new HandlerID, allowing
// multiple subscribers to the same topic with independent lifecycles.
// Unsubscribe operations are ref-counted per topic.
type HandlerID string