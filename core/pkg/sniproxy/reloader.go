package sniproxy

import (
	"os"
	"time"

	"go.uber.org/zap"
)

// DefaultRouteReloadInterval is the default poll cadence for a FileRouteReloader.
// SNI route changes (a namespace enabling/disabling the stealth-TURN path) are
// infrequent, so 30s of detection latency is fine — and polling keeps the
// dependency surface minimal (no fsnotify), matching the TURNS cert reloader.
const DefaultRouteReloadInterval = 30 * time.Second

// RouteSource produces the current route table + fallback backend. It returns
// an error when the underlying source (e.g. the YAML config file) is missing or
// invalid; on error the reloader KEEPS the routes already installed in the
// Router rather than dropping traffic for a bad edit.
type RouteSource func() (routes []Route, fallback Backend, err error)

// FileRouteReloader watches a config file's mtime and re-applies its routes to
// a Router when it changes — so the SNI route table can be updated (e.g. a new
// namespace's cdn/turn routes added) WITHOUT restarting the router. The
// Router's Replace swaps the table atomically while connections are in flight,
// so reloads are seamless. Mirrors the TURNS cert hot-reload pattern.
//
// modTime is only ever touched by the goroutine running Watch (after the
// synchronous startup Apply), so it needs no lock; the routes themselves live
// behind the Router's own mutex.
type FileRouteReloader struct {
	path    string
	source  RouteSource
	router  *Router
	logger  *zap.Logger
	modTime time.Time
}

// NewFileRouteReloader creates a reloader. source must read/parse the file at
// path; router receives the Replace calls.
func NewFileRouteReloader(path string, source RouteSource, router *Router, logger *zap.Logger) *FileRouteReloader {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &FileRouteReloader{path: path, source: source, router: router, logger: logger}
}

// Apply loads the routes from the source and atomically installs them in the
// Router, recording the config file's mtime. On a source error it returns the
// error and leaves the Router untouched.
func (r *FileRouteReloader) Apply() error {
	routes, fallback, err := r.source()
	if err != nil {
		return err
	}
	r.router.Replace(routes, fallback)
	if fi, statErr := os.Stat(r.path); statErr == nil {
		r.modTime = fi.ModTime()
	}
	return nil
}

// Watch polls the config file's mtime every interval and re-applies the routes
// when it advances. Blocks until stop is closed. A failed reload logs a warning
// and keeps the currently-installed routes (a bad edit must not blackhole
// traffic).
func (r *FileRouteReloader) Watch(interval time.Duration, stop <-chan struct{}) {
	ticker := time.NewTicker(interval)
	defer ticker.Stop()
	for {
		select {
		case <-stop:
			return
		case <-ticker.C:
			fi, err := os.Stat(r.path)
			if err != nil {
				// File briefly absent during an atomic rename — retry next tick.
				continue
			}
			if !fi.ModTime().After(r.modTime) {
				continue
			}
			if err := r.Apply(); err != nil {
				r.logger.Warn("SNI route reload failed; keeping current routes",
					zap.String("config_path", r.path), zap.Error(err))
				continue
			}
			r.logger.Info("SNI routes hot-reloaded",
				zap.String("config_path", r.path),
				zap.Int("routes", len(r.router.Routes())))
		}
	}
}
