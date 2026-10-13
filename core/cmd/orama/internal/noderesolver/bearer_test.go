package noderesolver

import (
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
)

// Bug: node resolution and `orama monitor` read only the stored session, so
// ORAMA_TOKEN was ignored by them.
func TestLoadBearer_honoursORAMATOKEN(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	const jwt = "eyJhbGciOiJFZERTQSJ9.eyJzdWIiOiIweG93bmVyIn0.c2ln"
	t.Setenv("ORAMA_TOKEN", jwt)

	got, err := LoadBearer("https://gateway.example")
	if err != nil || got != jwt {
		t.Fatalf("LoadBearer = %q, %v; want ORAMA_TOKEN", got, err)
	}
}

func TestLoadBearer_noCredentialIsTheAuthExit(t *testing.T) {
	t.Setenv("HOME", t.TempDir())
	t.Setenv("ORAMA_TOKEN", "")

	_, err := LoadBearer("https://gateway.example")
	if got := clierr.CodeOf(err); got != clierr.CodeAuth || !strings.Contains(err.Error(), "orama auth login") {
		t.Fatalf("exit %d, %v; want %d and the login hint", got, err, clierr.CodeAuth)
	}
}
