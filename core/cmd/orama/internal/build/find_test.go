package build

import "testing"

func TestArchiveName(t *testing.T) {
	if got, want := ArchiveName("0.200.0", "amd64"), "orama-0.200.0-linux-amd64.tar.gz"; got != want {
		t.Errorf("ArchiveName = %q, want %q", got, want)
	}
}
