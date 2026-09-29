//go:build !linux

package globalnetns

import "fmt"

// InNamespace is Linux only.
func InNamespace(path string, fn func() error) error {
	return fmt.Errorf("network namespaces are a Linux feature; cannot join %s", path)
}
