package rqlite

import "sort"

// Which database each of the platform's tables belongs in.
//
// There are two, and a gateway may hold both. The **cluster registry** is the
// index's RQLite: one per cluster, and the only place identity means anything.
// A **namespace RQLite** is one tenant's, and it is also the database that
// tenant reads, writes and can export whole — `/v1/rqlite/export` hands back the
// file, and `/v1/rqlite/import` replaces it.
//
// Every core migration used to be applied to both, stripping two tables whose
// names collided with a tenant's own (bugboard #150). So a tenant's application
// database also held `api_keys`, `grants`, `nonces`, `refresh_tokens` and the
// rest — the tables that decide who is an admin of the namespace, in the schema
// the tenant's own code writes. What guarded them was a denylist in the
// serverless SQL guard, and a denylist is a list somebody has to remember to
// add to. This is the list instead, and a test fails when a migration creates a
// table that is not on it.

// Placement says which database a table belongs in.
type Placement int

const (
	// PlacementNamespace is a table a namespace gateway reads and writes on
	// its own RQLite: the data plane it serves for one tenant.
	PlacementNamespace Placement = iota

	// PlacementCluster is a table that exists only in the cluster registry. It
	// is stripped from a namespace RQLite, so a query against one there fails
	// loudly rather than reading an empty table that looks like an answer.
	PlacementCluster
)

// tableNote is where a table lives and why.
type tableNote struct {
	Placement Placement
	Why       string
}

// tablePlacement is every table the core migrations leave behind.
//
// A table a later migration drops, or renames away as part of a rebuild, is not
// here: it does not exist once the set has been applied, and a placement for it
// would be a decision about nothing.
//
// A table is `PlacementCluster` only where the code that reads it demonstrably
// uses the registry handle — `keyORM()` in the auth service, `GlobalORMClient`
// in a handler, or a package that only ever runs on the index. Everything else
// stays where it is; moving a table a namespace gateway reads locally would
// turn a working query into "no such table", and that is a decision to make
// with evidence rather than by category.
var tablePlacement = map[string]tableNote{
	// --- identity and authorization: the cluster registry, always ---------
	//
	// These are read through the auth service's registry handle. A copy in a
	// tenant's database is a copy its subject can rewrite, and one the rest of
	// the cluster never sees.
	"api_keys":              {PlacementCluster, "a key is validated against the registry (bug-162)"},
	"wallet_api_keys":       {PlacementCluster, "which key belongs to which wallet, beside api_keys"},
	"principals":            {PlacementCluster, "who the platform will authenticate"},
	"grants":                {PlacementCluster, "who may do what in a namespace"},
	"nonces":                {PlacementCluster, "a challenge issued on one gateway is consumed on another"},
	"refresh_tokens":        {PlacementCluster, "a session must be refreshable and revocable from anywhere"},
	"revoked_tokens":        {PlacementCluster, "a revocation that reaches one gateway refuses nothing"},
	"signing_keys":          {PlacementCluster, "publishing a key is minting authority; the cluster verifies against it"},
	"node_credentials":      {PlacementCluster, "a node's own key; every gateway in the cluster verifies its stamps against this"},
	"device_authorizations": {PlacementCluster, "started on one gateway, approved on another"},
	"operators":             {PlacementCluster, "who may operate the cluster"},
	"audit_events":          {PlacementCluster, "a record its own subject could delete is not a record"},

	// --- the tenant's data plane: the namespace's own RQLite --------------
	//
	// Read through ORMClient by the handlers that serve one namespace.
	"namespaces":                  {PlacementNamespace, "the namespace's own row, which its local tables key on"},
	"apps":                        {PlacementNamespace, "the tenant's applications"},
	"deployments":                 {PlacementNamespace, "served per namespace"},
	"deployment_domains":          {PlacementNamespace, "served per namespace"},
	"deployment_events":           {PlacementNamespace, "served per namespace"},
	"deployment_health_checks":    {PlacementNamespace, "served per namespace"},
	"deployment_history":          {PlacementNamespace, "served per namespace"},
	"deployment_replicas":         {PlacementNamespace, "served per namespace"},
	"home_node_assignments":       {PlacementNamespace, "which node hosts a deployment, read while serving it"},
	"port_allocations":            {PlacementNamespace, "a deployment's ports on this namespace's nodes"},
	"functions":                   {PlacementNamespace, "served per namespace"},
	"function_cron_triggers":      {PlacementNamespace, "served per namespace"},
	"function_db_change_tracking": {PlacementNamespace, "served per namespace"},
	"function_db_triggers":        {PlacementNamespace, "served per namespace"},
	"function_env_vars":           {PlacementNamespace, "served per namespace"},
	"function_invocations":        {PlacementNamespace, "served per namespace"},
	"function_jobs":               {PlacementNamespace, "served per namespace"},
	"function_logs":               {PlacementNamespace, "served per namespace"},
	"function_pubsub_triggers":    {PlacementNamespace, "served per namespace"},
	"function_rate_limits":        {PlacementNamespace, "served per namespace"},
	"function_secrets":            {PlacementNamespace, "read on the invocation path, per namespace"},
	"function_timers":             {PlacementNamespace, "served per namespace"},
	"ipfs_content_ownership":      {PlacementNamespace, "read through ORMClient by the storage handlers"},
	"namespace_quotas":            {PlacementNamespace, "read through ORMClient by the storage handlers"},
	"namespace_sqlite_databases":  {PlacementNamespace, "the tenant's own databases, listed per namespace"},
	"namespace_sqlite_backups":    {PlacementNamespace, "beside namespace_sqlite_databases"},
	"namespace_publish_seq":       {PlacementNamespace, "per-namespace publish ordering"},
	"namespace_push_config":       {PlacementNamespace, "read on the push path, per namespace"},
	"namespace_push_credentials":  {PlacementNamespace, "read on the push path, per namespace"},
	"namespace_rate_limit_config": {PlacementNamespace, "read on every request to this namespace"},
	"namespace_webrtc_config":     {PlacementNamespace, "read on the WebRTC path, per namespace"},
	"push_devices":                {PlacementNamespace, "the tenant's devices"},
	"webrtc_rooms":                {PlacementNamespace, "the tenant's rooms"},
	"request_logs":                {PlacementNamespace, "this gateway's own request log"},
	"subscriptions":               {PlacementNamespace, "dead since 002_core; stripped separately by name collision"},
	"cluster_locks":               {PlacementNamespace, "the migration runner takes one on the database it is migrating"},
	"schema_migrations":           {PlacementNamespace, "the tenant's own tracker; core's lives in orama_schema_migrations"},

	// --- cluster control plane -------------------------------------------
	//
	// Written by the index, the node agent or the CLI, never by a namespace
	// gateway serving a request. They are left in place for now: each needs
	// its readers checked one at a time, and a table moved without that is a
	// query that stops working on a machine this cannot test. See ARCH-2.
	"invite_tokens":                {PlacementNamespace, "cluster join; unverified, see ARCH-2"},
	"wireguard_peers":              {PlacementNamespace, "mesh membership; unverified, see ARCH-2"},
	"dns_records":                  {PlacementNamespace, "cluster DNS; unverified, see ARCH-2"},
	"dns_nodes":                    {PlacementNamespace, "cluster DNS; unverified, see ARCH-2"},
	"dns_nameservers":              {PlacementNamespace, "cluster DNS; unverified, see ARCH-2"},
	"reserved_domains":             {PlacementNamespace, "cluster DNS; unverified, see ARCH-2"},
	"raft_evicted_nodes":           {PlacementNamespace, "cluster membership; unverified, see ARCH-2"},
	"node_health_events":           {PlacementNamespace, "cluster health; unverified, see ARCH-2"},
	"rqlite_backups":               {PlacementNamespace, "cluster backups; unverified, see ARCH-2"},
	"namespace_clusters":           {PlacementNamespace, "cluster topology; unverified, see ARCH-2"},
	"namespace_cluster_nodes":      {PlacementNamespace, "cluster topology; unverified, see ARCH-2"},
	"namespace_cluster_events":     {PlacementNamespace, "cluster topology; unverified, see ARCH-2"},
	"namespace_port_allocations":   {PlacementNamespace, "cluster port allocation; unverified, see ARCH-2"},
	"webrtc_port_allocations":      {PlacementNamespace, "cluster port allocation; unverified, see ARCH-2"},
	"global_deployment_subdomains": {PlacementNamespace, "subdomain ownership; unverified, see ARCH-2"},
	"namespace_pending_cleanup":    {PlacementNamespace, "cluster cleanup; unverified, see ARCH-2"},
}

// PlacementOf returns where a table belongs, and whether it is classified at
// all.
func PlacementOf(table string) (tableNote, bool) {
	note, ok := tablePlacement[table]
	return note, ok
}

// ClusterOnlyTables are the tables that exist only in the cluster registry, and
// are therefore stripped from a namespace RQLite.
func ClusterOnlyTables() []string {
	out := make([]string, 0, len(tablePlacement))
	for table, note := range tablePlacement {
		if note.Placement == PlacementCluster {
			out = append(out, table)
		}
	}
	sort.Strings(out)
	return out
}
