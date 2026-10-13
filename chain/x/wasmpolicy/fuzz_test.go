package wasmpolicy

import (
	"regexp"
	"testing"
)

var noramaFactory = regexp.MustCompile(`^factory/[^/]+/norama$`)

// FuzzWasmPolicyNoramaWrapper: a contract can never create the native denom or a factory token
// named norama, nor hold a factory norama wrapper; native norama may be held. Compared with an
// independent pattern of the same rule.
func FuzzWasmPolicyNoramaWrapper(f *testing.F) {
	for _, s := range []string{"norama", "factory/orama1x/norama", "factory//norama", "factory/a/b/norama", "factory/a/NORAMA", "wnorama", "", "factory/a/norama/"} {
		f.Add(s, s)
	}
	f.Fuzz(func(t *testing.T, create, hold string) {
		isNorama := func(d string) bool { return d == "norama" || noramaFactory.MatchString(d) }
		err := RefuseNoramaWrapper(create, []string{hold})
		wantRefused := isNorama(create) || noramaFactory.MatchString(hold)
		if (err != nil) != wantRefused {
			t.Fatalf("RefuseNoramaWrapper(%q, [%q]) = %v, rule says refused=%v", create, hold, err, wantRefused)
		}
	})
}
