package globalcmd

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"runtime"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/install"
)

func chainServer(t *testing.T, body string, status int) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

var (
	sameOramad = install.ChainBinaryState{CurrentSHA256: strings.Repeat("a", 64), ReleaseSHA256: strings.Repeat("a", 64)}
	newOramad  = install.ChainBinaryState{CurrentSHA256: strings.Repeat("a", 64), ReleaseSHA256: strings.Repeat("b", 64)}
)

func TestHandleChainBinary_sameOramadNeedsNothing(t *testing.T) {
	var out bytes.Buffer

	err := handleChainBinary(context.Background(), &out, install.GlobalHost{}, install.RefreshOptions{}, sameOramad, "http://127.0.0.1:1")

	if err != nil || !strings.Contains(out.String(), "nothing to stage") {
		t.Fatalf("err = %v, output %q", err, out.String())
	}
}

func TestHandleChainBinary_newOramadWithoutAGovernedUpgradeIsKeptAndSaid(t *testing.T) {
	srv := chainServer(t, `{"plan":null}`, http.StatusOK)
	var out bytes.Buffer

	err := handleChainBinary(context.Background(), &out, install.GlobalHost{}, install.RefreshOptions{}, newOramad, srv.URL)

	if err != nil {
		t.Fatalf("err = %v: a release without a scheduled upgrade is not a failure", err)
	}
	for _, want := range []string{"oramad kept", "no governed upgrade is scheduled", strings.Repeat("b", 12), strings.Repeat("a", 12)} {
		if !strings.Contains(out.String(), want) {
			t.Errorf("output lacks %q:\n%s", want, out.String())
		}
	}
}

func TestHandleChainBinary_aScheduledUpgradeThatCannotBeStagedIsAnError(t *testing.T) {
	srv := chainServer(t, `{"plan":{"name":"v0-4-0"}}`, http.StatusOK)
	var out bytes.Buffer

	err := handleChainBinary(context.Background(), &out, install.GlobalHost{}, install.RefreshOptions{Manifest: "/nonexistent/manifest.json"}, newOramad, srv.URL)

	if err == nil || !strings.Contains(err.Error(), "release manifest") {
		t.Fatalf("err = %v, want the staging failure", err)
	}
	if strings.Contains(out.String(), "staged") {
		t.Errorf("output claims a stage that failed:\n%s", out.String())
	}
}

func planWithChecksum(sum string) string {
	info := `{"binaries":{"` + runtime.GOOS + `/` + runtime.GOARCH + `":"https://x.example/oramad?checksum=sha256:` + sum + `"}}`
	body, _ := json.Marshal(map[string]any{"plan": map[string]string{"name": "v0-4-0", "info": info}})
	return string(body)
}

func TestHandleChainBinary_aPlanThatNamesAnotherBinaryIsNotStaged(t *testing.T) {
	srv := chainServer(t, planWithChecksum(strings.Repeat("c", 64)), http.StatusOK)
	var out bytes.Buffer

	err := handleChainBinary(context.Background(), &out, install.GlobalHost{}, install.RefreshOptions{Manifest: "/nonexistent/manifest.json"}, newOramad, srv.URL)

	if clierr.CodeOf(err) != clierr.CodeConflict || !strings.Contains(err.Error(), "not the one the proposal was for") {
		t.Fatalf("err = %v, want a refusal before anything is staged (a staging attempt would have failed on the missing manifest instead)", err)
	}
}

func TestHandleChainBinary_aPlanThatNamesThisBinaryIsStaged(t *testing.T) {
	srv := chainServer(t, planWithChecksum(newOramad.ReleaseSHA256), http.StatusOK)
	var out bytes.Buffer

	err := handleChainBinary(context.Background(), &out, install.GlobalHost{}, install.RefreshOptions{Manifest: "/nonexistent/manifest.json"}, newOramad, srv.URL)

	if err == nil || !strings.Contains(err.Error(), "release manifest") {
		t.Fatalf("err = %v, want staging attempted (and failing on the missing manifest)", err)
	}
}

func TestHandleChainBinary_aPlanThatNamesNoChecksumSaysTheBinaryWasNotCheckedAgainstIt(t *testing.T) {
	srv := chainServer(t, `{"plan":{"name":"v0-4-0","info":"upgrade to 0.4.0"}}`, http.StatusOK)
	var out bytes.Buffer

	_ = handleChainBinary(context.Background(), &out, install.GlobalHost{}, install.RefreshOptions{Manifest: "/nonexistent/manifest.json"}, newOramad, srv.URL)

	if !strings.Contains(out.String(), "names no checksum") {
		t.Errorf("output does not say the release's oramad was not checked against the plan:\n%s", out.String())
	}
}

func TestHandleChainBinary_anUnreadablePlanLeavesTheChainUndecidedAsAnError(t *testing.T) {
	srv := chainServer(t, `boom`, http.StatusInternalServerError)
	var out bytes.Buffer

	err := handleChainBinary(context.Background(), &out, install.GlobalHost{}, install.RefreshOptions{}, newOramad, srv.URL)

	if err == nil || !strings.Contains(err.Error(), "undecided") {
		t.Fatalf("err = %v, want the undecided chain binary reported", err)
	}
}

func TestShort(t *testing.T) {
	if got := short(strings.Repeat("c", 64)); got != strings.Repeat("c", 12) {
		t.Errorf("short = %q", got)
	}
	if got := short("abc"); got != "abc" {
		t.Errorf("short of a short digest = %q", got)
	}
}
