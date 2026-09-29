//go:build e2e_fleet

package deployments

import (
	"archive/tar"
	"bytes"
	"compress/gzip"
	"mime/multipart"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/gw"
)

// writeTree writes files (relative path -> content) under a fresh directory.
func writeTree(t testing.TB, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	for rel, body := range files {
		path := filepath.Join(dir, rel)
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	return dir
}

// staticSite is a two-page site; marker tells versions apart.
func staticSite(t testing.TB, marker string) string {
	t.Helper()
	return writeTree(t, map[string]string{
		"index.html":     "<!doctype html><title>e2e</title><p>" + marker + "</p>",
		"assets/app.css": "body{color:#123}",
		"data.json":      `{"marker":"` + marker + `"}`,
	})
}

// nextStaticExport is what `next build` with output: 'export' leaves in out/
// (docs/DEPLOYMENT_GUIDE.md "Static Next.js Export"): HTML pages plus _next/.
func nextStaticExport(t testing.TB, marker string) string {
	t.Helper()
	return writeTree(t, map[string]string{
		"index.html":                  "<!doctype html><div id=__next>" + marker + "</div>",
		"about.html":                  "<!doctype html><div id=__next>about</div>",
		"_next/static/chunks/main.js": "console.log('" + marker + "')",
		"_next/static/css/app.css":    "html{margin:0}",
		"404.html":                    "<!doctype html>not found",
	})
}

// nodeServer is a dependency-free Node.js HTTP server with /health, /version
// and /marker (whether an install script ran, see npm_test.go).
const nodeServer = `const http = require('http');
const fs = require('fs');
const path = require('path');
const version = 'VERSION';
http.createServer((req, res) => {
  const u = new URL(req.url, 'http://x');
  let body = {ok: true, detail: 'healthy'};
  if (u.pathname === '/version') body = {ok: true, detail: version};
  if (u.pathname === '/marker') {
    const marks = ['../node_modules/.pwned', 'node_modules/.pwned', 'pwned'].filter(p => fs.existsSync(path.join(__dirname, p)));
    body = {ok: marks.length === 0, detail: marks.join(',')};
  }
  res.setHeader('Content-Type', 'application/json');
  res.end(JSON.stringify(body));
}).listen(process.env.PORT);
`

// nodeApp writes a Node.js app with an empty node_modules, so the CLI does
// not run npm on the runner and the server does the install. pkg is the
// package.json.
func nodeApp(t testing.TB, version, pkg string) string {
	t.Helper()
	dir := writeTree(t, map[string]string{
		"package.json": pkg,
		"index.js":     replaceVersion(nodeServer, version),
	})
	if err := os.Mkdir(filepath.Join(dir, "node_modules"), 0o755); err != nil {
		t.Fatal(err)
	}
	return dir
}

func replaceVersion(src, version string) string {
	return string(bytes.Replace([]byte(src), []byte("'VERSION'"), []byte("'"+version+"'"), 1))
}

// plainPackage is a package.json with no dependencies and a main entry.
const plainPackage = `{"name":"e2e-app","version":"1.0.0","main":"index.js"}`

// tarball gzips files into a .tar.gz in memory.
func tarball(t testing.TB, files map[string]string) []byte {
	t.Helper()
	var buf bytes.Buffer
	gz := gzip.NewWriter(&buf)
	tw := tar.NewWriter(gz)
	names := make([]string, 0, len(files))
	for n := range files {
		names = append(names, n)
	}
	sort.Strings(names)
	for _, n := range names {
		body := files[n]
		if err := tw.WriteHeader(&tar.Header{Name: n, Mode: 0o644, Size: int64(len(body)), Typeflag: tar.TypeReg}); err != nil {
			t.Fatal(err)
		}
		if _, err := tw.Write([]byte(body)); err != nil {
			t.Fatal(err)
		}
	}
	if err := tw.Close(); err != nil {
		t.Fatal(err)
	}
	if err := gz.Close(); err != nil {
		t.Fatal(err)
	}
	return buf.Bytes()
}

// multipartReq builds a deployment upload (fields plus a tarball named
// filename, none when filename is empty) the way the CLI does
// (core/cmd/orama/internal/deployments uploadDeployment).
func multipartReq(fields map[string]string, filename string, tgz []byte) (gw.Req, error) {
	var body bytes.Buffer
	w := multipart.NewWriter(&body)
	for k, v := range fields {
		if err := w.WriteField(k, v); err != nil {
			return gw.Req{}, err
		}
	}
	if filename != "" {
		part, err := w.CreateFormFile("tarball", filename)
		if err != nil {
			return gw.Req{}, err
		}
		if _, err := part.Write(tgz); err != nil {
			return gw.Req{}, err
		}
	}
	if err := w.Close(); err != nil {
		return gw.Req{}, err
	}
	return gw.Req{Method: http.MethodPost, Header: http.Header{"Content-Type": {w.FormDataContentType()}}, Body: body.Bytes()}, nil
}

// upload posts a deployment upload as the admin member, for what the CLI
// cannot send.
func (tn *tenant) upload(t testing.TB, path string, fields map[string]string, filename string, tgz []byte) *gw.Response {
	t.Helper()
	req, err := multipartReq(fields, filename, tgz)
	if err != nil {
		t.Fatal(err)
	}
	req.Path, req.Bearer = path, tn.admin.Bearer
	return tn.n.Client.MustSend(t, req)
}
