package node

// purgeOrphanedNamespaceRecordsSQL removes per-namespace DNS records whose
// namespace is no longer in `namespaces`: the gateway host and its wildcard
// (`namespace:<name>`), TURN (`namespace-turn:<name>`) and stealth TURN
// (`namespace-turn-stealth:<name>`).
//
// Why this exists: records were written when a namespace's cluster came up and
// only removed by DeprovisionCluster. A provision that failed and rolled back
// never removed them, the failed cluster row was later deleted by the retry
// path, and a node mid-spawn could write one after the delete. Nothing that knew
// the namespace was left to remove them, so `*.ns-<name>.<base>` kept resolving
// for namespaces that were gone. The records are keyed on the namespace name and
// the namespaces table is the registry of what exists, so this reconciles the
// one against the other and heals rows leaked before the writers were fixed.
//
// Safety:
//   - Only the three tag prefixes above are touched. `system` records (base
//     wildcard, nameserver glue, NS/SOA), deployment and domain records carry
//     other tags and never match.
//   - The whole rule is one statement, so a namespace created concurrently is
//     either visible in `namespaces` (kept) or was never provisioned. The
//     namespace row is written before its cluster is provisioned, so a live
//     namespace's records are never candidates.
//   - Idempotent and unguarded by node role, like the other sweeps: every node
//     may run it, and old nodes that do not simply leave the rows to a new one.
//     A failed statement changes nothing.
const purgeOrphanedNamespaceRecordsSQL = `DELETE FROM dns_records
	 WHERE (namespace LIKE 'namespace:%'
	     OR namespace LIKE 'namespace-turn:%'
	     OR namespace LIKE 'namespace-turn-stealth:%')
	   AND NOT EXISTS (
	       SELECT 1 FROM namespaces ns
	        WHERE dns_records.namespace IN ('namespace:'||ns.name,
	                                        'namespace-turn:'||ns.name,
	                                        'namespace-turn-stealth:'||ns.name)
	   )`
