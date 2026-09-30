package provision

import (
	"context"
	"fmt"
	"strings"

	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// LiveExtras are the run's servers that exist in Hetzner but not in st:
// extras and eval cluster servers the broker created for feature processes
// (which never write the shared state) and that are still up, so the
// runner can collect their artifacts too. Each carries its fleet name
// (the server name without e2e-<run>-), address and SSH user.
func LiveExtras(ctx context.Context, st *fleet.State) ([]fleet.Node, error) {
	if err := refuseStagenet("LiveExtras", st); err != nil {
		return nil, err
	}

	d, err := depsFromEnv()
	if err != nil {
		return nil, err
	}
	return liveExtras(ctx, st, d)
}

func liveExtras(ctx context.Context, st *fleet.State, d deps) ([]fleet.Node, error) {
	servers, err := d.cloud.ListServers(ctx, runSelector(st.RunID))
	if err != nil {
		return nil, fmt.Errorf("failed to list the servers of run %s: %w", st.RunID, err)
	}
	known := map[int64]bool{}
	for _, list := range [][]fleet.Node{st.Nodes, st.Extras, st.Probes} {
		for _, n := range list {
			known[n.ServerID] = true
		}
	}
	prefix := serverName(st.RunID, "")
	var out []fleet.Node
	for _, s := range servers {
		name, ok := strings.CutPrefix(s.Name, prefix)
		if known[s.ID] || !ok || s.IPv4() == "" {
			continue
		}
		out = append(out, fleet.Node{Name: name, Role: fleet.RoleNode, SSHUser: sshUser, ServerID: s.ID, PublicIP: s.IPv4()})
	}
	return out, nil
}
