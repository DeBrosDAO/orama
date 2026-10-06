package privhelper

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"strings"
)

// ToolNodeReport collects this node's health report (pkg/telemetry/report) as
// root and returns it as JSON. The collectors read journals, ss -p, ufw and
// wg, which the orama user cannot; the cluster gateway serves the report and
// runs as orama, so it asks the helper. It takes no arguments and no input:
// there is nothing for a caller to steer.
const ToolNodeReport = "node-report"

func validateNodeReport(args []string) error {
	if len(args) != 0 {
		return fmt.Errorf("node-report takes no arguments, got %q", args)
	}
	return nil
}

// NodeReport collects this node's health report through the helper and
// returns its JSON. ctx bounds the whole call; cancelling it kills the client.
func NodeReport(ctx context.Context) ([]byte, error) {
	cmd := CommandContext(ctx, ToolNodeReport)
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	if err := cmd.Run(); err != nil {
		detail := strings.TrimSpace(stderr.String() + " " + stdout.String())
		return nil, fmt.Errorf("collect the node report through %s (check %s is running): %w: %s",
			Path, SocketUnitName, err, detail)
	}
	out := bytes.TrimSpace(stdout.Bytes())
	if !json.Valid(out) {
		return nil, fmt.Errorf("the node report from %s is not JSON (%d bytes)", Path, len(out))
	}
	return out, nil
}
