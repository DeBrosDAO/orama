package main

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/DeBrosOfficial/network/e2e/harness/config"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
)

// refuseStagenetEnv refuses cmd, which provisions, changes or destroys
// servers or cloud resources, when E2E_FLEET_STATE names a stagenet state:
// the existing stagenet cluster is only ever tested. The state is read
// without the run guards on purpose (it is only asked what its target is);
// a state that cannot be read is not a stagenet state, and cmd's own loading
// refuses it or never looks at it.
func refuseStagenetEnv(cmd string) error {
	path := strings.TrimSpace(os.Getenv(config.EnvState))
	if path == "" {
		return nil
	}
	st, err := fleet.Load(path)
	if err != nil {
		return nil
	}
	return refuseStagenetState(cmd, st)
}

// refuseStagenetState is the refusal for a loaded state.
func refuseStagenetState(cmd string, st *fleet.State) error {
	if st.IsStagenet() {
		return fmt.Errorf("`e2e-fleet %s` is not available on the stagenet target: the existing stagenet cluster is only tested (`e2e-fleet test --stage N`), never provisioned, changed, swept or destroyed", cmd)
	}
	return nil
}

// signInStagenetOperator makes the operator's wallet session, in the
// namespace the operator owns, the stagenet HOME's current credential, as
// provisioning does for a fleet it builds. Tests create namespaces and call
// the operator routes as the operator: the gateway refuses the first to any
// credential but a wallet session (a service account the owner's own work
// left current: "creating a namespace requires a signed-in wallet"), and the
// second to a session holding no admin grant (the lobby).
func signInStagenetOperator(ctx context.Context, st *fleet.State) error {
	cli := oramacli.ForState(st, nil)
	if err := cli.Check(); err != nil {
		return err
	}
	res, err := cli.Run(ctx, "auth", "login", "--namespace", st.OperatorNamespace)
	if err != nil {
		return fmt.Errorf("failed to sign the operator in to stagenet: %w", err)
	}
	if res.Exit != 0 {
		return fmt.Errorf("orama auth login exited %d signing the operator in to stagenet (is the dev RootWallet agent running? ~/orama-stagenet-handoff/rwdev.sh): %s", res.Exit, strings.TrimSpace(res.Stderr))
	}
	return nil
}
