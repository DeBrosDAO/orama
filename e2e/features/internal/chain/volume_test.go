//go:build e2e_fleet

package chain

import (
	"strings"
	"testing"
)

const testBackup = "/var/lib/orama-global/chain/data.e2e-r1"

func TestParseFS_blocksAndMount(t *testing.T) {
	fs, err := ParseFS("4096 1000 250\n/var/lib/orama-global\n")
	if err != nil {
		t.Fatal(err)
	}
	if fs.Mount != "/var/lib/orama-global" || fs.Free != 4096*1000 || fs.Avail != 4096*250 {
		t.Errorf("got %+v", fs)
	}
}

func TestParseFS_refusesWhatItCannotRead(t *testing.T) {
	for name, in := range map[string]string{
		"empty":                   "",
		"one line":                "4096 1000 250",
		"two fields":              "4096 1000\n/",
		"a word for a count":      "4096 lots 250\n/",
		"a negative count":        "4096 -1 250\n/",
		"a mount that is no path": "4096 1000 250\nFilesystem",
		"three lines":             "4096 1000 250\n/\n/tmp",
	} {
		if _, err := ParseFS(in); err == nil {
			t.Errorf("%s: %q was accepted", name, in)
		}
	}
}

func TestWipeDataScript_markerBeforeTheMoveAndNoStaleBackup(t *testing.T) {
	got := WipeDataScript(testBackup)
	for _, want := range []string{
		"set -e", "test ! -e '" + testBackup + "'", "test ! -e '" + testBackup + ".wiped'",
		"mv '/var/lib/orama-global/chain/data' '" + testBackup + "'", SignStateFile,
	} {
		if !strings.Contains(got, want) {
			t.Errorf("wipe script lacks %q:\n%s", want, got)
		}
	}
	if touch, move := strings.Index(got, "touch"), strings.Index(got, "mv "); touch < 0 || touch > move {
		t.Errorf("the marker must be written before the move:\n%s", got)
	}
}

func TestRestoreDataScript_onlyWithTheMarkerAndKeepsTheNewerSignState(t *testing.T) {
	got := RestoreDataScript(testBackup)
	for _, want := range []string{
		"if [ -e '" + testBackup + ".wiped' ] && [ -d '" + testBackup + "' ]",
		"systemctl stop " + Unit, "install -o " + ServiceUser, "/" + SignStateFile, "rm -f '" + testBackup + ".wiped'",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("restore script lacks %q:\n%s", want, got)
		}
	}
	if stop, swap := strings.Index(got, "systemctl stop"), strings.Index(got, "mv "); stop > swap {
		t.Errorf("the unit must stop before the data directories swap:\n%s", got)
	}
}

func TestDropBackupScript_removesTheBackupAndItsMarker(t *testing.T) {
	got := DropBackupScript(testBackup)
	if !strings.Contains(got, "'"+testBackup+"'") || !strings.Contains(got, "'"+testBackup+".wiped'") {
		t.Errorf("got %q", got)
	}
}
