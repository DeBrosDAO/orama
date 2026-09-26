package inspector

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/ipfs"
)

// The REST API requires credentials. They are derived from the cluster secret
// the way install derived them, and given to curl on stdin, never on its
// command line. service.json is the orama user's file and is not read.
func TestIPFSClusterCurl_sendsCredentialsOnStdinOnly(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	dir := t.TempDir()
	secret := "cluster-secret\nwith a newline and a \"quote"
	secretPath := filepath.Join(dir, "cluster-secret")
	if err := os.WriteFile(secretPath, []byte(secret), 0o600); err != nil {
		t.Fatal(err)
	}
	// A service.json a tenant could have rewritten. The pipeline must not open it.
	if err := os.WriteFile(filepath.Join(dir, "service.json"), []byte("user = \"orama:pwned\"\nurl = \"file:///etc/passwd\"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	fakes := map[string]string{
		"sudo": "#!/bin/sh\nexec \"$@\"\n",
		"curl": "#!/bin/sh\necho \"$@\" > \"$FAKE_DIR/argv\"\ncat > \"$FAKE_DIR/stdin\"\n",
	}
	for name, script := range fakes {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	want, err := ipfs.ClusterRESTPassword(secret)
	if err != nil {
		t.Fatal(err)
	}
	cmd := strings.Replace(ipfsClusterCurl("--max-time 10", "/peers"), ipfsClusterSecretPath, secretPath, 1)
	if strings.Contains(cmd, "service.json") {
		t.Fatal("the pipeline still reads service.json")
	}
	run := exec.Command(bash, "-c", cmd)
	run.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "FAKE_DIR="+dir)
	if out, err := run.CombinedOutput(); err != nil {
		t.Fatalf("pipeline failed: %v\n%s", err, out)
	}
	argv, _ := os.ReadFile(filepath.Join(dir, "argv"))
	stdin, _ := os.ReadFile(filepath.Join(dir, "stdin"))
	if strings.Contains(string(argv), want) || strings.Contains(string(argv), "quote") {
		t.Errorf("the password is on curl's command line: %s", argv)
	}
	if !strings.Contains(string(argv), "-K -") || !strings.Contains(string(argv), "http://localhost:10108/peers") {
		t.Errorf("curl argv = %s", argv)
	}
	if strings.TrimSpace(string(stdin)) != `user = "orama:`+want+`"` {
		t.Errorf("curl config on stdin = %q", stdin)
	}
}

func TestIPFSKuboCurl_sendsTheBearerOnStdinOnly(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	dir := t.TempDir()
	secretPath := filepath.Join(dir, "cluster-secret")
	if err := os.WriteFile(secretPath, []byte("cluster-secret\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, script := range map[string]string{
		"sudo": "#!/bin/sh\nexec \"$@\"\n",
		"curl": "#!/bin/sh\necho \"$@\" > \"$FAKE_DIR/argv\"\ncat > \"$FAKE_DIR/stdin\"\n",
	} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	want, err := ipfs.KuboAPIToken("cluster-secret")
	if err != nil {
		t.Fatal(err)
	}
	cmd := strings.Replace(ipfsKuboCurl("/api/v0/id"), ipfsClusterSecretPath, secretPath, 1)
	run := exec.Command(bash, "-c", cmd)
	run.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "FAKE_DIR="+dir)
	if out, err := run.CombinedOutput(); err != nil {
		t.Fatalf("pipeline failed: %v\n%s", err, out)
	}
	argv, _ := os.ReadFile(filepath.Join(dir, "argv"))
	stdin, _ := os.ReadFile(filepath.Join(dir, "stdin"))
	if strings.Contains(string(argv), want) {
		t.Errorf("the bearer is on curl's command line: %s", argv)
	}
	if !strings.Contains(string(stdin), "Authorization: Bearer "+want) {
		t.Errorf("curl config on stdin = %q", stdin)
	}
}

// A symlink where the secret should be is the orama user's file pointing
// wherever they like. The derivation refuses it instead of hashing the target
// into a curl config.
func TestIPFSClusterCurl_refusesASymlinkedSecret(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash not available")
	}
	if _, err := exec.LookPath("python3"); err != nil {
		t.Skip("python3 not available")
	}
	dir := t.TempDir()
	real := filepath.Join(dir, "real-secret")
	if err := os.WriteFile(real, []byte("cluster-secret"), 0o600); err != nil {
		t.Fatal(err)
	}
	link := filepath.Join(dir, "cluster-secret")
	if err := os.Symlink(real, link); err != nil {
		t.Fatal(err)
	}
	bin := filepath.Join(dir, "bin")
	if err := os.Mkdir(bin, 0o755); err != nil {
		t.Fatal(err)
	}
	for name, script := range map[string]string{
		"sudo": "#!/bin/sh\nexec \"$@\"\n",
		"curl": "#!/bin/sh\necho invoked > \"$FAKE_DIR/curl-ran\"\n",
	} {
		if err := os.WriteFile(filepath.Join(bin, name), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	cmd := strings.Replace(ipfsClusterCurl("", "/id"), ipfsClusterSecretPath, link, 1)
	run := exec.Command(bash, "-c", cmd)
	run.Env = append(os.Environ(), "PATH="+bin+":"+os.Getenv("PATH"), "FAKE_DIR="+dir)
	if err := run.Run(); err == nil {
		t.Fatal("a symlinked cluster secret was derived")
	}
	if _, err := os.Stat(filepath.Join(dir, "curl-ran")); !os.IsNotExist(err) {
		t.Fatal("curl ran on a refused secret")
	}
}
