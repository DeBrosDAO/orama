package privhelper

import (
	"fmt"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/deploysecrets"
)

// ToolDeploy stores a tenant deployment's environment and token where its
// unit reads them (pkg/deploysecrets): a root-only directory the gateway can
// no longer plant symlinks in.
const ToolDeploy = "deploy"

// Deploy operations; <instance> is the unit instance (%i).
const (
	deploySetEnv   = "set-env"   // set-env <instance>, the env file on input
	deploySetToken = "set-token" // set-token <instance>, the token on input
	deployClear    = "clear"     // clear <instance>
)

// MaxDeploySecretBytes caps one staged file: a deployment's environment or its
// workload token. Real ones are a few KiB (a token is under 1 KiB, and an
// environment value is capped at 64 KiB by the gateway); the cap is well above
// them and far below what a request may carry, so a runaway caller cannot fill
// the root-owned directory the units read from.
const MaxDeploySecretBytes = 256 << 10

// CheckDeployInput refuses a set-env or set-token payload over
// MaxDeploySecretBytes. The helper runs it on every request, whichever way the
// request arrived.
func CheckDeployInput(op string, input []byte) error {
	if (op == deploySetEnv || op == deploySetToken) && len(input) > MaxDeploySecretBytes {
		return fmt.Errorf("deploy %s: input is %d bytes; the limit is %d", op, len(input), MaxDeploySecretBytes)
	}
	return nil
}

func validateDeploy(args []string) error {
	if len(args) != 2 {
		return fmt.Errorf("deploy takes an operation and an instance")
	}
	switch args[0] {
	case deploySetEnv, deploySetToken, deployClear:
	default:
		return fmt.Errorf("deploy %q is not allowed", args[0])
	}
	if !deploysecrets.ValidInstance(args[1]) {
		return fmt.Errorf("deployment instance %q is not valid", args[1])
	}
	return nil
}

// SetDeploymentEnv stores instance's environment file through the helper.
func SetDeploymentEnv(instance, contents string) error {
	return runDeploy(deploySetEnv, instance, contents)
}

// SetDeploymentToken stores instance's workload token through the helper.
func SetDeploymentToken(instance, token string) error {
	return runDeploy(deploySetToken, instance, token)
}

// ClearDeployment removes instance's environment file and token.
func ClearDeployment(instance string) error {
	return runDeploy(deployClear, instance, "")
}

func runDeploy(op, instance, input string) error {
	cmd := Command(ToolDeploy, op, instance)
	if input != "" {
		cmd.Stdin = strings.NewReader(input)
	}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("deploy %s %s: %w: %s", op, instance, err, strings.TrimSpace(string(out)))
	}
	return nil
}
