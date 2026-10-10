package globalcmd

import (
	"bytes"
	"strings"
	"testing"

	"github.com/spf13/pflag"

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

func resetEditFlags(t *testing.T) {
	t.Helper()
	nodeEditCmd.Flags().VisitAll(func(f *pflag.Flag) {
		_ = f.Value.Set(f.DefValue)
		f.Changed = false
	})
}

func TestNodeEdit_storageOutsideItsBoundsIsAUsageErrorBeforeAnythingIsTouched(t *testing.T) {
	for _, v := range []string{"0", "1000001", "18446744073709551615"} {
		t.Cleanup(func() { resetEditFlags(t) })
		resetEditFlags(t)
		if err := nodeEditCmd.Flags().Set(storageFlag, v); err != nil {
			t.Fatal(err)
		}

		err := nodeEditCmd.RunE(nodeEditCmd, nil)

		if clierr.CodeOf(err) != clierr.CodeUsage || !strings.Contains(err.Error(), "between 1 and 1000000 GB") {
			t.Errorf("--storage-gb %s: err = %v, want a usage error naming the bounds (the byte count would overflow)", v, err)
		}
	}
}

func TestExitWord(t *testing.T) {
	if exitWord(true) != "an exit" || exitWord(false) != "a plain relay" {
		t.Error("exitWord names the wrong role")
	}
}
