package constants

import "time"

// The time budget of an environment change on a replicated deployment, from
// the innermost wait to the outermost. Each link must be strictly shorter than
// the one around it, or the outer one gives up while the inner one is still
// working and the caller is told "timed out" about a change that went through:
//
//	replica restarts the unit        DeploymentEnvReconfigureTimeout  20s
//	home node waits for that call    DeploymentEnvCallTimeout         30s
//	handler, lock wait included      DeploymentEnvChangeBudget        60s
//	gateway hop to the home node     GatewayProxyTimeout             120s
//	gateway server write deadline    GatewayServerWriteTimeout       120s
//
// (An env change is a deployment change, so on its route both limits are the
// longer ones below; the chain holds against either.)
//
//	CLI for env set / env unset      DeploymentEnvClientTimeout       90s
//
// The CLI is shorter than the two gateway limits on purpose: it only has to
// outlast the handler, which answers within DeploymentEnvChangeBudget.
//
// The environment change's budget is split in two so the replicas are never
// left with what the local step did not use: the lock wait and the local
// restart run under DeploymentEnvLocalBudget, and the wait for the replicas then
// gets its own DeploymentEnvCallTimeout, whatever the local step took.
//
// An update or rollback waits longer, because a replica fetches the artifact and
// waits for the app's health check. It has the same two segments, and the routes
// that change a deployment (update, rollback, env, delete) move both gateways'
// limits out to match, on the entry gateway and on the home node:
//
//	replica fetches and starts       DeploymentReplicaCallTimeout    180s
//	replica segment (call + report)  DeploymentUpdateReplicaBudget   210s
//	lock wait + local work           DeploymentUpdateLocalBudget     120s
//	whole handler on the home node   DeploymentUpdateBudget          330s
//	gateway hop to the home node     GatewayDeploymentProxyTimeout   360s
//	entry gateway write deadline     GatewayDeploymentWriteBudget    390s
//
// An environment change takes the same per-deployment lock as an update. One
// that arrives while an update holds it waits at most its own local budget,
// then is refused with 503 and asked to run again; an update that finds the lock
// held waits at most DeploymentUpdateLocalBudget the same way.
const (
	// DeploymentEnvReconfigureTimeout bounds a replica rewriting its env file
	// and restarting the unit. It runs detached from the caller's connection,
	// so the bound is what stops it outliving the caller's wait.
	DeploymentEnvReconfigureTimeout = 20 * time.Second

	// DeploymentEnvCallTimeout bounds the home node's wait for one replica.
	DeploymentEnvCallTimeout = 30 * time.Second

	// DeploymentEnvChangeBudget bounds a whole environment change on the home
	// node: waiting for the deployment's lock, the local restart and the wait
	// for every replica.
	DeploymentEnvChangeBudget = 60 * time.Second

	// DeploymentEnvLocalBudget bounds the lock wait, the write and the local
	// restart of an environment change. What is left of DeploymentEnvChangeBudget
	// is reserved for the replicas.
	DeploymentEnvLocalBudget = DeploymentEnvChangeBudget - DeploymentEnvCallTimeout

	// GatewayProxyTimeout bounds a gateway's call to another node's gateway
	// when it forwards a request to a deployment's home node.
	GatewayProxyTimeout = 120 * time.Second

	// GatewayServerWriteTimeout is the HTTP server's write deadline.
	GatewayServerWriteTimeout = 120 * time.Second

	// DeploymentEnvClientTimeout is how long the CLI waits for env set and env
	// unset. Every other gateway call keeps the shorter default.
	DeploymentEnvClientTimeout = 90 * time.Second

	// DeploymentReplicaCallTimeout bounds the home node's wait for one replica
	// to apply an update or rollback: the artifact wait plus the health wait.
	DeploymentReplicaCallTimeout = 180 * time.Second

	// DeploymentUpdateReplicaBudget is the segment of an update or rollback that
	// waits for the replicas: one replica call, with room to report.
	DeploymentUpdateReplicaBudget = DeploymentReplicaCallTimeout + 30*time.Second

	// DeploymentUpdateLocalBudget bounds the lock wait and the work on the home
	// node (extract, pin, start, health) of an update or rollback.
	DeploymentUpdateLocalBudget = 120 * time.Second

	// DeploymentUpdateBudget bounds a whole update or rollback on the home node:
	// the local segment and then the replica segment.
	DeploymentUpdateBudget = DeploymentUpdateLocalBudget + DeploymentUpdateReplicaBudget

	// GatewayDeploymentProxyTimeout bounds a gateway's call to a deployment's
	// home node for a route that changes the deployment.
	GatewayDeploymentProxyTimeout = DeploymentUpdateBudget + 30*time.Second

	// GatewayDeploymentWriteBudget is the write deadline the entry gateway
	// gives a request that changes a deployment, in place of the server's.
	GatewayDeploymentWriteBudget = GatewayDeploymentProxyTimeout + 30*time.Second
)
