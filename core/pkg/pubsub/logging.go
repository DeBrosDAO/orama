package pubsub

import (
	"fmt"
)

// Logf is a package-level logger function used by pubsub internals.
// By default it is a no-op to avoid polluting stdout; applications can
// assign it (e.g., to a UI-backed logger) to surface logs as needed.
var Logf = func(format string, args ...interface{}) { _ = fmt.Sprintf(format, args...) }

// SetLogFunc allows applications to provide a custom logger sink.
func SetLogFunc(f func(string, ...interface{})) {
	if f != nil {
		Logf = f
	}
}
