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

// Trust says what a row in a table is worth to the platform.
//
// Placement says where a table lives; this says whether a decision may rest on
// what a tenant could have written into it. The zero value is TrustPlatform, so
// a table entered without thinking about it is the guarded kind.
type Trust int

const (
	// TrustPlatform is a table whose rows the platform acts on: authority,
	// credentials, limits, routing, what runs where. A tenant that could write
	// one gains something the platform never granted. Tenant SQL must not be
	// able to name it (pkg/sqlguard), and a test holds the two lists together.
	TrustPlatform Trust = iota

	// TrustTenantData is the tenant's own data, or a table nothing reads. A
	// forged row gains the tenant nothing beyond what its own API gives it.
	TrustTenantData

	// TrustTelemetry is a record of what happened. No platform decision may
	// read it.
	TrustTelemetry
)

// tableNote is where a table lives, how far its rows are trusted, and why.
type tableNote struct {
	Placement Placement
	Trust     Trust
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
	"api_keys":                 {PlacementCluster, TrustPlatform, "a key is validated against the registry (bug-162)"},
	"wallet_api_keys":          {PlacementCluster, TrustPlatform, "which key belongs to which wallet, beside api_keys"},
	"principals":               {PlacementCluster, TrustPlatform, "who the platform will authenticate"},
	"grants":                   {PlacementCluster, TrustPlatform, "who may do what in a namespace"},
	"status_uptime_hourly":     {PlacementCluster, TrustPlatform, "public uptime history; written and read only by pkg/telemetry/hub, which runs on the cluster gateway"},
	"api_keys_expiry_cutoff":   {PlacementCluster, TrustPlatform, "the highest api_keys id migration 051 backfilled; the contract release revokes keys above it"},
	"namespace_ownership":      {PlacementCluster, TrustPlatform, "0.122.x's authorization, kept for the rolling window (050 is expand-only); the next release drops it"},
	"nonces":                   {PlacementCluster, TrustPlatform, "a challenge issued on one gateway is consumed on another"},
	"refresh_tokens":           {PlacementCluster, TrustPlatform, "a session must be refreshable and revocable from anywhere"},
	"revoked_tokens":           {PlacementCluster, TrustPlatform, "a revocation that reaches one gateway refuses nothing"},
	"signing_keys":             {PlacementCluster, TrustPlatform, "publishing a key is minting authority; the cluster verifies against it"},
	"node_credentials":         {PlacementCluster, TrustPlatform, "a node's own key; every gateway in the cluster verifies its stamps against this"},
	"device_authorizations":    {PlacementCluster, TrustPlatform, "started on one gateway, approved on another"},
	"session_devices":          {PlacementCluster, TrustPlatform, "which devices hold sessions; revoked on one gateway, refused on every other"},
	"namespace_session_policy": {PlacementCluster, TrustPlatform, "what a sign-in must prove; read where sessions are issued"},
	"operators":                {PlacementCluster, TrustPlatform, "who may operate the cluster"},
	"cluster_settings":         {PlacementCluster, TrustPlatform, "who may create namespaces, and how many one wallet may own; read on the index, so a copy in a tenant database is a policy the cluster never enforces"},
	"namespace_creators":       {PlacementCluster, TrustPlatform, "the allowlist for namespace creation; a tenant copy would let the tenant add themselves"},
	"audit_events":             {PlacementCluster, TrustPlatform, "a record its own subject could delete is not a record"},
	"encryption_roots":         {PlacementCluster, TrustPlatform, "the IKM stored secrets are derived from; a tenant copy would be a KEK they can rewrite"},
	"ipfs_cid_refs":            {PlacementCluster, TrustPlatform, "the cross-namespace reference count that decides whether an unpin may remove the shared cluster pin; a namespace RQLite only sees its own references"},

	// --- the tenant's data plane: the namespace's own RQLite --------------
	//
	// Read through ORMClient by the handlers that serve one namespace.
	"namespaces":                  {PlacementNamespace, TrustPlatform, "the namespace's own row, which its local tables key on"},
	"apps":                        {PlacementNamespace, TrustTenantData, "the tenant's applications"},
	"deployments":                 {PlacementCluster, TrustPlatform, "deployment rows: written by `orama deploy` through the main gateway and read by host routing, recovery and every gateway's deployment service; a tenant copy was an empty table every namespace-host call read"},
	"deployment_domains":          {PlacementCluster, TrustPlatform, "a deployment's custom domains, routed on the main gateway"},
	"deployment_events":           {PlacementCluster, TrustPlatform, "written beside the deployment it records"},
	"deployment_health_checks":    {PlacementCluster, TrustPlatform, "written beside the deployment it records"},
	"deployment_history":          {PlacementCluster, TrustPlatform, "a deployment's versions, which rollback restores"},
	"deployment_replicas":         {PlacementCluster, TrustPlatform, "which nodes run a deployment, read while serving and checking it"},
	"home_node_assignments":       {PlacementCluster, TrustPlatform, "which node hosts a deployment, read while serving it"},
	"port_allocations":            {PlacementCluster, TrustPlatform, "a deployment's ports on a node, unique across every namespace on it"},
	"functions":                   {PlacementNamespace, TrustPlatform, "served per namespace"},
	"function_cron_triggers":      {PlacementNamespace, TrustPlatform, "served per namespace"},
	"function_db_change_tracking": {PlacementNamespace, TrustTenantData, "served per namespace"},
	"function_db_triggers":        {PlacementNamespace, TrustPlatform, "served per namespace"},
	"function_env_vars":           {PlacementNamespace, TrustPlatform, "served per namespace"},
	"function_invocations":        {PlacementNamespace, TrustTelemetry, "served per namespace"},
	"function_jobs":               {PlacementNamespace, TrustTenantData, "served per namespace"},
	"function_logs":               {PlacementNamespace, TrustTelemetry, "served per namespace"},
	"function_pubsub_triggers":    {PlacementNamespace, TrustPlatform, "served per namespace"},
	"function_rate_limits":        {PlacementNamespace, TrustTenantData, "served per namespace"},
	"function_secrets":            {PlacementNamespace, TrustPlatform, "read on the invocation path, per namespace"},
	"function_timers":             {PlacementNamespace, TrustTenantData, "served per namespace"},
	"ipfs_content_ownership":      {PlacementNamespace, TrustPlatform, "read through ORMClient by the storage handlers"},
	"namespace_quotas":            {PlacementNamespace, TrustPlatform, "read through ORMClient by the storage handlers"},
	"namespace_sqlite_databases":  {PlacementNamespace, TrustPlatform, "the tenant's own databases, listed per namespace"},
	"namespace_sqlite_backups":    {PlacementNamespace, TrustPlatform, "beside namespace_sqlite_databases"},
	"namespace_publish_seq":       {PlacementNamespace, TrustTenantData, "per-namespace publish ordering"},
	"namespace_push_config":       {PlacementNamespace, TrustPlatform, "read on the push path, per namespace"},
	"namespace_push_credentials":  {PlacementNamespace, TrustPlatform, "read on the push path, per namespace"},
	"namespace_rate_limit_config": {PlacementNamespace, TrustPlatform, "read on every request to this namespace"},
	"namespace_webrtc_config":     {PlacementNamespace, TrustPlatform, "read on the WebRTC path, per namespace"},
	"push_devices":                {PlacementNamespace, TrustTenantData, "the tenant's devices"},
	"push_topics":                 {PlacementNamespace, TrustTenantData, "the tenant's devices, addressed by rotating topic (FEAT-265)"},
	"webrtc_rooms":                {PlacementCluster, TrustPlatform, "only the cluster manager and namespace delete touch it (both on the index gateway, both to delete a namespace's rows); no namespace gateway code reads or writes a room row"},
	"request_logs":                {PlacementNamespace, TrustTelemetry, "this gateway's own request log"},
	"subscriptions":               {PlacementNamespace, TrustTenantData, "dead since 002_core; stripped separately by name collision"},
	"cluster_locks":               {PlacementNamespace, TrustPlatform, "the migration runner takes one on the database it is migrating"},
	"schema_migrations":           {PlacementNamespace, TrustTenantData, "the tenant's own tracker; core's lives in orama_schema_migrations"},

	// --- cluster control plane -------------------------------------------
	//
	// Written by the index, the node process or the CLI. Each was checked
	// against every reader and writer: a table is PlacementCluster only when
	// nothing running on a namespace gateway touches it, and the Why says what
	// was found. The ones left here are read or written on a namespace
	// gateway's own handle by code that is mounted there, so stripping them
	// would turn an empty answer into "no such table" in that code path.
	"invite_tokens":                {PlacementNamespace, TrustPlatform, "cluster join; the join, enrol and operator-invite handlers are built on ORMClient and mounted on every gateway (gateway.go, routes.go)"},
	"wireguard_peers":              {PlacementNamespace, TrustPlatform, "mesh membership; the wireguard, node-API, join and enrol handlers are built on ORMClient and mounted on every gateway (gateway.go, routes.go)"},
	"dns_records":                  {PlacementNamespace, TrustPlatform, "cluster DNS; a namespace gateway writes it through g.sqlDB (namespace_health.go). The deployment service writes it in the registry on every gateway (gateway.go deploymentRegistry)"},
	"dns_nodes":                    {PlacementNamespace, TrustPlatform, "cluster DNS; read on a namespace gateway through g.sqlDB (namespace_health.go) and by the deployment home-node and replica managers on ORMClient"},
	"dns_nameservers":              {PlacementCluster, TrustPlatform, "read and written by the node process, CoreDNS (which dials the registry) and the CLI; no gateway package names it"},
	"raft_evicted_nodes":           {PlacementCluster, TrustPlatform, "tombstones read and written only by the node process on its own index rqlite (eviction.go, the membership reconciler) and by the CLI; a namespace gateway never opens it"},
	"node_health_events":           {PlacementCluster, TrustPlatform, "written by the peer health monitor, which starts only on the cluster gateway (gateway.go: !isNamespaceGateway), and read by the voter-eviction corroboration on the node"},
	"rqlite_backups":               {PlacementCluster, TrustPlatform, "written by the node process right after it snapshots its index rqlite (backup_offbox.go); no gateway reads it"},
	"namespace_clusters":           {PlacementNamespace, TrustPlatform, "cluster topology; a namespace gateway reads it through g.sqlDB in the namespace health loop, which starts on every gateway (gateway.go)"},
	"namespace_cluster_nodes":      {PlacementNamespace, TrustPlatform, "cluster topology; a namespace gateway reads it through g.sqlDB in the namespace health loop, which starts on every gateway (gateway.go)"},
	"namespace_cluster_events":     {PlacementCluster, TrustPlatform, "the cluster manager's log, written and read through cm.db, which exists only on the index gateway (WireCoreGateway)"},
	"namespace_port_allocations":   {PlacementNamespace, TrustPlatform, "cluster port allocation; a namespace gateway reads it through g.sqlDB in the namespace health loop, which starts on every gateway (gateway.go)"},
	"webrtc_port_allocations":      {PlacementCluster, TrustPlatform, "which node runs which SFU or TURN role; read through the registry handle (sfu_directory.go uses globalSQLDB) and written by the cluster manager on the index gateway"},
	"global_deployment_subdomains": {PlacementNamespace, TrustPlatform, "subdomain ownership; the deployment service claims and releases subdomains in the registry on every gateway (gateway.go deploymentRegistry), so a tenant copy is unused; it stays placed here until no other namespace-gateway code is found to touch it"},
	"namespace_pending_cleanup":    {PlacementCluster, TrustPlatform, "the tenant reconciler's retry queue, run by the cluster manager on the index gateway through cm.db"},
}

// TablesOfTrust returns the tables of one trust class, sorted.
func TablesOfTrust(trust Trust) []string {
	out := make([]string, 0, len(tablePlacement))
	for table, note := range tablePlacement {
		if note.Trust == trust {
			out = append(out, table)
		}
	}
	sort.Strings(out)
	return out
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
