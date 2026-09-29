package main

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestShellQuote_neutralisesShellSyntax(t *testing.T) {
	for in, want := range map[string]string{
		"plain":       `'plain'`,
		"a b":         `'a b'`,
		"it's":        `'it'\''s'`,
		"$(rm -rf /)": `'$(rm -rf /)'`,
		"a;b`c`":      "'a;b`c`'",
		"":            `''`,
	} {
		require.Equal(t, want, shellQuote(in))
	}
}

func TestInNetns_quotesEveryArgument(t *testing.T) {
	got := inNetns("python3", "-c", "print('x')", "31001")
	require.Equal(t, `sudo ip netns exec orama-global 'python3' '-c' 'print('\''x'\'')' '31001'`, got)
}

func TestAgentCommand_pipesTheKeyOnTheNodeAndNeverPrintsIt(t *testing.T) {
	cmd := agentCommand()
	require.True(t, strings.HasPrefix(cmd, "sudo sh -c '"))
	require.Contains(t, cmd, "keys")
	require.Contains(t, cmd, "--unarmored-hex")
	require.Contains(t, cmd, "| "+nodeHelper+" agent", "the export feeds the agent on the same host")
	require.Contains(t, cmd, agentSock+":0", "root's own socket, for `orama` run on the node")
	require.Contains(t, cmd, agentFwdSock)
}

func TestBridgeScript_isSelfContainedPython(t *testing.T) {
	require.Contains(t, bridgeScript, "socket.create_connection")
	require.Contains(t, bridgeScript, "127.0.0.1", "it only ever dials the namespace's own loopback")
	require.NotContains(t, bridgeScript, "argv[2]")
}

func TestAbbreviate(t *testing.T) {
	require.Equal(t, "short", abbreviate("short"))
	require.Len(t, abbreviate(strings.Repeat("x", 200)), 83)
}
