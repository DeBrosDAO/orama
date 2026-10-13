package report

import (
	"slices"
	"testing"
)

func TestNamespaceServiceNames_skipsTemplates(t *testing.T) {
	got := namespaceServiceNames([]string{
		"/etc/systemd/system/orama-namespace-wireguard@.service",
		"/etc/systemd/system/orama-namespace-rqlite@index.service",
		"/etc/systemd/system/orama-namespace-coredns@nameserver.service",
	})
	want := []string{"orama-namespace-rqlite@index", "orama-namespace-coredns@nameserver"}
	if !slices.Equal(got, want) {
		t.Fatalf("namespaceServiceNames = %v, want %v (a template is not a service)", got, want)
	}
}

func TestNamespaceServiceNames_none(t *testing.T) {
	if got := namespaceServiceNames(nil); got != nil {
		t.Fatalf("namespaceServiceNames(nil) = %v", got)
	}
}
