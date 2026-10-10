package remotessh

import "encoding/base64"

// ScriptCommand is the ssh command that runs script as root (through sudo for a
// login that is not root) on the node. The script travels base64-encoded and is
// decoded by the node's own shell into `bash -s`, so no quoting of the script
// survives to be got wrong, and nothing in it appears in the node's process list.
// The shell has to decode it: piping the encoded text to bash would run the
// base64 itself as a command.
func ScriptCommand(sudo, script string) string {
	return "printf %s " + base64.StdEncoding.EncodeToString([]byte(script)) + " | base64 -d | " + sudo + "bash -s"
}
