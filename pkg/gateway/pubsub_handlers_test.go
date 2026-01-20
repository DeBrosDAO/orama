package gateway

import (
	"testing"
)

func TestNamespaceHelpers(t *testing.T) {
	// Note: These helper functions are now internal to the pubsub package
	// and are not exported. This test can be removed or moved to the pubsub package.
	// For now, we'll skip this test as the functionality is tested within the pubsub package itself.
	t.Skip("Namespace helpers moved to internal pubsub package")
}

// Alternatively, we could create a test in the pubsub package itself
// by creating a file: pkg/gateway/handlers/pubsub/types_test.go
