package install

import (
	"errors"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/constants"
)

func TestMigrateIndexerLayout_runsTheInstalledBinaryOnTheIndexersHome(t *testing.T) {
	var name string
	var args []string
	h := GlobalHost{BinDir: "/usr/lib/orama-global/bin", Run: func(n string, a ...string) ([]byte, error) {
		name, args = n, a
		return []byte("  indexer: moved it aside\n"), nil
	}}

	said, err := MigrateIndexerLayout(h)

	if err != nil || said != "indexer: moved it aside" {
		t.Fatalf("said %q, err %v", said, err)
	}
	want := "-u " + globalIndexerUser + " -- /usr/lib/orama-global/bin/orama-global indexer migrate --home " + constants.GlobalIndexerHome
	if name != "runuser" || strings.Join(args, " ") != want {
		t.Fatalf("ran %s %v, want runuser %s: the migration must run as the indexer's account, not as root", name, args, want)
	}
}

func TestMigrateIndexerLayout_aFailureCarriesWhatTheCommandSaid(t *testing.T) {
	h := GlobalHost{BinDir: "/b", Run: func(string, ...string) ([]byte, error) {
		return []byte("the index in /x has layout version 4, newer than the 3 this indexer writes"), errors.New("exit status 1")
	}}

	said, err := MigrateIndexerLayout(h)

	if err == nil || !strings.Contains(err.Error(), "layout version 4") || !strings.Contains(err.Error(), "indexer migrate") {
		t.Fatalf("err = %v", err)
	}
	if !strings.Contains(said, "layout version 4") {
		t.Fatalf("said %q", said)
	}
}
