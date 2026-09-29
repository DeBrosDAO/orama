package fleet

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/DeBrosOfficial/network/e2e/harness/agent"
	"github.com/DeBrosOfficial/network/e2e/harness/config"
	"github.com/DeBrosOfficial/network/e2e/harness/secrets"
)

// CheckState applies the run guards to a state before anything acts on it:
// the CLI environment is the run's own (e2e-...), the base domain is an e2e
// name under the one allowed zone, the chain is the run's, and the agent
// socket every CLI invocation signs through is not the owner's real wallet.
// Every loader of a state runs it (the runner's commands and harness.Main),
// so a state file edited or written by something else cannot aim a run at a
// shared environment.
func CheckState(st *State, realHome string) error {
	var errs []error
	for _, err := range []error{
		config.CheckRunName("run id", st.RunID),
		config.CheckEnvName(st.Env),
		config.CheckBaseDomain(st.BaseDomain),
		config.CheckChainID(st.ChainID),
		secrets.CheckAgentSockNotRealWallet(st.RWSock, realHome),
		checkAgentLayout(st),
	} {
		if err != nil {
			errs = append(errs, err)
		}
	}
	return errors.Join(errs...)
}

// checkAgentLayout requires the CLI HOME to be a test agent directory, an
// e2e-rw-... directory directly under agent.DefaultBaseDir (links resolved),
// and the agent socket to be that directory's socket: a state cannot hand
// the CLI another HOME (the owner's, with its ~/.orama and ~/.ssh) or a
// socket beside it.
func checkAgentLayout(st *State) error {
	if st.Home == "" {
		return errors.New("the state has no CLI HOME (the test agent's directory)")
	}
	// The parent is resolved, not the directory: a teardown retried after
	// the agent directory was shredded must still pass.
	parent, err := filepath.EvalSymlinks(filepath.Dir(st.Home))
	if err != nil {
		return fmt.Errorf("the state's CLI HOME %s cannot be resolved: %w", st.Home, err)
	}
	home := filepath.Join(parent, filepath.Base(st.Home))
	base, err := filepath.EvalSymlinks(agent.DefaultBaseDir)
	if err != nil {
		return fmt.Errorf("failed to resolve %s: %w", agent.DefaultBaseDir, err)
	}
	if filepath.Dir(home) != base || !strings.HasPrefix(filepath.Base(home), agent.DirPrefix) {
		return fmt.Errorf("the state's CLI HOME %s is not a test agent directory (%s/%s...)", st.Home, agent.DefaultBaseDir, agent.DirPrefix)
	}
	if st.RWSock != agent.SockPath(st.Home) {
		return fmt.Errorf("the state's agent socket %s is not its test agent's socket %s", st.RWSock, agent.SockPath(st.Home))
	}
	return nil
}
