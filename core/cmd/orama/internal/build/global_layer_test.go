package build

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/constants"
)

func fakeToolchain(missing []string, targets string) toolchain {
	return toolchain{
		lookPath: func(name string) (string, error) {
			for _, m := range missing {
				if m == name {
					return "", errors.New("not found")
				}
			}
			return "/usr/bin/" + name, nil
		},
		output: func(string, ...string) ([]byte, error) { return []byte(targets), nil },
	}
}

func TestCheckGlobalToolchain_namesWhatIsMissing(t *testing.T) {
	if err := checkGlobalToolchain(fakeToolchain(nil, "x86_64-unknown-linux-musl\naarch64-apple-darwin\n")); err != nil {
		t.Fatal(err)
	}
	err := checkGlobalToolchain(fakeToolchain([]string{"cargo", "rsync"}, ""))
	if err == nil || !strings.Contains(err.Error(), "cargo, rsync") || !strings.Contains(err.Error(), skipGlobalFlag) {
		t.Fatalf("err = %v, want the missing programs and the way to skip", err)
	}
	err = checkGlobalToolchain(fakeToolchain(nil, "aarch64-apple-darwin\n"))
	if err == nil || !strings.Contains(err.Error(), "rustup target add x86_64-unknown-linux-musl") {
		t.Fatalf("err = %v, want the rustup command", err)
	}
}

func TestPlanGlobalLayer_archIsAmd64OnlyAndSkippable(t *testing.T) {
	b := &Builder{flags: &Flags{Arch: "arm64"}}
	err := b.planGlobalLayer()
	if err == nil || clierr.CodeOf(err) != clierr.CodeUsage || !strings.Contains(err.Error(), skipGlobalFlag) {
		t.Fatalf("an arm64 archive with the global layer: %v", err)
	}
	b = &Builder{flags: &Flags{Arch: "arm64", SkipGlobalLayer: true}}
	if err := b.planGlobalLayer(); err != nil || b.globalLayer {
		t.Fatalf("skipped: err %v, globalLayer %v", err, b.globalLayer)
	}
}

// chainOutputs writes a chain build directory: a verifier, its digest, an
// oramad that pins it and orama-global.
func chainOutputs(t *testing.T, pinned, sidecar string) (buildDir string, sum string) {
	t.Helper()
	buildDir = t.TempDir()
	verifier := []byte("verifier bytes")
	h := sha256.Sum256(verifier)
	sum = hex.EncodeToString(h[:])
	if pinned == "" {
		pinned = sum
	}
	if sidecar == "" {
		sidecar = sum
	}
	for name, body := range map[string]string{
		builtOrchardVerifier:             string(verifier),
		builtOrchardVerifier + ".sha256": sidecar + "\n",
		builtOramad:                      "oramad code ... " + pinned + " ... more code",
		builtOramaGlobal:                 "orama-global code",
	} {
		if err := os.WriteFile(filepath.Join(buildDir, name), []byte(body), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	return buildDir, sum
}

func TestStageGlobalFiles_putsTheLayerInBinUnderTheInstallersNames(t *testing.T) {
	buildDir, sum := chainOutputs(t, "", "")
	bin := t.TempDir()
	if err := stageGlobalFiles(buildDir, bin); err != nil {
		t.Fatal(err)
	}
	for _, name := range []string{"oramad", "orama-orchard-verifier", "orama-global"} {
		info, err := os.Stat(filepath.Join(bin, name))
		if err != nil || info.Mode()&0o111 == 0 {
			t.Errorf("%s: %v, %v", name, info, err)
		}
	}
	got, err := os.ReadFile(filepath.Join(bin, constants.ChainVerifierSHA256File))
	if err != nil || strings.TrimSpace(string(got)) != sum {
		t.Fatalf("sidecar = %q, %v", got, err)
	}
}

func TestStageGlobalFiles_refusesPartsThatDoNotBelongTogether(t *testing.T) {
	other := strings.Repeat("ab", 32)
	cases := map[string]struct{ pinned, sidecar string }{
		"an oramad that pins another verifier": {pinned: other},
		"a sidecar of another file":            {sidecar: other},
	}
	for name, c := range cases {
		buildDir, _ := chainOutputs(t, c.pinned, c.sidecar)
		if err := stageGlobalFiles(buildDir, t.TempDir()); err == nil {
			t.Errorf("%s was staged", name)
		}
	}
}

func TestStageGlobalFiles_aMissingOutputIsAnError(t *testing.T) {
	for _, drop := range []string{builtOramad, builtOrchardVerifier, builtOrchardVerifier + ".sha256", builtOramaGlobal} {
		buildDir, _ := chainOutputs(t, "", "")
		if err := os.Remove(filepath.Join(buildDir, drop)); err != nil {
			t.Fatal(err)
		}
		if err := stageGlobalFiles(buildDir, t.TempDir()); err == nil {
			t.Errorf("a build without %s was staged", drop)
		}
	}
}
