package monitor

import (
	"context"
	"encoding/json"
	"fmt"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/oramacli"
)

// Parse decodes `orama monitor report --json` output.
func Parse(raw []byte) (*Report, error) {
	var r Report
	if err := json.Unmarshal(raw, &r); err != nil {
		return nil, fmt.Errorf("failed to parse the monitor report: %w", err)
	}
	return &r, nil
}

// Get runs `orama monitor report --env <env> --json` and decodes it. A
// non-zero exit is an error carrying the CLI's stderr: use it inside
// eventually loops, where a failed read is an observation, not the end.
func Get(ctx context.Context, cli *oramacli.Runner, env string) (*Report, error) {
	res, err := cli.Run(ctx, "monitor", "report", "--env", env, "--json")
	if err != nil {
		return nil, err
	}
	if res.Exit != 0 {
		return nil, fmt.Errorf("orama monitor report --env %s exited %d: %s", env, res.Exit, res.Stderr)
	}
	return Parse([]byte(res.Stdout))
}

// Fetch is Get that fails the test when the report cannot be read.
func Fetch(t testing.TB, cli *oramacli.Runner, env string) *Report {
	t.Helper()
	r, err := Get(t.Context(), cli.For(t), env)
	if err != nil {
		t.Fatal(cli.Recorder.Redactor().Redact(err.Error()))
	}
	return r
}
