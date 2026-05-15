package serverless

import "os"

// readFileImpl is split into its own file so registry_ws_columns_test.go
// stays focused on the assertion logic and doesn't import os directly
// (which would be unused in some builds).
func readFileImpl(path string) ([]byte, error) {
	return os.ReadFile(path)
}
