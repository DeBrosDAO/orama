package contracts

// Logger defines a structured logging interface.
// Provides leveled logging with contextual fields for debugging and monitoring.
type Logger interface {
	// Debug logs a debug-level message with optional fields.
	Debug(msg string, fields ...Field)

	// Info logs an info-level message with optional fields.
	Info(msg string, fields ...Field)

	// Warn logs a warning-level message with optional fields.
	Warn(msg string, fields ...Field)

	// Error logs an error-level message with optional fields.
	Error(msg string, fields ...Field)

	// Fatal logs a fatal-level message and terminates the application.
	Fatal(msg string, fields ...Field)

	// With creates a child logger with additional context fields.
	// The returned logger includes all parent fields plus the new ones.
	With(fields ...Field) Logger

	// Sync flushes any buffered log entries.
	// Should be called before application shutdown.
	Sync() error
}

// Field represents a structured logging field with a key and value.
// Implementations typically use zap.Field or similar structured logging types.
type Field interface {
	// Key returns the field's key name.
	Key() string

	// Value returns the field's value.
	Value() interface{}
}

// LoggerFactory creates logger instances with configuration.
type LoggerFactory interface {
	// NewLogger creates a new logger with the given name.
	// The name is typically used as a component identifier in logs.
	NewLogger(name string) Logger

	// NewLoggerWithFields creates a new logger with pre-set context fields.
	NewLoggerWithFields(name string, fields ...Field) Logger
}
