package production

import (
	"os"
	"path/filepath"
	"testing"
)

func TestWriteConfig_mode0600(t *testing.T) {
	dir := t.TempDir()
	wp := &WireGuardProvisioner{
		configDir: dir,
		config: WireGuardConfig{
			PrivateIP:  "10.0.0.1",
			PrivateKey: "dGVzdA==",
		},
	}
	if err := wp.WriteConfig(); err != nil {
		t.Fatal(err)
	}
	fi, err := os.Stat(filepath.Join(dir, "wg0.conf"))
	if err != nil {
		t.Fatal(err)
	}
	if fi.Mode().Perm() != 0o600 {
		t.Fatalf("wg0.conf mode %o, want 0600", fi.Mode().Perm())
	}
}
