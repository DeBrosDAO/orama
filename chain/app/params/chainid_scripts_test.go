package params

import (
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

// gateScripts are the shell scripts that refuse a chain id before they touch a machine. Each carries
// the chain's pattern as text, so a script and ClassOfChainID cannot disagree on what a test network is.
var gateScripts = []string{
	"chain/scripts/stagenet/deploy.sh",
	"chain/scripts/localnet/localnet.sh",
	"e2e/scripts/chain-deploy.sh",
}

const nameWordCheck = `case "$chain_name" in localnet | devnet | stagenet) chain_class="" ;; esac`

func repoFile(t *testing.T, rel string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join("..", "..", "..", rel))
	if err != nil {
		t.Fatalf("read %s: %v", rel, err)
	}
	return string(data)
}

func TestChainIDGateScripts_carryTheChainsPattern(t *testing.T) {
	assign := regexp.MustCompile(`(?m)^TEST_CHAIN_ID_RE='([^']*)'$`)
	for _, rel := range gateScripts {
		src := repoFile(t, rel)
		m := assign.FindStringSubmatch(src)
		if m == nil {
			t.Fatalf("%s declares no TEST_CHAIN_ID_RE='...': its chain id gate is not the chain's rule", rel)
		}
		if m[1] != testChainIDPattern.String() {
			t.Errorf("%s has chain id pattern %s, chain/app/params has %s", rel, m[1], testChainIDPattern)
		}
		if !strings.Contains(src, nameWordCheck) {
			t.Errorf("%s does not reject a class word as the name word (%s)", rel, nameWordCheck)
		}
		if strings.Contains(src, "*-devnet-*") || strings.Contains(src, "*-stagenet-*") || strings.Contains(src, "*-localnet-*") {
			t.Errorf("%s still gates a chain id on a substring", rel)
		}
	}
}

// The pattern and the name-word check, run by bash on the same ids as ClassOfChainID, give the same class.
func TestChainIDGateScripts_bashAgreesWithClassOfChainID(t *testing.T) {
	bash, err := exec.LookPath("bash")
	if err != nil {
		t.Skip("bash is not installed")
	}
	const snippet = `if [[ "$1" =~ $2 ]]; then chain_name="${BASH_REMATCH[2]}"; chain_class="${BASH_REMATCH[3]}"; fi
` + nameWordCheck + `
printf '%s' "$chain_class"`
	want := map[ChainClass]string{ChainProduction: "", ChainLocalnet: "localnet", ChainDevnet: "devnet", ChainStagenet: "stagenet"}
	ids := []string{
		"orama-stagenet-10", "orama-devnet-9", "orama-localnet-1", "orama-devnet-e2e-run7", "orama-pubnet-stagenet-1",
		"orama-1-stagenet-x", "mainnet-localnet-1", "orama-stagenet-", "orama-stagenet", "orama-1", "",
		"orama-devnet-localnet-1", "orama-localnet-devnet-1", "orama-stagenet-stagenet-1", "orama-devnet-devnet-1",
		"orama-devnet-localnetx-1", "orama-pubnet-localnet-1", "orama-a-b-stagenet-1", "orama-Stagenet-1",
	}
	for _, id := range ids {
		out, err := exec.Command(bash, "-c", snippet, "_", id, testChainIDPattern.String()).Output()
		if err != nil {
			t.Fatalf("bash on %q: %v", id, err)
		}
		if got := string(out); got != want[ClassOfChainID(id)] {
			t.Errorf("chain id %q: the shell gate says class %q, ClassOfChainID says %q", id, got, want[ClassOfChainID(id)])
		}
	}
}
