package namespace

// v1StampActions are the spawn actions a request stamped only with the v1
// coordination MAC may still carry during a rolling upgrade. The v1 MAC does not
// cover the body, so a captured request can have its action and namespace
// swapped inside the skew window; these are the actions where that costs the
// namespace nothing it would not lose anyway: they start or re-apply a service,
// or save state the next call rewrites. Everything else — every stop-*,
// teardown-*, delete-cluster-state, and a spawn-rqlite that declares a
// brand-new cluster (FreshStart) — needs the v2 MAC.
var v1StampActions = map[string]bool{
	"spawn-olric":        true,
	"spawn-gateway":      true,
	"restart-gateway":    true,
	"spawn-sfu":          true,
	"save-cluster-state": true,
	"spawn-rqlite":       true,
}

// requiresBodyBoundMAC reports whether req may only be accepted under the v2
// coordination MAC, which covers the body.
func requiresBodyBoundMAC(req *SpawnRequest) bool {
	if !v1StampActions[req.Action] {
		return true
	}
	return req.Action == "spawn-rqlite" && req.RQLiteFreshStart
}
