package provision

import (
	"context"
	"fmt"
	"strings"

	"github.com/DeBrosOfficial/network/e2e/harness/hetzner"
)

// deleteOwnedServer deletes server id only once it has read it back and
// found it labelled e2e-run=runID and named e2e-<runID>-... (exactly
// wantName when that is set): a server id from a stale or edited state file
// can never take another run's server, or anything else in the project. A
// server that is already gone is not an error.
func deleteOwnedServer(ctx context.Context, d deps, runID string, id int64, wantName string) error {
	s, err := d.cloud.GetServer(ctx, id)
	if hetzner.IsNotFound(err) {
		return nil
	}
	if err != nil {
		return fmt.Errorf("failed to read server %d before deleting it: %w", id, err)
	}
	prefix := serverName(runID, "")
	if s.Labels[hetzner.LabelRun] != runID || !strings.HasPrefix(s.Name, prefix) || (wantName != "" && s.Name != wantName) {
		return fmt.Errorf("refusing to delete server %d (%q, e2e-run=%q): it is not %s of run %s",
			id, s.Name, s.Labels[hetzner.LabelRun], ownedDescription(prefix, wantName), runID)
	}
	if err := d.cloud.DeleteServer(ctx, id); err != nil {
		return fmt.Errorf("failed to delete server %d (%s): %w", id, s.Name, err)
	}
	return nil
}

func ownedDescription(prefix, wantName string) string {
	if wantName != "" {
		return wantName
	}
	return "a server named " + prefix + "..."
}
