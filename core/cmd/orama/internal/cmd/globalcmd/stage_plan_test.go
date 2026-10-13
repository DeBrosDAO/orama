package globalcmd

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/cosmovisor"
	"github.com/DeBrosOfficial/network/pkg/planinfo"
)

func writeInfo(t *testing.T, info string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "info.json")
	if err := os.WriteFile(path, []byte(info), 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

func planInfoFile(t *testing.T, oramad, verifier string) string {
	t.Helper()
	platform := runtime.GOOS + "/" + runtime.GOARCH
	return writeInfo(t, `{"binaries":{"`+platform+`":"https://x.example/oramad?checksum=sha256:`+oramad+`"},`+
		`"verifiers":{"`+platform+`":"https://x.example/orama-orchard-verifier?checksum=sha256:`+verifier+`"}}`+"\n")
}

func TestPlanDigests_readsWhatThePlanNamesAndRefusesWhatItDoesNot(t *testing.T) {
	oramad, verifier := strings.Repeat("a", 64), strings.Repeat("b", 64)
	got, err := planDigests(context.Background(), "v2", planInfoFile(t, oramad, verifier))
	if err != nil || got.Oramad != oramad || got.Verifier != verifier {
		t.Fatalf("planDigests = %+v, %v", got, err)
	}
	_, err = planDigests(context.Background(), "v2", writeInfo(t, "upgrade to v2"))
	if clierr.CodeOf(err) != clierr.CodeConflict || !strings.Contains(err.Error(), "carries no upgrade-info JSON") {
		t.Fatalf("a plan that names no checksums: %v", err)
	}
}

func TestPlanDigests_unreadableOrOversizedInfoIsRefused(t *testing.T) {
	if _, err := planDigests(context.Background(), "v2", filepath.Join(t.TempDir(), "missing")); err == nil || !strings.Contains(err.Error(), "--plan-info") {
		t.Errorf("a missing info file: %v", err)
	}
	huge := writeInfo(t, strings.Repeat("x", planInfoLimit+1))
	if _, err := planDigests(context.Background(), "v2", huge); clierr.CodeOf(err) != clierr.CodeUsage {
		t.Errorf("an oversized info file: %v", err)
	}
}

// stagedCopy is what the stager does with src: a fresh file, copied from src as it is now, open for
// reading and writing like the descriptor the stager hands to the verification.
func stagedCopy(t *testing.T, src string) *os.File {
	t.Helper()
	in, err := os.Open(src)
	if err != nil {
		t.Fatal(err)
	}
	defer in.Close()
	out, err := os.CreateTemp(t.TempDir(), "staged-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { out.Close() })
	if _, err := io.Copy(out, in); err != nil {
		t.Fatal(err)
	}
	return out
}

func accept(*os.File) error { return nil }

func TestPlannedVerifies_holdBothFilesToTheChecksumsThePlanNames(t *testing.T) {
	dir := t.TempDir()
	oramad := filepath.Join(dir, "oramad")
	verifier := filepath.Join(dir, "orama-orchard-verifier")
	for path, content := range map[string]string{oramad: "oramad v2", verifier: "verifier v2"} {
		if err := os.WriteFile(path, []byte(content), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	oramadSum, err := sha256File(oramad)
	if err != nil {
		t.Fatal(err)
	}
	verifierSum, err := sha256File(verifier)
	if err != nil {
		t.Fatal(err)
	}
	other := strings.Repeat("0", 64)

	tests := []struct {
		name              string
		oramad, verifier  string
		wantOramadErr     string
		wantVerifierError string
	}{
		{"the plan names both", oramadSum, verifierSum, "", ""},
		{"another oramad", other, verifierSum, "names an oramad with sha256 000000000000", ""},
		{"another verifier", oramadSum, other, "", "names an orama-orchard-verifier with sha256 000000000000"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			vOramad, vVerifier := plannedVerifies("v2", planinfo.Digests{Oramad: tt.oramad, Verifier: tt.verifier}, accept, accept)
			for _, c := range []struct {
				verify cosmovisor.Verify
				path   string
				want   string
			}{{vOramad, oramad, tt.wantOramadErr}, {vVerifier, verifier, tt.wantVerifierError}} {
				err := c.verify(stagedCopy(t, c.path))
				switch {
				case c.want == "" && err != nil:
					t.Fatalf("a file the plan names was refused: %v", err)
				case c.want != "" && (!errors.Is(err, planinfo.ErrMismatch) || !strings.Contains(err.Error(), c.want)):
					t.Fatalf("err = %v, want a refusal naming %q", err, c.want)
				}
			}
		})
	}
}

// TestPlannedVerifies_aFileSwappedAfterThePlanWasReadIsRefused: the path the operator gave names the
// plan's oramad when the plan is read and something else when the stager copies it. The check is of
// the copy, so the swap is refused. A check of the path before staging, which this replaced, passed.
func TestPlannedVerifies_aFileSwappedAfterThePlanWasReadIsRefused(t *testing.T) {
	dir := t.TempDir()
	oramad := filepath.Join(dir, "oramad")
	if err := os.WriteFile(oramad, []byte("oramad v2"), 0o755); err != nil {
		t.Fatal(err)
	}
	sum, err := sha256File(oramad)
	if err != nil {
		t.Fatal(err)
	}
	verify, _ := plannedVerifies("v2", planinfo.Digests{Oramad: sum, Verifier: strings.Repeat("b", 64)}, accept, accept)

	if err := verify(stagedCopy(t, oramad)); err != nil {
		t.Fatalf("the file the plan names, copied before any swap: %v", err)
	}
	if err := os.WriteFile(oramad, []byte("oramad v2, but not the one the proposal was for"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := verify(stagedCopy(t, oramad)); !errors.Is(err, planinfo.ErrMismatch) {
		t.Fatalf("a swapped file reached staging: %v", err)
	}
}

func TestPlannedVerifies_theReleaseVerificationRunsFirstAndItsRefusalStands(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oramad")
	if err := os.WriteFile(path, []byte("oramad v2"), 0o755); err != nil {
		t.Fatal(err)
	}
	sum, err := sha256File(path)
	if err != nil {
		t.Fatal(err)
	}
	notInRelease := errors.New("not in the release metadata")
	verify, _ := plannedVerifies("v2", planinfo.Digests{Oramad: sum, Verifier: strings.Repeat("b", 64)}, func(*os.File) error { return notInRelease }, accept)
	if err := verify(stagedCopy(t, path)); !errors.Is(err, notInRelease) {
		t.Fatalf("a file the plan names but the release does not: %v", err)
	}
}

func TestPlannedVerifies_aPlanWithoutUsableChecksumsStagesNothing(t *testing.T) {
	path := filepath.Join(t.TempDir(), "oramad")
	if err := os.WriteFile(path, []byte("x"), 0o755); err != nil {
		t.Fatal(err)
	}
	verify, verifierVerify := plannedVerifies("v2", planinfo.Digests{}, accept, accept)
	for _, v := range []cosmovisor.Verify{verify, verifierVerify} {
		if err := v(stagedCopy(t, path)); !errors.Is(err, planinfo.ErrMismatch) {
			t.Fatalf("an empty plan accepted a file: %v", err)
		}
	}
}
