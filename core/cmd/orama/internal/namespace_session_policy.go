package cli

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/cmd/orama/internal/printer"
)

// sessionPolicyPath is the route that reads and sets a namespace's session
// policy: who may sign in to it, and what its sessions must bind.
const sessionPolicyPath = "/v1/namespace/session-policy"

// NamespaceSessionPolicy shows a namespace's session policy, or sets the fields
// the caller passed. A field left empty is left as the gateway has it, so
// `--sign-in open` changes nothing about devices. Neither field is checked
// here: the gateway validates both before writing either and says which value
// it refused, and a second copy of its list would be the one that drifts.
func NamespaceSessionPolicy(out *printer.Printer, ns, signIn, devicePolicy string) error {
	gatewayURL, token, err := loadAuthForNamespace(ns)
	if err != nil {
		return err
	}

	fields, err := sessionPolicyFields(signIn, devicePolicy)
	if err != nil {
		return err
	}
	method, what := http.MethodGet, "read the session policy"
	var body io.Reader
	if fields != nil {
		method, what, body = http.MethodPut, "set the session policy", bytes.NewReader(fields)
	}
	result, err := nsRequest(what, method, gatewayURL+sessionPolicyPath, token, body)
	if err != nil {
		return err
	}
	if out.JSONMode() {
		return out.JSON(result)
	}
	fmt.Fprintf(out.Out(), "Session policy for namespace '%v'\n\n", result["namespace"])
	fmt.Fprintf(out.Out(), "  Sign-in:        %v\n", result["sign_in"])
	fmt.Fprintf(out.Out(), "  Device policy:  %v\n", result["device_policy"])
	if revoked, ok := result["revoked_sign_in_keys"].(float64); ok && revoked > 0 {
		fmt.Fprintf(out.Out(), "\n  Revoked %.0f end-user sign-in keys: requiring devices ends the keys a sign-in minted.\n", revoked)
	}
	return nil
}

// sessionPolicyFields is the body of a PUT carrying only the fields the caller
// named, or nil when it named none and the command reads instead.
func sessionPolicyFields(signIn, devicePolicy string) ([]byte, error) {
	fields := map[string]string{}
	if signIn != "" {
		fields["sign_in"] = signIn
	}
	if devicePolicy != "" {
		fields["device_policy"] = devicePolicy
	}
	if len(fields) == 0 {
		return nil, nil
	}
	raw, err := json.Marshal(fields)
	if err != nil {
		return nil, clierr.Failure("failed to encode the session policy: %w", err)
	}
	return raw, nil
}
