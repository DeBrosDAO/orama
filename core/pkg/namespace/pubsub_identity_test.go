package namespace

import (
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strings"
	"testing"
)

// The pubsub unit may only write under ReadWritePaths; an identity key outside
// them fails with "read-only file system" and the unit crash-loops. Holding the
// path to the template keeps the two from drifting apart again.
func TestPubsubIdentityDir_IsInsideWhatTheUnitMayWrite(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	unit, err := os.ReadFile(filepath.Join(filepath.Dir(file), "..", "..", "systemd", "orama-namespace-pubsub@.service"))
	if err != nil {
		t.Fatal(err)
	}
	m := regexp.MustCompile(`(?m)^ReadWritePaths=(.*)$`).FindStringSubmatch(string(unit))
	if m == nil {
		t.Fatal("pubsub unit has no ReadWritePaths")
	}

	const oramaData = "/opt/orama/.orama/data"
	dir := pubsubIdentityDir(filepath.Join(oramaData, "namespaces"))
	for _, allowed := range strings.Fields(m[1]) {
		allowed = strings.ReplaceAll(allowed, "%i", BlueprintNameIndex)
		if strings.HasPrefix(dir+"/", strings.TrimRight(allowed, "/")+"/") {
			return
		}
	}
	t.Errorf("identity dir %s is outside the unit's ReadWritePaths %q", dir, m[1])
}
