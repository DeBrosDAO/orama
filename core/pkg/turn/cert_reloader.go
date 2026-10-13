package turn

import (
	"crypto/tls"
	"fmt"
	"os"
	"sync"
	"time"

	"go.uber.org/zap"
)

// turnCertReloadInterval is how often the TURNS certificate file is polled for
// changes. TLS cert renewals (Caddy DNS-01 for cdn.<base-domain>) happen on the
// order of weeks, so a minute of detection latency is irrelevant; polling keeps
// the dependency surface minimal (no fsnotify) and is robust across the
// atomic-rename pattern certbot/Caddy use when writing a renewed cert.
const turnCertReloadInterval = 60 * time.Second

// certReloader serves the current TURNS certificate through a tls.Config
// GetCertificate callback and hot-reloads it when the cert file changes on
// disk. This lets a Caddy-renewed certificate be picked up WITHOUT restarting
// the TURN server — a restart would tear down every active relay (~30s RTC
// drop for users mid-call). See plans/platform/04_STEALTH_TURN.md, the
// "cert renewal during cutover" note.
type certReloader struct {
	certPath string
	keyPath  string
	logger   *zap.Logger

	mu      sync.RWMutex
	cert    *tls.Certificate
	modTime time.Time
}

// newCertReloader loads the initial cert/key pair. Returns an error if the
// initial load fails — TURNS cannot start without a valid certificate.
func newCertReloader(certPath, keyPath string, logger *zap.Logger) (*certReloader, error) {
	r := &certReloader{certPath: certPath, keyPath: keyPath, logger: logger}
	if err := r.reload(); err != nil {
		return nil, err
	}
	return r, nil
}

// reload reads the cert/key pair from disk and atomically swaps it in. On
// failure it leaves the previously-loaded certificate in place: a renewal that
// momentarily presents a half-written or mismatched cert/key file must never
// take TURNS down — the old (still-valid) cert keeps serving until the next
// successful reload.
func (r *certReloader) reload() error {
	cert, err := tls.LoadX509KeyPair(r.certPath, r.keyPath)
	if err != nil {
		return fmt.Errorf("load TURNS cert/key (%s): %w", r.certPath, err)
	}
	var mod time.Time
	if fi, statErr := os.Stat(r.certPath); statErr == nil {
		mod = fi.ModTime()
	}
	r.mu.Lock()
	r.cert = &cert
	r.modTime = mod
	r.mu.Unlock()
	return nil
}

// GetCertificate is the tls.Config.GetCertificate callback. It always returns
// the most recently loaded certificate, so every new TLS handshake uses the
// current cert without the listener being recreated.
func (r *certReloader) GetCertificate(*tls.ClientHelloInfo) (*tls.Certificate, error) {
	r.mu.RLock()
	defer r.mu.RUnlock()
	return r.cert, nil
}

// watch polls the cert file's mtime every interval and reloads when it advances.
// Blocks until stop is closed.
func (r *certReloader) watch(interval time.Duration, stop <-chan struct{}) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			fi, err := os.Stat(r.certPath)
			if err != nil {
				// File briefly absent during an atomic rename — retry next tick.
				continue
			}
			r.mu.RLock()
			unchanged := !fi.ModTime().After(r.modTime)
			r.mu.RUnlock()
			if unchanged {
				continue
			}
			if err := r.reload(); err != nil {
				r.logger.Warn("TURNS cert reload failed; keeping previous certificate",
					zap.String("cert_path", r.certPath), zap.Error(err))
				continue
			}
			r.logger.Info("TURNS cert hot-reloaded", zap.String("cert_path", r.certPath))
		}
	}
}

// samePaths reports whether this reloader already serves the given cert/key
// pair, so a tenant reload can reuse it instead of re-reading the files.
func (r *certReloader) samePaths(certPath, keyPath string) bool {
	return r != nil && r.certPath == certPath && r.keyPath == keyPath
}
