package gateway

import (
	"context"
	"os"
	"path/filepath"
	"time"

	"github.com/DeBrosOfficial/network/pkg/constants"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/tlsstore"
	"go.uber.org/zap"
)

// tlsExportInterval is how often the cluster gateway compares the exported
// wildcard with the store, and retries a failed import. A renewal reaches TURN
// within it plus the TURN reloader's own poll; certificates are renewed weeks
// before they expire.
const tlsExportInterval = time.Minute

// legacyImportMarker records that this node's certificates from before the
// shared store were imported, so they are imported once.
const legacyImportMarker = ".legacy-imported"

// startTLSStore prepares the cluster's certificate store on this node and
// keeps the `*.<base>` pair exported for the shared TURN server (bugboard
// #751). Cluster gateway only.
//
// First it imports the certificates this node's Caddy kept on disk before the
// store, then it lets the store answer Caddy: a Caddy that found the store
// empty would order certificates the cluster already has. An import that fails
// is retried and the store keeps refusing (503), so Caddy does not start
// rather than spend the CA's limits.
func (g *Gateway) startTLSStore(ctx context.Context) {
	keys, err := tlsstore.KeysFromClusterSecret(g.cfg.ClusterSecret)
	if err != nil {
		g.logger.ComponentError(logging.ComponentGeneral, "The cluster's certificate store cannot open on this node", zap.Error(err))
		return
	}
	if g.cfg.DataDir == "" {
		g.logger.ComponentError(logging.ComponentGeneral, "The cluster's certificate store cannot open on this node: the gateway has no data_dir to record the certificate import in")
		return
	}
	ticker := time.NewTicker(tlsExportInterval)
	defer ticker.Stop()
	for !g.importLegacyTLS(ctx, keys.Seal) {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
	g.tlsStoreReady.Store(true)
	if g.cfg.BaseDomain == "" {
		return
	}

	exporter := tlsstore.NewExporter(g.sqlDB, keys.Seal, g.cfg.BaseDomain,
		constants.WildcardCertPath(g.cfg.DataDir), constants.WildcardKeyPath(g.cfg.DataDir))
	var last string
	for {
		last = g.exportTLSOnce(ctx, exporter, last)
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
		}
	}
}

// importLegacyTLS imports this node's on-disk Caddy certificates once, and
// reports whether the store may serve.
func (g *Gateway) importLegacyTLS(ctx context.Context, sealKey []byte) bool {
	marker := filepath.Join(filepath.Dir(constants.WildcardCertPath(g.cfg.DataDir)), legacyImportMarker)
	if _, err := os.Stat(marker); err == nil {
		return true
	}
	res, err := tlsstore.NewStore(g.sqlDB).ImportLegacy(ctx, sealKey, tlsstore.LegacyCaddyStorageDir)
	for _, skipped := range res.Skipped {
		g.logger.ComponentWarn(logging.ComponentGeneral, "Left a file out of the certificate import: it is not Caddy's",
			zap.String("file", skipped))
	}
	if err != nil {
		g.logger.ComponentError(logging.ComponentGeneral, "Could not import this node's certificates into the cluster's store; the store stays closed, and this node's Caddy keeps restarting until it opens",
			zap.Error(err))
		return false
	}
	if err := os.MkdirAll(filepath.Dir(marker), 0o750); err != nil {
		g.logger.ComponentError(logging.ComponentGeneral, "Could not record the certificate import", zap.Error(err))
		return false
	}
	if err := os.WriteFile(marker, nil, 0o600); err != nil {
		g.logger.ComponentError(logging.ComponentGeneral, "Could not record the certificate import", zap.Error(err))
		return false
	}
	g.logger.ComponentInfo(logging.ComponentGeneral, "Imported this node's certificates into the cluster's store",
		zap.Int("added", res.Added), zap.String("from", tlsstore.LegacyCaddyStorageDir))
	return true
}

// exportTLSOnce exports the wildcard if it changed. last is the state the
// previous run logged; a state is logged when it changes, not every minute.
func (g *Gateway) exportTLSOnce(ctx context.Context, exporter *tlsstore.Exporter, last string) string {
	wrote, err := exporter.Export(ctx)
	switch {
	case tlsstore.IsNotExist(err):
		if last != "missing" {
			g.logger.ComponentInfo(logging.ComponentGeneral, "The cluster's wildcard certificate is not in the store yet; TURNS waits for it",
				zap.String("base_domain", g.cfg.BaseDomain))
		}
		return "missing"
	case err != nil:
		if last != err.Error() {
			g.logger.ComponentWarn(logging.ComponentGeneral, "Could not export the cluster's wildcard certificate for TURN",
				zap.String("base_domain", g.cfg.BaseDomain), zap.Error(err))
		}
		return err.Error()
	case wrote:
		g.logger.ComponentInfo(logging.ComponentGeneral, "Exported the cluster's wildcard certificate for TURN",
			zap.String("cert_path", constants.WildcardCertPath(g.cfg.DataDir)))
	}
	return "exported"
}
