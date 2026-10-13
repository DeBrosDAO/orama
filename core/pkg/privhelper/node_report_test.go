package privhelper

import (
	"context"
	"os"
	"strings"
	"testing"
)

func TestValidate_nodeReportTakesNothing(t *testing.T) {
	inv, err := Validate([]string{ToolNodeReport})
	if err != nil {
		t.Fatalf("node-report refused: %v", err)
	}
	if inv.Tool != ToolNodeReport || len(inv.Args) != 0 {
		t.Errorf("validated into %+v", inv)
	}
	if inv.NeedsInput() {
		t.Error("node-report must not read input")
	}
}

func TestValidate_nodeReportRefusesArguments(t *testing.T) {
	for _, argv := range [][]string{
		{ToolNodeReport, "--json"},
		{ToolNodeReport, ""},
		{ToolNodeReport, "compact", "extra"},
	} {
		if inv, err := Validate(argv); err == nil {
			t.Errorf("%q must be refused, validated into %+v", argv, inv)
		}
	}
}

func TestValidate_unknownToolListsNodeReport(t *testing.T) {
	_, err := Validate([]string{"bash"})
	if err == nil || !strings.Contains(err.Error(), ToolNodeReport) {
		t.Errorf("refusal must list %s among the allowed tools, got %v", ToolNodeReport, err)
	}
}

func TestAuthorize_nodeReport(t *testing.T) {
	inv := mustValidate(t, ToolNodeReport)
	for _, unit := range []string{NodeUnit, IndexGatewayUnit} {
		if err := Authorize(Caller{UID: oramaUID, Unit: unit}, inv); err != nil {
			t.Errorf("%s refused node-report: %v", unit, err)
		}
	}
	for _, unit := range []string{
		"orama-namespace-gateway@alice.service",
		"orama-deploy-node@alice-web.service",
		"",
	} {
		if err := Authorize(Caller{UID: oramaUID, Unit: unit}, inv); err == nil {
			t.Errorf("%q was allowed node-report", unit)
		}
	}
}

// The cluster gateway's grant is an explicit list: a tool nobody granted it is
// refused rather than falling through to an allow.
func TestAuthorizeIndexGateway_refusesAnUngrantedTool(t *testing.T) {
	err := authorizeIndexGateway(Invocation{Tool: "future-tool"})
	if err == nil {
		t.Fatal("an ungranted tool was allowed")
	}
}

func TestCommandContext_unprivilegedCallerGoesThroughTheSocketClient(t *testing.T) {
	if os.Geteuid() == 0 {
		t.Skip("running as root: the helper runs the tool in-process by design")
	}
	cmd := CommandContext(context.Background(), ToolNodeReport)
	if want := Path + " call " + ToolNodeReport; strings.Join(cmd.Args, " ") != want {
		t.Errorf("CommandContext args = %q, want %q", strings.Join(cmd.Args, " "), want)
	}
}

// Outside a node the helper binary is absent: the error must say what to
// check instead of surfacing a bare exec failure.
func TestNodeReport_helperMissingIsActionable(t *testing.T) {
	_, err := NodeReport(context.Background())
	if err == nil {
		t.Skip(Path + " is installed here")
	}
	if !strings.Contains(err.Error(), SocketUnitName) || !strings.Contains(err.Error(), Path) {
		t.Errorf("error does not name the helper and its socket: %v", err)
	}
}

func TestNodeReport_cancelledContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := NodeReport(ctx); err == nil {
		t.Error("a cancelled context must fail the call")
	}
}
