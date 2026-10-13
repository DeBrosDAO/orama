package inspector

import (
	"encoding/json"
	"strings"
)

// namespaceRegistrySQL reads, for every namespace, its registry status and the
// seconds it has been in that status: the teardown stamp for a namespace being
// deleted, the provisioning stamp otherwise. The registry's own clock gives
// both ends, so a node's skewed clock cannot make a namespace look old.
const namespaceRegistrySQL = `SELECT namespace_name, status, ` +
	`CAST(strftime('%s','now') - strftime('%s', CASE status WHEN 'deprovisioning' THEN deprovisioning_at ELSE provisioned_at END) AS INTEGER) ` +
	`FROM namespace_clusters`

// namespaceRegistryCurlOpts is the curl options that post namespaceRegistrySQL
// to the node's rqlite, shell-quoted: the SQL has single quotes of its own, so
// each is closed, escaped and reopened. Quoting the SQL by hand once stripped
// them all, and the registry answered a syntax error on every node.
func namespaceRegistryCurlOpts() string {
	body, err := json.Marshal([][]string{{namespaceRegistrySQL}})
	if err != nil {
		panic("inspector: the namespace registry query is not JSON: " + err.Error())
	}
	return "-sf -H 'Content-Type: application/json' -d '" + strings.ReplaceAll(string(body), "'", `'\''`) + "'"
}
