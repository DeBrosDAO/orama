package production

import (
	"strings"
	"testing"
)

func TestGenerateOlricConfig_omitsIgnoredEncryptionKey(t *testing.T) {
	dir := t.TempDir()
	cg := NewConfigGenerator(dir)
	out, err := cg.GenerateOlricConfig("127.0.0.1", 3320, "127.0.0.1", 3322, "local", "", nil)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(out, "encryptionKey") {
		t.Fatalf("Olric v0.7.0 YAML loader ignores encryptionKey; must not emit it:\n%s", out)
	}
}
