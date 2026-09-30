package main

import (
	"fmt"
	"os"
	"strings"

	"github.com/DeBrosOfficial/network/e2e/harness/config"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
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
