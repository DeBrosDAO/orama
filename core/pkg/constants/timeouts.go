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
//	CLI for env set / env unset      DeploymentEnvClientTimeout       90s
//
// The CLI is shorter than the two gateway limits on purpose: it only has to
// outlast the handler, which answers within DeploymentEnvChangeBudget.
//
// An update or rollback waits longer, because a replica fetches the artifact and
// waits for the app's health check. Its chain is nested the same way, and the
// routes that make a deployment change (update, rollback, env, delete) move both
// gateways' limits out to match, on the entry gateway and on the home node:
//
//	replica fetches and starts       DeploymentReplicaCallTimeout    180s
//	handler waits for every replica  DeploymentUpdateBudget          210s
//	gateway hop to the home node     GatewayDeploymentProxyTimeout   240s
//	entry gateway write deadline     GatewayDeploymentWriteBudget    270s
//
// An environment change takes the same per-deployment lock as an update. One
// that arrives while an update holds it waits at most DeploymentEnvChangeBudget,
// then is refused with 503 and asked to run again; it never waits out the update.
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

	// DeploymentUpdateBudget is how long an update's or rollback's response may
	// be delayed by its replicas: one replica call, with room to report.
	DeploymentUpdateBudget = DeploymentReplicaCallTimeout + 30*time.Second

	// GatewayDeploymentProxyTimeout bounds a gateway's call to a deployment's
	// home node for a route that changes the deployment.
	GatewayDeploymentProxyTimeout = DeploymentUpdateBudget + 30*time.Second

	// GatewayDeploymentWriteBudget is the write deadline the entry gateway
	// gives a request that changes a deployment, in place of the server's.
	GatewayDeploymentWriteBudget = GatewayDeploymentProxyTimeout + 30*time.Second
)
