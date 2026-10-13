package globalcmd

import (
	"bytes"
	"encoding/hex"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/clusterreg"
	"github.com/DeBrosOfficial/network/pkg/globalbind"
)

const (
	testOperator = "orama19rl4cm2hmr8afy4kldpxz3fka4jguq0a5tup0s"
	testChainID  = "orama-test-1"
)

func writeBinding(t *testing.T) string {
	t.Helper()
	secret := bytes.Repeat([]byte{0x11}, 32)
	b, err := globalbind.SignSecp256k1(secret, testChainID, testOperator, "provider")
	if err != nil {
		t.Fatal(err)
	}
	doc, err := json.Marshal(map[string]string{
		"service": b.Service, "key_type": b.KeyType, "pubkey": hex.EncodeToString(b.Pubkey), "signature": hex.EncodeToString(b.Signature),
	})
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(t.TempDir(), "provider.json")
	if err := os.WriteFile(path, doc, 0o600); err != nil {
		t.Fatal(err)
	}
	return path
}

// writeHotKeyBinding writes the hot key's proof of possession and returns the
// file and the hot key's address.
func writeHotKeyBinding(t *testing.T) (path, hot string) {
	t.Helper()
	b, err := globalbind.SignSecp256k1(bytes.Repeat([]byte{0x22}, 32), testChainID, testOperator, clusterreg.HotKeyService)
	if err != nil {
		t.Fatal(err)
	}
	hot, err = clusterreg.AccountAddressOf(b.Pubkey)
	if err != nil {
		t.Fatal(err)
	}
	doc, err := json.Marshal(map[string]string{
		"service": b.Service, "key_type": b.KeyType, "pubkey": hex.EncodeToString(b.Pubkey), "signature": hex.EncodeToString(b.Signature),
	})
	if err != nil {
		t.Fatal(err)
	}
	path = filepath.Join(t.TempDir(), "hot-key.json")
	if err := os.WriteFile(path, doc, 0o600); err != nil {
		t.Fatal(err)
	}
	return path, hot
}

// registerSignDoc runs `register` without --node and returns the printed sign document.
func registerSignDoc(t *testing.T, binding string, asn uint32) (string, error) {
	t.Helper()
	hotBinding, hot := writeHotKeyBinding(t)
	nodeFlags.chainID, nodeFlags.operator, nodeFlags.id, nodeFlags.hotKey = testChainID, testOperator, "node-a", hot
	nodeFlags.roles, nodeFlags.bindings, nodeFlags.ends, nodeFlags.region = []string{"storage"}, []string{binding, hotBinding}, nil, ""
	nodeFlags.node, nodeFlags.fee, nodeFlags.gas, nodeFlags.pubKey = "", "1000", 200000, hex.EncodeToString(bytes.Repeat([]byte{0x02}, 33))
	nodeFlags.asn = asn
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout := os.Stdout
	os.Stdout = w
	runErr := runRegisterNode(registerNodeCmd, nil)
	os.Stdout = stdout
	if err := w.Close(); err != nil {
		t.Fatal(err)
	}
	out, err := io.ReadAll(r)
	if err != nil {
		t.Fatal(err)
	}
	return string(out), runErr
}

func TestRegisterFlags_asnIsAnOptionalUint32(t *testing.T) {
	f := registerNodeCmd.Flags().Lookup("asn")
	if f == nil || f.DefValue != "0" || f.Value.Type() != "uint32" {
		t.Fatalf("--asn = %+v, want an optional uint32 defaulting to 0", f)
	}
	if err := registerNodeCmd.Flags().Set("asn", "-1"); err == nil {
		t.Error("a negative asn was accepted")
	}
	if err := registerNodeCmd.Flags().Set("asn", "4294967296"); err == nil {
		t.Error("an asn over 32 bits was accepted")
	}
	if err := registerNodeCmd.Flags().Set("asn", "0"); err != nil {
		t.Fatal(err)
	}
}

func TestRegister_asnIsPartOfTheSignedMessage(t *testing.T) {
	binding := writeBinding(t)
	with, err := registerSignDoc(t, binding, 15169)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(with, "40c176") {
		t.Errorf("the sign document does not carry field 8 = 15169:\n%s", with)
	}
	without, err := registerSignDoc(t, binding, 0)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(without, "40c176") || with == without {
		t.Error("an undeclared asn changed the message")
	}
}

func TestRegister_reservedAsnIsAUsageErrorBeforeAnySigning(t *testing.T) {
	binding := writeBinding(t)
	for _, asn := range []uint32{23456, 64500, 64512, 65545, 4200000000} {
		out, err := registerSignDoc(t, binding, asn)
		if err == nil || clierr.CodeOf(err) != clierr.CodeUsage {
			t.Errorf("asn %d: err = %v, want a usage error", asn, err)
		}
		if out != "" {
			t.Errorf("asn %d produced a sign document", asn)
		}
	}
}
