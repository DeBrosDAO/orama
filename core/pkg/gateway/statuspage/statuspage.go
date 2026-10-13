// Package statuspage is the public status page a gateway serves at /status to
// a browser. It is static: the page's script reads /v1/status, which is where
// the data and its redaction live (cluster.PublicStatus).
package statuspage

import (
	"embed"
	"io/fs"
	"net/http"
)

// AssetsPrefix is where the page's script and stylesheet are served.
const AssetsPrefix = "/status/assets/"

//go:embed index.html assets
var files embed.FS

// contentSecurityPolicy lets the page load only its own script and style and
// talk only to its own origin. Nothing is inline, so no 'unsafe-inline'.
const contentSecurityPolicy = "default-src 'none'; script-src 'self'; style-src 'self'; " +
	"connect-src 'self'; img-src 'self' data:; base-uri 'none'; form-action 'none'; frame-ancestors 'none'"

// ServePage writes the page.
func ServePage(w http.ResponseWriter, r *http.Request) {
	page, err := files.ReadFile("index.html")
	if err != nil {
		http.Error(w, "status page missing from this build", http.StatusInternalServerError)
		return
	}
	setHeaders(w)
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusOK)
	_, _ = w.Write(page)
}

// Assets serves the page's script and stylesheet under AssetsPrefix.
func Assets() http.Handler {
	sub, err := fs.Sub(files, "assets")
	if err != nil {
		// The directory is embedded at build time; its absence is a build bug.
		panic("statuspage: assets not embedded: " + err.Error())
	}
	files := http.StripPrefix(AssetsPrefix, http.FileServer(http.FS(sub)))
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		setHeaders(w)
		files.ServeHTTP(w, r)
	})
}

func setHeaders(w http.ResponseWriter) {
	w.Header().Set("Content-Security-Policy", contentSecurityPolicy)
	w.Header().Set("X-Content-Type-Options", "nosniff")
	w.Header().Set("Referrer-Policy", "no-referrer")
	w.Header().Set("Cache-Control", "public, max-age=300")
}
