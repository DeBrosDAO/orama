package privhelper

import (
	"bytes"
	"fmt"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/gatewaykeys"
)

// ToolGatewayKey stores the index gateway's signing keys in the root-only
// tree pkg/gatewaykeys describes.
const ToolGatewayKey = "gateway-key"

const gatewayKeyPut = "put" // put <filename>, the PEM on input

func validateGatewayKey(args []string) error {
	if len(args) == 2 && args[0] == gatewayKeyPut && gatewaykeys.ValidName(args[1]) {
		return nil
	}
	return fmt.Errorf("gateway-key %q is not allowed", args)
}

// PutGatewayKey stores pem as the index gateway's key named name.
func PutGatewayKey(name string, pem []byte) error {
	if !gatewaykeys.ValidName(name) {
		return fmt.Errorf("store the index gateway's %s: not a signing key", name)
	}
	cmd := Command(ToolGatewayKey, gatewayKeyPut, name)
	cmd.Stdin = bytes.NewReader(pem)
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("store the index gateway's %s: %w: %s", name, err, strings.TrimSpace(string(out)))
	}
	return nil
}
