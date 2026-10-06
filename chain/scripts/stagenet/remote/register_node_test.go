package remote_test

import (
	"os"
	"os/exec"
	"strings"
	"testing"
)

// register-node.sh runs as root on a node and puts these values into shell arithmetic and onto
// command lines, so it re-validates every one of them before it changes anything.
func registerArgs(mutate func(args []string)) []string {
	args := []string{
		"orama-stagenet-1", "node-id", "203.0.113.7", "16276", "1000000000", "1000000000",
		"10737418240", "2000000000", "600000",
	}
	mutate(args)
	return args
}

func runRegister(t *testing.T, args []string) (string, error) {
	t.Helper()
	out, err := exec.Command("bash", append([]string{"register-node.sh"}, args...)...).CombinedOutput()
	return string(out), err
}

func TestRegisterNode_refusesANumberThatIsNotAPlainDecimal(t *testing.T) {
	names := map[int]string{3: "ASN", 4: "STORAGE_BOND", 5: "ARCHIVER_BOND", 6: "CAPACITY_BYTES", 7: "HOT_KEY_FUND", 8: "TX_GAS"}
	for idx, name := range names {
		for _, bad := range []string{"", "12x", "-1", "1e9", "0x10", "1 2", "$(id)", "1234567890123456", "07; rm -rf /", "010", "00", "0123"} {
			out, err := runRegister(t, registerArgs(func(a []string) { a[idx] = bad }))
			if err == nil {
				t.Errorf("%s=%q was accepted", name, bad)
				continue
			}
			if !strings.Contains(out, name+" is not a plain number") {
				t.Errorf("%s=%q: output %q does not name the refused value", name, bad, out)
			}
		}
	}
}

// A good set of numbers gets past the validation (it then stops at the first thing a node has and
// this machine does not).
func TestRegisterNode_acceptsPlainDecimalsUpToFifteenDigits(t *testing.T) {
	// Past validation the script acts on the machine it runs on (it is written to run as root on a
	// node), so this test never runs it as root: a CI root runner would go on to real work.
	if os.Geteuid() == 0 {
		t.Skip("the script acts on the host past its validation; not run as root")
	}
	out, _ := runRegister(t, registerArgs(func(a []string) { a[4] = "999999999999999" }))
	if strings.Contains(out, "is not a plain number") {
		t.Errorf("a 15-digit bond was refused: %s", out)
	}
}
