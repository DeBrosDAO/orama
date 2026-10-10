package globalcmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
)

func TestNodeEdit_needsASetting(t *testing.T) {
	var out bytes.Buffer
	nodeEditCmd.SetOut(&out)
	nodeEditCmd.SetErr(&out)

	err := nodeEditCmd.RunE(nodeEditCmd, nil)

	if clierr.CodeOf(err) != clierr.CodeUsage || !strings.Contains(err.Error(), "--storage-gb") || !strings.Contains(err.Error(), "--exit") {
		t.Fatalf("err = %v, want a usage error naming both settings", err)
	}
}

func TestExitWord(t *testing.T) {
	if exitWord(true) != "an exit" || exitWord(false) != "a plain relay" {
		t.Error("exitWord names the wrong role")
	}
}
