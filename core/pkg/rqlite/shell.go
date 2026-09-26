package rqlite

import (
	"strings"

	"github.com/DeBrosOfficial/network/pkg/config"
)

// Remote rqlite calls.
//
// Operator commands (recover-raft, cluster ops, the inspector, rollout probes,
// sandbox) reach a node's rqlite by running curl ON the node over SSH, so the
// operator's machine does not have to be on the WireGuard overlay. rqlited
// binds only the node's WireGuard IP and requires basic auth, and the
// operator's machine knows neither that IP nor the cluster's rqlite password —
// the node does, in its node.yaml. These commands therefore read the same
// node.yaml fields EndpointFromNodeConfig reads (discovery.http_adv_address,
// database.rqlite_port, database.rqlite_username, database.rqlite_password) on
// the node itself.
//
// The credentials come only from node.yaml, which this release's installer
// writes (database.rqlite_username / rqlite_password, beside rqlite_auth_file).
// A node.yaml with none of the three is one written before rqlite
// authentication (0.122.x), whose node started the index rqlited without
// -auth: the index is called without credentials, as EndpointFromNodeConfig
// does (preAuthConfig). Any other gap — an auth file without credentials, a
// user without a password — fails with "rqlite address or credentials
// missing". Namespace instances always need the credentials.
//
// The password reaches curl on stdin as a curl config line (-K -), never on a
// command line where ps could show it.

// NodeShellCurl returns a shell command that, run on a node, calls that node's
// index rqlite at path ("/status", "/db/query?level=strong") with curlOpts
// (already shell-quoted by the caller). sudo is the privilege prefix for
// reading node.yaml ("sudo " or "" for root). The command is a subshell, so it
// composes with pipes and ||. It fails with a message on stderr when node.yaml
// lacks the address or the credentials.
func NodeShellCurl(sudo, curlOpts, path string) string {
	return indexShellCurl(config.ProductionNodeConfigPath, sudo, curlOpts, path)
}

// NodeShellCurlAt is NodeShellCurl for an rqlite other than the index — a
// namespace instance — at addrExpr, a shell expression that expands to its
// host:port on the node (e.g. "$ADDR" read from its rqlite.env HTTP_ADDR).
// Namespace instances use the cluster-wide credentials from node.yaml.
func NodeShellCurlAt(sudo, addrExpr, curlOpts, path string) string {
	return instanceShellCurl(config.ProductionNodeConfigPath, sudo, addrExpr, curlOpts, path)
}

// indexShellCurl is NodeShellCurl against the node.yaml at nodeConfigPath. The
// host is http_adv_address without its port (what rqlited binds, see
// BindAddr), the port is rqlite_port.
func indexShellCurl(nodeConfigPath, sudo, curlOpts, path string) string {
	return shellCurl(sudo, nodeConfigPath, `"http://${_rq_a%:*}:${_rq_n}`+path+`"`, curlOpts, true)
}

// instanceShellCurl is NodeShellCurlAt against the node.yaml at nodeConfigPath.
func instanceShellCurl(nodeConfigPath, sudo, addrExpr, curlOpts, path string) string {
	return shellCurl(sudo, nodeConfigPath, `"http://`+addrExpr+path+`"`, curlOpts, false)
}

// shellCurl builds the command. url is a shell word; withIndexAddr also reads
// the index address and port from node.yaml into _rq_a / _rq_n.
func shellCurl(sudo, nodeConfigPath, url, curlOpts string, withIndexAddr bool) string {
	var b strings.Builder
	b.WriteString(`( _rq_c='` + nodeConfigPath + `'; `)
	// Prints the value of a top-level-or-nested "key: value" line, quotes
	// stripped. node.yaml is rendered from a template, one key per line.
	b.WriteString(`_rq_v() { ` + sudo + `sed -n "s/^[[:space:]]*$1:[[:space:]]*\"\{0,1\}\([^\"]*\)\"\{0,1\}[[:space:]]*\$/\1/p" "$_rq_c" | head -n 1; }; `)
	required := `[ -z "$_rq_u" ] || [ -z "$_rq_p" ]`
	if withIndexAddr {
		b.WriteString(`_rq_a=$(_rq_v http_adv_address); _rq_n=$(_rq_v rqlite_port); `)
		b.WriteString(`if [ -z "$_rq_a" ] || [ -z "$_rq_n" ]; then echo "rqlite address or credentials missing from $_rq_c" >&2; exit 1; fi; `)
	}
	b.WriteString(`_rq_u=$(_rq_v rqlite_username); _rq_p=$(_rq_v rqlite_password); `)
	authed := `printf 'user = "%s:%s"\n' "$(_rq_q "$_rq_u")" "$(_rq_q "$_rq_p")" | curl -K - ` + curlOpts + ` ` + url
	if withIndexAddr {
		// A pre-authentication node.yaml (no auth file, no credentials):
		// its index rqlited runs without -auth.
		b.WriteString(`_rq_f=$(_rq_v rqlite_auth_file); `)
		b.WriteString(`if [ -z "$_rq_f" ] && [ -z "$_rq_u" ] && [ -z "$_rq_p" ]; then curl ` + curlOpts + ` ` + url + `; exit $?; fi; `)
	}
	b.WriteString(`if ` + required + `; then echo "rqlite address or credentials missing from $_rq_c" >&2; exit 1; fi; `)
	b.WriteString(shellCurlConfigQuote)
	b.WriteString(authed + ` )`)
	return b.String()
}

// shellCurlConfigQuote defines _rq_q, which escapes a value for a
// double-quoted curl config parameter: inside "...", curl treats backslash as
// an escape character, so a literal \ or " must be written \\ or \".
// Generated rqlite passwords are hex, but a hand-set one must not change the
// credentials curl sends or break the config line.
const shellCurlConfigQuote = `_rq_q() { printf '%s' "$1" | sed 's/[\\"]/\\&/g'; }; `
