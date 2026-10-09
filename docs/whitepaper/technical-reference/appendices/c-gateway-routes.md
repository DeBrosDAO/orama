# Gateway routes

> **At a glance.**
>
> - **Generated** from the gateway's route policy table (`core/pkg/gateway/route_policy.go:buildRoutePolicies`) and the handlers `routes.go` mounts by `make whitepaper-gen`. Do not edit by hand: the gate fails when this file and the code disagree.

Every route the gateway serves, with the policy the route table declares for it: what credential the middleware insists on, which grant a caller needs, and which token. A pattern ending in a slash matches every path beneath it. A route absent from the policy table cannot be registered, so this list is complete. [Gateway architecture](../vol1/12-gateway-architecture.md) explains the middleware and [Authorization](../vol1/14-authorization.md) explains grants.

Access is `credential` (an API key or a JWT, resolved by the middleware), `open` (anyone) or `handler-auth` (the handler authenticates the caller itself: an invite token, a cluster-secret or coordination MAC). Grant is `domain:action`; an empty grant means any valid credential. Token is the token kind required on top of the grant: `any` (a bare key), `token` (a JWT of any kind), `wallet` (a logged-in user) or `principal` (a user or a deployed app's workload token). Notes lists: `owned` (a live grant in the namespace), `main` (always served by the index gateway, never proxied to a namespace gateway), `narrowed` (an open route that still applies a grant's resource selector), `no-address` and `no-log` (what the request log keeps).

## /.well-known

| Route | Handler | Access | Grant | Token | Notes |
|---|---|---|---|---|---|
| `/.well-known/jwks.json` | `g.authService.JWKSHandler` | open |  | any |  |

## /health

| Route | Handler | Access | Grant | Token | Notes |
|---|---|---|---|---|---|
| `/health` | `g.healthHandler` | open |  | any |  |

## /status

| Route | Handler | Access | Grant | Token | Notes |
|---|---|---|---|---|---|
| `/status` | `g.statusHandler` | open |  | any |  |
| `/status/assets/` | `statuspage.Assets()` | open |  | any |  |

## /v1/audit

| Route | Handler | Access | Grant | Token | Notes |
|---|---|---|---|---|---|
| `/v1/audit` | `g.authHandlers.AuditHandler` | credential | `audit:read` | any |  |

## /v1/auth

| Route | Handler | Access | Grant | Token | Notes |
|---|---|---|---|---|---|
| `/v1/auth/api-key` | `g.authHandlers.IssueAPIKeyHandler` | open |  | any |  |
| `/v1/auth/challenge` | `g.authHandlers.ChallengeHandler` | open |  | any |  |
| `/v1/auth/device` | `g.authHandlers.DeviceAuthorizationHandler` | open |  | any |  |
| `/v1/auth/device/approve` | `g.authHandlers.DeviceApprovalHandler` | open |  | any |  |
| `/v1/auth/device/token` | `g.authHandlers.DeviceTokenHandler` | open |  | any |  |
| `/v1/auth/devices` | `g.authHandlers.DevicesHandler` | credential |  | any |  |
| `/v1/auth/devices/` | `g.authHandlers.DeviceByIDHandler` | credential |  | any |  |
| `/v1/auth/jwks` | `g.authService.JWKSHandler` | open |  | any |  |
| `/v1/auth/logout` | `g.authHandlers.LogoutHandler` | open |  | any |  |
| `/v1/auth/refresh` | `g.authHandlers.RefreshHandler` | open |  | any |  |
| `/v1/auth/renew` | `g.authHandlers.RenewHandler` | credential |  | any |  |
| `/v1/auth/sessions` | `g.authHandlers.SessionsHandler` | credential |  | any |  |
| `/v1/auth/sessions/` | `g.authHandlers.SessionByIDHandler` | credential |  | any |  |
| `/v1/auth/token` | `g.authHandlers.APIKeyToJWTHandler` | credential |  | any |  |
| `/v1/auth/verify` | `g.authHandlers.VerifyHandler` | open |  | any |  |
| `/v1/auth/whoami` | `g.authHandlers.WhoamiHandler` | credential |  | any |  |

## /v1/cache

| Route | Handler | Access | Grant | Token | Notes |
|---|---|---|---|---|---|
| `/v1/cache/delete` | `g.cacheDeleteHandler` | credential | `cache:write` | any |  |
| `/v1/cache/get` | `g.cacheGetHandler` | credential | `cache:read` | any |  |
| `/v1/cache/health` | `g.cacheHealthHandler` | credential | `cache:read` | any |  |
| `/v1/cache/mget` | `g.cacheMGetHandler` | credential | `cache:read` | any |  |
| `/v1/cache/put` | `g.cachePutHandler` | credential | `cache:write` | any |  |
| `/v1/cache/scan` | `g.cacheScanHandler` | credential | `cache:read` | any |  |

## /v1/chain

| Route | Handler | Access | Grant | Token | Notes |
|---|---|---|---|---|---|
| `/v1/chain/` | `inline handler` | open |  | any |  |

## /v1/db

| Route | Handler | Access | Grant | Token | Notes |
|---|---|---|---|---|---|
| `/v1/db/sqlite/backup` | `g.sqliteBackupHandler.BackupDatabase` | credential | `db:write` | any |  |
| `/v1/db/sqlite/backups` | `g.sqliteBackupHandler.ListBackups` | credential | `db:read` | any |  |
| `/v1/db/sqlite/create` | `g.sqliteHandler.CreateDatabase` | credential | `db:write` | any |  |
| `/v1/db/sqlite/delete` | `g.sqliteHandler.DeleteDatabase` | credential | `db:write` | any |  |
| `/v1/db/sqlite/list` | `g.sqliteHandler.ListDatabases` | credential | `db:read` | any |  |
| `/v1/db/sqlite/query` | `g.sqliteHandler.QueryDatabase` | credential | `db:write` | any |  |

## /v1/deployments

| Route | Handler | Access | Grant | Token | Notes |
|---|---|---|---|---|---|
| `/v1/deployments/delete` | `g.listHandler.HandleDelete` | credential | `deploy:write` | any | main |
| `/v1/deployments/domains/add` | `g.domainHandler.HandleAddDomain` | credential | `deploy:write` | any | main |
| `/v1/deployments/domains/list` | `g.domainHandler.HandleListDomains` | credential | `deploy:read` | any | main |
| `/v1/deployments/domains/remove` | `g.domainHandler.HandleRemoveDomain` | credential | `deploy:write` | any | main |
| `/v1/deployments/domains/verify` | `g.domainHandler.HandleVerifyDomain` | credential | `deploy:write` | any | main |
| `/v1/deployments/env` | `g.envHandler.HandleGetEnv` | credential | `secrets:read` | any | main |
| `/v1/deployments/env/set` | `g.envHandler.HandleSetEnv` | credential | `secrets:write` | any | main |
| `/v1/deployments/events` | `g.logsHandler.HandleGetEvents` | credential | `deploy:read` | any | main |
| `/v1/deployments/get` | `g.listHandler.HandleGet` | credential | `deploy:read` | any | main |
| `/v1/deployments/go/update` | `g.updateHandler.HandleUpdate` | credential | `deploy:write` | any | main |
| `/v1/deployments/go/upload` | `g.goHandler.HandleUpload` | credential | `deploy:write` | any | main |
| `/v1/deployments/grants` | `g.appGrantsHandler` | credential | `members:write` | any | main |
| `/v1/deployments/list` | `g.listHandler.HandleList` | credential | `deploy:read` | any | main |
| `/v1/deployments/logs` | `g.logsHandler.HandleLogs` | credential | `deploy:read` | any | main |
| `/v1/deployments/nextjs/update` | `g.updateHandler.HandleUpdate` | credential | `deploy:write` | any | main |
| `/v1/deployments/nextjs/upload` | `g.nextjsHandler.HandleUpload` | credential | `deploy:write` | any | main |
| `/v1/deployments/nodejs/update` | `g.updateHandler.HandleUpdate` | credential | `deploy:write` | any | main |
| `/v1/deployments/nodejs/upload` | `g.nodejsHandler.HandleUpload` | credential | `deploy:write` | any | main |
| `/v1/deployments/rollback` | `g.rollbackHandler.HandleRollback` | credential | `deploy:write` | any | main |
| `/v1/deployments/static/update` | `g.updateHandler.HandleUpdate` | credential | `deploy:write` | any | main |
| `/v1/deployments/static/upload` | `g.staticHandler.HandleUpload` | credential | `deploy:write` | any | main |
| `/v1/deployments/stats` | `g.statsHandler.HandleStats` | credential | `deploy:read` | any | main |
| `/v1/deployments/versions` | `g.rollbackHandler.HandleListVersions` | credential | `deploy:read` | any | main |

## /v1/functions

| Route | Handler | Access | Grant | Token | Notes |
|---|---|---|---|---|---|
| `/v1/functions` | `h.handleFunctions` | credential | `fn:manage` | any | owned |
| `/v1/functions/` | `h.handleFunctionByName` | by request | | | see below |

## /v1/health

| Route | Handler | Access | Grant | Token | Notes |
|---|---|---|---|---|---|
| `/v1/health` | `g.healthHandler` | open |  | any |  |

## /v1/internal

| Route | Handler | Access | Grant | Token | Notes |
|---|---|---|---|---|---|
| `/v1/internal/acme/cleanup` | `g.acmeCleanupHandler` | open |  | any |  |
| `/v1/internal/acme/present` | `g.acmePresentHandler` | open |  | any |  |
| `/v1/internal/deployments/replica/env` | `g.replicaHandler.HandleEnv` | handler-auth |  | any |  |
| `/v1/internal/deployments/replica/rollback` | `g.replicaHandler.HandleRollback` | handler-auth |  | any |  |
| `/v1/internal/deployments/replica/setup` | `g.replicaHandler.HandleSetup` | handler-auth |  | any |  |
| `/v1/internal/deployments/replica/teardown` | `g.replicaHandler.HandleTeardown` | handler-auth |  | any |  |
| `/v1/internal/deployments/replica/update` | `g.replicaHandler.HandleUpdate` | handler-auth |  | any |  |
| `/v1/internal/join` | `g.joinHandler.HandleJoin` | handler-auth |  | any |  |
| `/v1/internal/namespace/repair` | `g.namespaceClusterRepairHandler` | handler-auth |  | any |  |
| `/v1/internal/namespace/spawn` | `g.spawnHandler` | handler-auth |  | any |  |
| `/v1/internal/node/enrol-key` | `g.nodeAPIHandler.HandleEnrolKey` | handler-auth |  | any | main |
| `/v1/internal/node/heartbeat` | `g.nodeAPIHandler.HandleHeartbeat` | handler-auth |  | any | main |
| `/v1/internal/node/register` | `g.nodeAPIHandler.HandleRegister` | handler-auth |  | any | main |
| `/v1/internal/ping` | `g.pingHandler` | open |  | any |  |
| `/v1/internal/push/ntfy/` | `g.handleInternalNtfyPublish` | handler-auth |  | any | main |
| `/v1/internal/secrets/reencrypt` | `g.handleInternalReencrypt` | handler-auth |  | any |  |
| `/v1/internal/storage/evict` | `g.storageHandlers.EvictHandler` | handler-auth |  | any |  |
| `/v1/internal/telemetry` | `g.internalTelemetryHandler` | handler-auth |  | any |  |
| `/v1/internal/tls-store` | `g.tlsStoreHandler` | handler-auth |  | any |  |
| `/v1/internal/tls/check` | `g.tlsCheckHandler` | open |  | any |  |
| `/v1/internal/webrtc/events` | `g.webrtcHandlers.EventsHandler` | handler-auth |  | any |  |

## /v1/invoke

| Route | Handler | Access | Grant | Token | Notes |
|---|---|---|---|---|---|
| `/v1/invoke/` | `h.HandleInvoke` | open |  | any | narrowed |

## /v1/namespace

| Route | Handler | Access | Grant | Token | Notes |
|---|---|---|---|---|---|
| `/v1/namespace/backup` | `g.namespaceBackupHandler` | credential | `secrets:read` | any | owned |
| `/v1/namespace/delete` | `g.namespaceDeleteHandler` | credential | `namespace:write` | any | main |
| `/v1/namespace/devices` | `g.authHandlers.NamespaceDevicesHandler` | credential | `members:write` | any | owned |
| `/v1/namespace/devices/` | `g.authHandlers.NamespaceDeviceByIDHandler` | credential | `members:write` | any | owned |
| `/v1/namespace/keys` | `g.namespaceKeysHandler` | credential | `members:write` | any | owned, main |
| `/v1/namespace/keys/` | `g.namespaceKeysByIDHandler` | credential | `members:write` | any | owned, main |
| `/v1/namespace/list` | `g.namespaceListHandler` | credential |  | wallet | main |
| `/v1/namespace/members` | `g.namespaceMembersHandler` | credential | `members:write` | any | owned |
| `/v1/namespace/members/` | `g.namespaceMemberByIDHandler` | credential | `members:write` | any | owned |
| `/v1/namespace/push-credentials` | `g.pushCredentialsSummaryHandler` | credential | `secrets:write` | any | owned |
| `/v1/namespace/push-credentials/` | `g.pushCredentialsByProviderHandler` | credential | `secrets:write` | any | owned |
| `/v1/namespace/rate-limit` | `g.rateLimitConfigDispatcher` | credential | `namespace:write` | any |  |
| `/v1/namespace/restore` | `g.namespaceRestoreHandler` | credential | `db:write` | any | owned |
| `/v1/namespace/restore-key` | `g.namespaceRestoreKeyHandler` | credential | `db:read` | any | owned |
| `/v1/namespace/session-policy` | `g.authHandlers.SessionPolicyHandler` | credential | `namespace:write` | any |  |
| `/v1/namespace/status` | `g.namespaceClusterStatusHandler` | open |  | any |  |
| `/v1/namespace/webrtc/disable` | `g.namespaceWebRTCDisablePublicHandler` | credential | `namespace:write` | any |  |
| `/v1/namespace/webrtc/enable` | `g.namespaceWebRTCEnablePublicHandler` | credential | `namespace:write` | any |  |
| `/v1/namespace/webrtc/status` | `g.namespaceWebRTCStatusPublicHandler` | credential |  | any |  |
| `/v1/namespace/webrtc/stealth/disable` | `inline handler` | credential | `namespace:write` | any |  |
| `/v1/namespace/webrtc/stealth/enable` | `inline handler` | credential | `namespace:write` | any |  |

## /v1/namespaces

| Route | Handler | Access | Grant | Token | Notes |
|---|---|---|---|---|---|
| `/v1/namespaces` | `g.namespaceCreateHandler` | credential |  | wallet |  |

## /v1/network

| Route | Handler | Access | Grant | Token | Notes |
|---|---|---|---|---|---|
| `/v1/network/connect` | `g.networkConnectHandler` | credential | `operator:write` | any | main |
| `/v1/network/disconnect` | `g.networkDisconnectHandler` | credential | `operator:write` | any | main |
| `/v1/network/peers` | `g.networkPeersHandler` | by request | | | see below |
| `/v1/network/status` | `g.networkStatusHandler` | by request | | | see below |

## /v1/node

| Route | Handler | Access | Grant | Token | Notes |
|---|---|---|---|---|---|
| `/v1/node/command` | `g.enrollHandler.HandleNodeCommand` | credential | `operator:write` | any |  |
| `/v1/node/enroll` | `g.enrollHandler.HandleEnroll` | handler-auth |  | any |  |
| `/v1/node/leave` | `g.enrollHandler.HandleNodeLeave` | credential | `operator:write` | any |  |
| `/v1/node/logs` | `g.enrollHandler.HandleNodeLogs` | credential | `operator:read` | any |  |
| `/v1/node/status` | `g.enrollHandler.HandleNodeStatus` | credential | `operator:read` | any |  |

## /v1/operator

| Route | Handler | Access | Grant | Token | Notes |
|---|---|---|---|---|---|
| `/v1/operator/creators` | `g.operatorHandler.HandleCreators` | credential | `operator:write` | any | main |
| `/v1/operator/creators/` | `g.operatorHandler.HandleCreators` | credential | `operator:write` | any | main |
| `/v1/operator/health` | `g.operatorHealthHandler` | credential | `operator:read` | any | main |
| `/v1/operator/invite` | `g.operatorHandler.HandleInvite` | credential | `*:*` | any | main |
| `/v1/operator/namespaces/remove` | `g.namespaceOperatorRemoveHandler` | credential | `operator:write` | any | main |
| `/v1/operator/node/register` | `g.operatorHandler.HandleRegister` | credential | `operator:write` | any | main |
| `/v1/operator/nodes` | `g.operatorHandler.HandleListNodes` | credential | `operator:read` | any | main |
| `/v1/operator/operators` | `g.operatorHandler.HandleOperators` | credential | `operator:write` | any | main |
| `/v1/operator/operators/` | `g.operatorHandler.HandleOperators` | credential | `operator:write` | any | main |
| `/v1/operator/rotate-secrets` | `g.handleRotateSecrets` | credential | `operator:write` | any | main |
| `/v1/operator/rotate-signing-key` | `g.handleRotateSigningKey` | credential | `operator:write` | any | main |
| `/v1/operator/settings` | `g.operatorHandler.HandleSettings` | credential | `operator:write` | any | main |
| `/v1/operator/settings/` | `g.operatorHandler.HandleSettings` | credential | `operator:write` | any | main |
| `/v1/operator/telemetry` | `g.operatorTelemetryHandler` | credential | `operator:read` | any | main |
| `/v1/operator/telemetry/stream` | `g.operatorTelemetryStreamHandler` | credential | `operator:read` | any | main |

## /v1/proxy

| Route | Handler | Access | Grant | Token | Notes |
|---|---|---|---|---|---|
| `/v1/proxy/anon` | `g.anonProxyHandler` | credential | `proxy:write` | wallet | owned |
| `/v1/proxy/relay` | `g.relayTunnelHandler` | open |  | any | main, no-log |
| `/v1/proxy/tunnel` | `g.anonTunnelHandler` | credential | `proxy:write` | wallet | owned |

## /v1/pubsub

| Route | Handler | Access | Grant | Token | Notes |
|---|---|---|---|---|---|
| `/v1/pubsub/presence` | `g.pubsubHandlers.PresenceHandler` | credential | `pubsub:read` | any | owned |
| `/v1/pubsub/publish` | `g.pubsubHandlers.PublishHandler` | credential | `pubsub:write` | any | owned |
| `/v1/pubsub/publish-batch` | `g.pubsubHandlers.PublishBatchHandler` | credential | `pubsub:write` | any | owned |
| `/v1/pubsub/topics` | `g.pubsubHandlers.TopicsHandler` | credential | `pubsub:read` | any | owned |
| `/v1/pubsub/ws` | `g.pubsubHandlers.WebsocketHandler` | credential | `pubsub:read` | any | owned |

## /v1/push

| Route | Handler | Access | Grant | Token | Notes |
|---|---|---|---|---|---|
| `/v1/push/config` | `g.pushConfigHandler` | credential | `secrets:write` | any | owned |
| `/v1/push/devices` | `g.pushDevicesHandler` | credential | `push:write` | any | owned |
| `/v1/push/devices/` | `g.pushDevicesByIDHandler` | credential | `push:write` | any | owned |
| `/v1/push/send` | `g.pushSendHandler` | credential | `push:write` | any | owned |
| `/v1/push/topics` | `g.pushTopicsHandler` | credential | `push:write` | any | owned |
| `/v1/push/topics/send` | `g.pushTopicsSendHandler` | credential | `push:write` | any | owned |

## /v1/rqlite

| Route | Handler | Access | Grant | Token | Notes |
|---|---|---|---|---|---|
| `/v1/rqlite/create-table` | `rqlite.HTTPGateway (core/pkg/rqlite/gateway.go)` | credential | `db:write` | any | owned |
| `/v1/rqlite/drop-table` | `rqlite.HTTPGateway (core/pkg/rqlite/gateway.go)` | credential | `db:write` | any | owned |
| `/v1/rqlite/exec` | `rqlite.HTTPGateway (core/pkg/rqlite/gateway.go)` | credential | `db:write` | any | owned |
| `/v1/rqlite/export` | `g.rqliteExportHandler` | credential | `db:read` | any | owned |
| `/v1/rqlite/find` | `rqlite.HTTPGateway (core/pkg/rqlite/gateway.go)` | credential | `db:write` | any | owned |
| `/v1/rqlite/find-one` | `rqlite.HTTPGateway (core/pkg/rqlite/gateway.go)` | credential | `db:write` | any | owned |
| `/v1/rqlite/import` | `g.rqliteImportHandler` | credential | `db:write` | any | owned |
| `/v1/rqlite/query` | `rqlite.HTTPGateway (core/pkg/rqlite/gateway.go)` | credential | `db:write` | any | owned |
| `/v1/rqlite/schema` | `rqlite.HTTPGateway (core/pkg/rqlite/gateway.go)` | credential | `db:write` | any | owned |
| `/v1/rqlite/select` | `rqlite.HTTPGateway (core/pkg/rqlite/gateway.go)` | credential | `db:write` | any | owned |
| `/v1/rqlite/transaction` | `rqlite.HTTPGateway (core/pkg/rqlite/gateway.go)` | credential | `db:write` | any | owned |

## /v1/schema-status

| Route | Handler | Access | Grant | Token | Notes |
|---|---|---|---|---|---|
| `/v1/schema-status` | `g.handleSchemaStatus` | credential |  | any |  |

## /v1/serverless

| Route | Handler | Access | Grant | Token | Notes |
|---|---|---|---|---|---|
| `/v1/serverless/ws/connections` | `h.WSConnections` | credential | `fn:read` | any | owned |
| `/v1/serverless/ws/connections/` | `h.WSConnections` | credential | `fn:read` | any | owned |

## /v1/status

| Route | Handler | Access | Grant | Token | Notes |
|---|---|---|---|---|---|
| `/v1/status` | `g.statusHandler` | open |  | any |  |

## /v1/storage

| Route | Handler | Access | Grant | Token | Notes |
|---|---|---|---|---|---|
| `/v1/storage/fetch-caps` | `g.storageHandlers.FetchCapsHandler` | credential | `storage:read` | principal |  |
| `/v1/storage/fetch-caps/` | `g.storageHandlers.FetchCapsHandler` | credential | `storage:read` | principal |  |
| `/v1/storage/get/` | `g.storageHandlers.DownloadHandler` | credential | `storage:read` | principal |  |
| `/v1/storage/pin` | `g.storageHandlers.PinHandler` | credential | `storage:write` | principal |  |
| `/v1/storage/relayed/` | `g.storageHandlers.RelayedDownloadHandler` | handler-auth |  | any | no-address |
| `/v1/storage/status/` | `g.storageHandlers.StatusHandler` | credential | `storage:read` | principal |  |
| `/v1/storage/unpin/` | `g.storageHandlers.UnpinHandler` | by request | | | see below |
| `/v1/storage/upload` | `g.storageHandlers.UploadHandler` | credential | `storage:write` | principal |  |

## /v1/vault

| Route | Handler | Access | Grant | Token | Notes |
|---|---|---|---|---|---|
| `/v1/vault/health` | `g.vaultHandlers.HandleHealth` | handler-auth |  | any |  |
| `/v1/vault/pull` | `g.vaultHandlers.HandlePull` | handler-auth |  | any |  |
| `/v1/vault/push` | `g.vaultHandlers.HandlePush` | handler-auth |  | any |  |
| `/v1/vault/status` | `g.vaultHandlers.HandleStatus` | handler-auth |  | any |  |

## /v1/version

| Route | Handler | Access | Grant | Token | Notes |
|---|---|---|---|---|---|
| `/v1/version` | `g.versionHandler` | open |  | any |  |

## /v1/webrtc

| Route | Handler | Access | Grant | Token | Notes |
|---|---|---|---|---|---|
| `/v1/webrtc/config` | `g.webrtcHandlers.ConfigHandler` | credential | `namespace:write` | any |  |
| `/v1/webrtc/rooms` | `g.webrtcHandlers.RoomsHandler` | credential | `webrtc:read` | principal | owned |
| `/v1/webrtc/signal` | `g.webrtcHandlers.SignalHandler` | credential | `webrtc:read` | principal | owned |
| `/v1/webrtc/turn/credentials` | `g.webrtcHandlers.CredentialsHandler` | credential | `webrtc:read` | principal | owned |

## Routes whose policy depends on the request

| Route | Policy |
|---|---|
| `/v1/functions/` | by operation: invoke is open (narrowed by a fn grant), a WebSocket opened with a capability is handler-auth, a WebSocket otherwise needs fn:invoke, everything else fn:manage with a namespace grant |
| `/v1/network/peers` | a request carrying a coordination MAC is handler-auth; any other needs operator:read and the operator list, on the main gateway |
| `/v1/network/status` | a request carrying a coordination MAC is handler-auth; any other needs operator:read and the operator list, on the main gateway |
| `/v1/storage/unpin/` | DELETE needs storage:write with any token (a key exchanged for a token is enough); any other method needs storage:write with a principal token |
