package globalcmd

import (
	"errors"
	"fmt"
	"testing"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"
	"github.com/DeBrosOfficial/network/pkg/tornet"
)

// `orama setup --create-network` waits out the refusal to restart a directory
// authority too soon by its exit code, so the refusal has one and nothing else does.
func TestLifecycleError_theRollRefusalHasItsOwnExitCode(t *testing.T) {
	refusal := fmt.Errorf("the directory authority was not stopped (--force overrides): %w",
		fmt.Errorf("%w (an authority casts no Running vote for 30m0s after it starts): AuthB published its descriptor 4m ago", tornet.ErrAuthorityRoll))
	err := lifecycleError(refusal)
	if clierr.CodeOf(err) != clierr.CodeNotYet || !errors.Is(err, tornet.ErrAuthorityRoll) {
		t.Fatalf("code %d (%v), want %d wrapping tornet.ErrAuthorityRoll", clierr.CodeOf(err), err, clierr.CodeNotYet)
	}
	for name, other := range map[string]error{
		"a check that could not judge": errors.New("cannot tell whether the other authorities are voting: this authority holds no valid consensus"),
		"a unit that failed":           errors.New("stop orama-global-tor-dirauth.service: exit status 1"),
		"text that only looks like it": fmt.Errorf("%s, said the log", tornet.AuthorityRollRefusal),
	} {
		if code := clierr.CodeOf(lifecycleError(other)); code != clierr.CodeFailure {
			t.Errorf("%s: exit code %d, want %d", name, code, clierr.CodeFailure)
		}
	}
}
