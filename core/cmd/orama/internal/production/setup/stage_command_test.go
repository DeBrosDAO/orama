package setup

import (
	"strings"
	"testing"
)

func TestStageArchiveCommand_isTheUploadsStageCommandWithItsInputsChecked(t *testing.T) {
	const dir = "/tmp/orama-archive.AbC12345"
	const upper, lower = "0xABCDEF0123456789ABCDEF0123456789ABCDEF01", "0xabcdef0123456789abcdef0123456789abcdef01"
	got, err := StageArchiveCommand(dir, testCLISum, []string{upper})
	if err != nil {
		t.Fatal(err)
	}
	if want := stageArchiveCommand(dir, testCLISum, []string{lower}); got != want {
		t.Errorf("the command differs from the one an upload stages with:\n%s\n%s", got, want)
	}
}

func TestStageArchiveCommand_refusesWhatCouldNotGoIntoARootShellCommand(t *testing.T) {
	const dir = "/tmp/orama-archive.AbC12345"
	cases := map[string]struct {
		dir, sum string
		signers  []string
	}{
		"a directory outside /tmp":       {"/etc/orama-archive.AbC12345", testCLISum, []string{operatorWallet}},
		"a directory with a shell quote": {"/tmp/orama-archive.AbC1'234", testCLISum, []string{operatorWallet}},
		"a directory that goes up":       {dir + "/../..", testCLISum, []string{operatorWallet}},
		"an empty directory":             {"", testCLISum, []string{operatorWallet}},
		"a short checksum":               {dir, "abcdef", []string{operatorWallet}},
		"an upper case checksum":         {dir, strings.ToUpper(testCLISum), []string{operatorWallet}},
		"a checksum with a command":      {dir, testCLISum[:60] + "; id", []string{operatorWallet}},
		"a signer that is no address":    {dir, testCLISum, []string{"0x1; reboot"}},
		"no signer":                      {dir, testCLISum, nil},
	}
	for name, c := range cases {
		if cmd, err := StageArchiveCommand(c.dir, c.sum, c.signers); err == nil {
			t.Errorf("%s was accepted: %s", name, cmd)
		}
	}
}
