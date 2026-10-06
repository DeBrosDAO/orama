package inspector

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestGlobalCollectScript_readsTheChainOnTheNamespaceAddressWhenCoLocated(t *testing.T) {
	script := globalCollectScript()
	for _, want := range []string{
		"chain_host=127.0.0.1",
		"[ -e /etc/systemd/system/orama-global-netns.service ] && chain_host=198.18.0.2",
		"http://$chain_host:31001/status",
		"http://$chain_host:31003/cosmos/slashing/v1beta1/params",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("script lacks %q", want)
		}
	}
	if strings.Contains(script, "http://127.0.0.1:31001") || strings.Contains(script, "http://127.0.0.1:31003") {
		t.Errorf("script still reads the chain on loopback unconditionally")
	}
}

func TestGlobalCollectScript_asksThroughSudoOnTheNamespaceAddressAndSaysWhenItFails(t *testing.T) {
	script := globalCollectScript()
	if !strings.Contains(script, `if [ "$chain_host" = 198.18.0.2 ]; then sudo -n curl -sf --max-time 3 "$1" || echo `+chainCurlFailedSudo+`; else curl -sf --max-time 3 "$1" || echo `+chainCurlFailed+`; fi`) {
		t.Errorf("chain_curl does not use sudo -n on the namespace address and report failure:\n%s", script)
	}
	if strings.Contains(script, "curl -sf --max-time 3 http://$chain_host") || strings.Contains(script, `curl -sf --max-time 3 "http://$chain_host`) {
		t.Errorf("a chain request bypasses chain_curl")
	}
}

// An active chain unit whose RPC could not be asked is an error, not an empty report that reads as
// a chain that is merely down.
func TestChainFromSections_anActiveChainThatCannotBeAskedIsAnError(t *testing.T) {
	r := chainFromSections(map[string]string{
		"chain_load": "loaded", "chain_state": "active", "status": chainCurlFailed,
	}, time.Now())
	if r == nil || r.Error == "" || r.Responsive {
		t.Fatalf("got %+v, want an error report", r)
	}
	quiet := chainFromSections(map[string]string{
		"chain_load": "loaded", "chain_state": "inactive", "status": chainCurlFailed,
	}, time.Now())
	if quiet == nil || strings.Contains(quiet.Error, "sudo") {
		t.Errorf("a stopped chain must not be reported as a sudo failure: %+v", quiet)
	}
}

func TestChainFromSections_theSudoHintIsOnlyForTheSudoPath(t *testing.T) {
	sudo := chainFromSections(map[string]string{"chain_load": "loaded", "chain_state": "active", "status": chainCurlFailedSudo}, time.Now())
	if sudo == nil || !strings.Contains(sudo.Error, "sudo") {
		t.Errorf("a failed sudo request must name sudo: %+v", sudo)
	}
	plain := chainFromSections(map[string]string{"chain_load": "loaded", "chain_state": "active", "status": chainCurlFailed}, time.Now())
	if plain == nil || plain.Error == "" || strings.Contains(plain.Error, "sudo") {
		t.Errorf("a failed loopback request must not mention sudo: %+v", plain)
	}
}

func TestGlobalCollectScript_asksTheKuboRPCOnTheNamespaceAddressThroughSudoWhenCoLocated(t *testing.T) {
	script := globalCollectScript()
	for _, want := range []string{
		`if [ "$chain_host" = 198.18.0.2 ]; then kubo_curl="sudo -n curl"; else kubo_curl=curl; fi`,
		"http://$chain_host:31011/api/v0/repo/stat",
	} {
		if !strings.Contains(script, want) {
			t.Errorf("script lacks %q", want)
		}
	}
	if strings.Contains(script, "http://127.0.0.1:31011") {
		t.Error("script still reads the Kubo RPC on loopback unconditionally")
	}
}

// The Kubo bearer reaches curl on stdin, never in its argv (ps shows argv), including the
// co-located path that runs curl under sudo.
func TestGlobalCollectScript_sendsTheKuboBearerOnStdinOnly(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	var line string
	for _, l := range strings.Split(globalCollectScript(), "\n") {
		if strings.Contains(l, "/api/v0/repo/stat") {
			line = strings.TrimSpace(l)
		}
	}
	if line == "" || strings.Contains(line, "-H ") {
		t.Fatalf("repo/stat line = %q", line)
	}
	dir := t.TempDir()
	fake := filepath.Join(dir, "curl")
	if err := os.WriteFile(fake, []byte("#!/bin/sh\necho \"$@\" > \"$FAKE_DIR/argv\"\ncat > \"$FAKE_DIR/stdin\"\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	const token = "the-kubo-bearer-token"
	run := exec.Command(bash, "-c", "tok="+token+"; chain_host=198.18.0.2; kubo_curl="+fake+"; "+line)
	run.Env = append(os.Environ(), "FAKE_DIR="+dir)
	if out, err := run.CombinedOutput(); err != nil {
		t.Fatalf("pipeline failed: %v\n%s", err, out)
	}
	argv, _ := os.ReadFile(filepath.Join(dir, "argv"))
	stdin, _ := os.ReadFile(filepath.Join(dir, "stdin"))
	if strings.Contains(string(argv), token) || !strings.Contains(string(argv), "-K -") {
		t.Errorf("curl argv = %s", argv)
	}
	if strings.TrimSpace(string(stdin)) != `header = "Authorization: Bearer `+token+`"` {
		t.Errorf("curl config on stdin = %q", stdin)
	}
}
