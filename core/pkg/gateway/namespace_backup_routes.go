package gateway

import (
	"net/http"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	backuphandlers "github.com/DeBrosOfficial/network/pkg/gateway/handlers/backup"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"go.uber.org/zap"
)

// initNamespaceBackup mounts backup and restore on a namespace gateway.
//
// The cluster gateway's database is the registry, not a tenant's, so it never
// serves them: a backup there would hand out every tenant's secrets and a
// restore would replace the registry.
func (g *Gateway) initNamespaceBackup(deps *Dependencies) {
	if g.servesCoreRegistry() || deps.ORMClient == nil || deps.GlobalORMClient == nil || deps.IPFSClient == nil {
		return
	}
	h, err := backuphandlers.New(backuphandlers.Config{
		Namespace:         ownNamespace(g.cfg),
		DB:                deps.ORMClient,
		Registry:          deps.GlobalORMClient,
		Snapshots:         newRQLiteSnapshots(g.rqliteBaseURL()),
		Pins:              deps.IPFSClient,
		Root:              g.encHolder.Get,
		ReplicationFactor: g.cfg.IPFSReplicationFactor,
		Caller:            backupCaller,
		Audit:             deps.AuthService.Audit(),
		Logger:            g.logger.Logger,
	})
	if err != nil {
		g.logger.ComponentError(logging.ComponentGeneral,
			"namespace backup and restore are unavailable on this gateway", zap.Error(err))
		return
	}
	g.backupHandler = h
}

// backupCaller is the namespace the credential belongs to, and whether it is
// the owner's grant.
func backupCaller(r *http.Request) (string, bool) {
	grant := callerGrant(r)
	return keysNamespace(r), grant != nil && grant.Role == auth.RoleOwner
}

func (g *Gateway) namespaceBackupHandler(w http.ResponseWriter, r *http.Request) {
	if g.backupHandler == nil {
		writeError(w, http.StatusServiceUnavailable, backupUnavailable)
		return
	}
	g.backupHandler.BackupHandler(w, r)
}

func (g *Gateway) namespaceRestoreKeyHandler(w http.ResponseWriter, r *http.Request) {
	if g.backupHandler == nil {
		writeError(w, http.StatusServiceUnavailable, backupUnavailable)
		return
	}
	g.backupHandler.RestoreKeyHandler(w, r)
}

func (g *Gateway) namespaceRestoreHandler(w http.ResponseWriter, r *http.Request) {
	if g.backupHandler == nil {
		writeError(w, http.StatusServiceUnavailable, backupUnavailable)
		return
	}
	g.backupHandler.RestoreHandler(w, r)
}

// backupUnavailable is why a gateway refuses the backup routes.
const backupUnavailable = "namespace backup and restore run only on a namespace's own gateway with RQLite and IPFS configured"
