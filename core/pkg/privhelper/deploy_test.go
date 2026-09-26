package privhelper

import (
	"strings"
	"testing"
)

func TestCheckDeployInput_CapsEnvAndToken(t *testing.T) {
	for _, op := range []string{deploySetEnv, deploySetToken} {
		if err := CheckDeployInput(op, make([]byte, MaxDeploySecretBytes)); err != nil {
			t.Errorf("%s at the limit refused: %v", op, err)
		}
		err := CheckDeployInput(op, make([]byte, MaxDeploySecretBytes+1))
		if err == nil || !strings.Contains(err.Error(), "limit") {
			t.Errorf("%s over the limit: got %v, want a refusal naming the limit", op, err)
		}
	}
}

func TestCheckDeployInput_EmptyAndClear(t *testing.T) {
	if err := CheckDeployInput(deploySetEnv, nil); err != nil {
		t.Errorf("an empty environment is a valid one: %v", err)
	}
	if err := CheckDeployInput(deployClear, nil); err != nil {
		t.Errorf("clear takes no input: %v", err)
	}
}

// The cap has to sit below what one request may carry, or the request limit
// refuses first with a less useful error.
func TestMaxDeploySecretBytes_BelowRequestLimit(t *testing.T) {
	if MaxDeploySecretBytes >= MaxRequestBytes {
		t.Fatalf("MaxDeploySecretBytes %d is not below MaxRequestBytes %d", MaxDeploySecretBytes, MaxRequestBytes)
	}
}
