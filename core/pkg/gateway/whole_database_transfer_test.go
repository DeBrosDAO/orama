package gateway

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	backuphandlers "github.com/DeBrosOfficial/network/pkg/gateway/handlers/backup"
	"github.com/DeBrosOfficial/network/pkg/nsbackup"
)

// fakeLoadGuard records the import's bracket.
type fakeLoadGuard struct {
	guardErr, finishErr, checkErr error
	guarded, finished, checked    atomic.Int32
	// finishCtxErr is the context's error as finish saw it.
	finishCtxErr atomic.Value
}

func (f *fakeLoadGuard) CheckImage(context.Context, string) error {
	f.checked.Add(1)
	return f.checkErr
}

func (f *fakeLoadGuard) GuardLoad(context.Context) (func(context.Context) error, error) {
	f.guarded.Add(1)
	if f.guardErr != nil {
		return nil, f.guardErr
	}
	return func(ctx context.Context) error {
		f.finished.Add(1)
		f.finishCtxErr.Store(fmt.Sprint(ctx.Err()))
		return f.finishErr
	}, nil
}

const sqliteHead = nsbackup.SQLiteMagic

// nsGateway is a namespace gateway whose RQLite is the server, with the
// owner's grant on every request it is given.
func nsGateway(t *testing.T, rqliteURL string, guard loadGuarder) *Gateway {
	t.Helper()
	return &Gateway{cfg: &Config{RQLiteDSN: rqliteURL, ClientNamespace: "anchat"}, logger: newRQLiteTestLogger(), loadGuard: guard}
}

func transferOwnerRequest(method, path string, body io.Reader) *http.Request {
	r := httptest.NewRequest(method, path, body)
	r.Header.Set("Content-Type", "application/octet-stream")
	ctx := context.WithValue(r.Context(), CtxKeyNamespaceOverride, "anchat")
	return markGrant(r.WithContext(ctx), &auth.Grant{Role: auth.RoleOwner})
}

func countingRQLite(t *testing.T) (*httptest.Server, *atomic.Int32) {
	t.Helper()
	var loads atomic.Int32
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		loads.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
	}))
	t.Cleanup(srv.Close)
	return srv, &loads
}

func TestRQLiteImport_scrubsAfterTheLoadAndNotBefore(t *testing.T) {
	srv, loads := countingRQLite(t)
	guard := &fakeLoadGuard{}
	g := nsGateway(t, srv.URL, guard)
	rec := httptest.NewRecorder()
	g.rqliteImportHandler(rec, transferOwnerRequest(http.MethodPost, "/v1/rqlite/import", strings.NewReader(sqliteHead+"rows")))
	if rec.Code != http.StatusOK || loads.Load() != 1 || guard.guarded.Load() != 1 || guard.finished.Load() != 1 {
		t.Fatalf("%d, loads %d, guarded %d, finished %d", rec.Code, loads.Load(), guard.guarded.Load(), guard.finished.Load())
	}
}

func TestRQLiteImport_aFailedGuardLoadsNothing(t *testing.T) {
	srv, loads := countingRQLite(t)
	g := nsGateway(t, srv.URL, &fakeLoadGuard{guardErr: errors.New("registry down")})
	rec := httptest.NewRecorder()
	g.rqliteImportHandler(rec, transferOwnerRequest(http.MethodPost, "/v1/rqlite/import", strings.NewReader(sqliteHead)))
	if rec.Code != http.StatusBadGateway || loads.Load() != 0 {
		t.Fatalf("%d, loads %d", rec.Code, loads.Load())
	}
}

func TestRQLiteImport_aFailedScrubIsAnErrorNotASuccess(t *testing.T) {
	srv, _ := countingRQLite(t)
	g := nsGateway(t, srv.URL, &fakeLoadGuard{finishErr: errors.New("boom")})
	rec := httptest.NewRecorder()
	g.rqliteImportHandler(rec, transferOwnerRequest(http.MethodPost, "/v1/rqlite/import", strings.NewReader(sqliteHead)))
	if rec.Code != http.StatusBadGateway || !strings.Contains(rec.Body.String(), "again is safe") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
}

func TestRQLiteImport_aNamespaceGatewayWithoutTheGuardRefuses(t *testing.T) {
	srv, loads := countingRQLite(t)
	g := nsGateway(t, srv.URL, nil)
	rec := httptest.NewRecorder()
	g.rqliteImportHandler(rec, transferOwnerRequest(http.MethodPost, "/v1/rqlite/import", strings.NewReader(sqliteHead)))
	if rec.Code != http.StatusServiceUnavailable || loads.Load() != 0 {
		t.Fatalf("%d, loads %d", rec.Code, loads.Load())
	}
}

// zeros yields n zero bytes after the SQLite header.
type zeros struct{ left int64 }

func (z *zeros) Read(p []byte) (int, error) {
	if z.left <= 0 {
		return 0, io.EOF
	}
	n := min(int64(len(p)), z.left)
	clear(p[:n])
	z.left -= n
	return int(n), nil
}

func TestRQLiteImport_isBounded(t *testing.T) {
	t.Run("announced", func(t *testing.T) {
		srv, loads := countingRQLite(t)
		g := nsGateway(t, srv.URL, &fakeLoadGuard{})
		r := transferOwnerRequest(http.MethodPost, "/v1/rqlite/import", unreadableBody{t})
		r.ContentLength = rqliteImportMaxBytes + 1
		rec := httptest.NewRecorder()
		g.rqliteImportHandler(rec, r)
		if rec.Code != http.StatusRequestEntityTooLarge || loads.Load() != 0 {
			t.Fatalf("%d, loads %d", rec.Code, loads.Load())
		}
	})
	t.Run("unannounced", func(t *testing.T) {
		srv, _ := countingRQLite(t)
		guard := &fakeLoadGuard{}
		g := nsGateway(t, srv.URL, guard)
		body := io.MultiReader(strings.NewReader(sqliteHead), &zeros{left: rqliteImportMaxBytes})
		r := transferOwnerRequest(http.MethodPost, "/v1/rqlite/import", body)
		r.ContentLength = -1
		rec := httptest.NewRecorder()
		g.rqliteImportHandler(rec, r)
		if rec.Code != http.StatusRequestEntityTooLarge || guard.finished.Load() != 0 {
			t.Fatalf("%d, finished %d: %s", rec.Code, guard.finished.Load(), rec.Body)
		}
	})
}

type unreadableBody struct{ t *testing.T }

func (u unreadableBody) Read([]byte) (int, error) {
	u.t.Error("the handler read the body of a request announced as over the limit")
	return 0, io.EOF
}

// Export and import share the gateway's one transfer slot, with backup and
// restore: an owner cannot run parallel whole-database requests.
func TestRQLiteExportAndImport_shareOneTransferSlot(t *testing.T) {
	release := make(chan struct{})
	entered := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		entered <- struct{}{}
		<-release
		_, _ = w.Write([]byte(sqliteHead))
	}))
	defer srv.Close()
	g := nsGateway(t, srv.URL, &fakeLoadGuard{})
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		g.rqliteExportHandler(httptest.NewRecorder(), transferOwnerRequest(http.MethodGet, "/v1/rqlite/export", nil))
	}()
	<-entered
	for name, serve := range map[string]func(http.ResponseWriter, *http.Request){
		"export": g.rqliteExportHandler, "import": g.rqliteImportHandler,
	} {
		method, path := http.MethodGet, "/v1/rqlite/export"
		if name == "import" {
			method, path = http.MethodPost, "/v1/rqlite/import"
		}
		rec := httptest.NewRecorder()
		serve(rec, transferOwnerRequest(method, path, strings.NewReader(sqliteHead)))
		if rec.Code != http.StatusTooManyRequests || rec.Header().Get("Retry-After") == "" {
			t.Errorf("a second %s while one runs answered %d", name, rec.Code)
		}
	}
	close(release)
	wg.Wait()
	rec := httptest.NewRecorder()
	g.rqliteExportHandler(rec, transferOwnerRequest(http.MethodGet, "/v1/rqlite/export", nil))
	if rec.Code == http.StatusTooManyRequests {
		t.Fatal("the slot was not released")
	}
}

// slowPost sends a POST to srv whose body takes bodyTime to arrive.
func slowPost(t *testing.T, url string, bodyTime time.Duration, body string) (int, error) {
	t.Helper()
	pr, pw := io.Pipe()
	go func() {
		_, _ = pw.Write([]byte(body))
		time.Sleep(bodyTime)
		_, _ = pw.Write([]byte("tail"))
		pw.Close()
	}()
	req, _ := http.NewRequest(http.MethodPost, url, pr)
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()
	return resp.StatusCode, nil
}

// The gateways' http.Server cuts a request's body off at its ReadTimeout (60s;
// 200ms here). The four whole-database routes move their deadlines and every
// other route keeps the server's.
func TestWholeDatabaseRoutes_slowBodiesAreNotCutOffAtTheServersTimeout(t *testing.T) {
	rqlite, _ := countingRQLite(t)
	g := nsGateway(t, rqlite.URL, &fakeLoadGuard{})
	mux := http.NewServeMux()
	mux.HandleFunc("/v1/rqlite/import", func(w http.ResponseWriter, r *http.Request) {
		r = markGrant(r.WithContext(context.WithValue(r.Context(), CtxKeyNamespaceOverride, "anchat")), &auth.Grant{Role: auth.RoleOwner})
		g.rqliteImportHandler(w, r)
	})
	for _, p := range []string{"/v1/namespace/backup", "/v1/namespace/restore", "/v1/rqlite/export", "/v1/other"} {
		mux.HandleFunc(p, func(w http.ResponseWriter, r *http.Request) {
			if !extendTransferDeadlines(w, r) {
				return
			}
			if _, err := io.ReadAll(r.Body); err != nil {
				http.Error(w, err.Error(), http.StatusRequestTimeout)
			}
		})
	}
	srv := httptest.NewUnstartedServer(mux)
	srv.Config.ReadTimeout = 200 * time.Millisecond
	srv.Start()
	defer srv.Close()

	for _, p := range []string{"/v1/rqlite/import", "/v1/namespace/backup", "/v1/namespace/restore", "/v1/rqlite/export"} {
		if status, err := slowPost(t, srv.URL+p, 600*time.Millisecond, sqliteHead); err != nil || status != http.StatusOK {
			t.Errorf("%s: a slow body was cut off (%d, %v)", p, status, err)
		}
	}
	if status, err := slowPost(t, srv.URL+"/v1/other", 600*time.Millisecond, sqliteHead); err == nil && status == http.StatusOK {
		t.Error("/v1/other: a body slower than the server's timeout was read in full; it must keep the server's timeouts")
	}
}

func TestIsWholeDatabasePath(t *testing.T) {
	for p, want := range map[string]bool{
		"/v1/namespace/backup": true, "/v1/namespace/restore": true, "/v1/rqlite/export": true, "/v1/rqlite/import": true,
		"/v1/namespace/restore-key": false, "/v1/rqlite/query": false, "/v1/storage/upload": false, "": false,
	} {
		if got := isWholeDatabasePath(p); got != want {
			t.Errorf("isWholeDatabasePath(%q) = %v", p, got)
		}
	}
}

// proxyToNamespaceGateway refuses an oversized restore before it sends a byte
// to a namespace gateway, and a client that goes away cancels what it sent.
func TestProxyToNamespaceGateway_refusesAnOversizedRestoreBeforeAnyOutboundRequest(t *testing.T) {
	var hits atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		_, _ = io.Copy(io.Discard, r.Body)
	}))
	defer upstream.Close()
	g := proxyGatewayTo(t, upstream)

	over := httptest.NewRequest(http.MethodPost, "/v1/namespace/restore", unreadableBody{t})
	over.ContentLength = int64(backuphandlers.MaxRestoreBytes) + 1
	rec := httptest.NewRecorder()
	g.proxyToNamespaceGateway(rec, over, "acme", namespaceProxyAuth{namespace: "acme"})
	if rec.Code != http.StatusRequestEntityTooLarge || hits.Load() != 0 {
		t.Fatalf("%d, %d outbound requests: %s", rec.Code, hits.Load(), rec.Body)
	}

	ok := httptest.NewRequest(http.MethodPost, "/v1/namespace/restore", strings.NewReader("small"))
	rec = httptest.NewRecorder()
	g.proxyToNamespaceGateway(rec, ok, "acme", namespaceProxyAuth{namespace: "acme"})
	if rec.Code != http.StatusOK || hits.Load() != 1 {
		t.Fatalf("a request within the limit: %d, %d outbound requests: %s", rec.Code, hits.Load(), rec.Body)
	}
}

func TestProxyToNamespaceGateway_aClientThatGoesAwayCancelsTheUpstreamRequest(t *testing.T) {
	cancelled := make(chan struct{})
	started := make(chan struct{})
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// The server notices a closed connection only once it has read the body.
		_, _ = io.Copy(io.Discard, r.Body)
		close(started)
		<-r.Context().Done()
		close(cancelled)
	}))
	defer upstream.Close()
	g := proxyGatewayTo(t, upstream)

	ctx, cancel := context.WithCancel(context.Background())
	r := httptest.NewRequest(http.MethodPost, "/v1/cache/put", strings.NewReader("x")).WithContext(ctx)
	done := make(chan struct{})
	go func() {
		g.proxyToNamespaceGateway(httptest.NewRecorder(), r, "acme", namespaceProxyAuth{namespace: "acme"})
		close(done)
	}()
	<-started
	cancel()
	select {
	case <-cancelled:
	case <-time.After(5 * time.Second):
		t.Fatal("the upstream request outlived the client that sent it")
	}
	<-done
}

// proxyGatewayTo is a gateway whose only namespace target is upstream.
func proxyGatewayTo(t *testing.T, upstream *httptest.Server) *Gateway {
	t.Helper()
	host, portText, _ := strings.Cut(strings.TrimPrefix(upstream.URL, "http://"), ":")
	port, err := strconv.Atoi(portText)
	if err != nil {
		t.Fatal(err)
	}
	g := &Gateway{logger: newRQLiteTestLogger(), cfg: &Config{}, mwCache: newMiddlewareCache(time.Minute),
		circuitBreakers: NewCircuitBreakerRegistry(), internalAuthKey: []byte("0123456789abcdef0123456789abcdef"),
		proxyTransport: http.DefaultTransport.(*http.Transport).Clone()}
	g.mwCache.SetNamespaceTargets("acme", []gatewayTarget{{ip: host, port: port}})
	return g
}

func TestRQLiteImport_anImageTheCheckRefusesIsNeverLoaded(t *testing.T) {
	srv, loads := countingRQLite(t)
	guard := &fakeLoadGuard{checkErr: fmt.Errorf("%w: it has a trigger (t)", backuphandlers.ErrImageRefused)}
	g := nsGateway(t, srv.URL, guard)
	rec := httptest.NewRecorder()
	g.rqliteImportHandler(rec, transferOwnerRequest(http.MethodPost, "/v1/rqlite/import", strings.NewReader(sqliteHead+"x")))
	if rec.Code != http.StatusBadRequest || !strings.Contains(rec.Body.String(), "trigger") {
		t.Fatalf("%d %s", rec.Code, rec.Body)
	}
	if loads.Load() != 0 || guard.guarded.Load() != 0 || guard.finished.Load() != 0 {
		t.Fatalf("loads %d, guarded %d, finished %d", loads.Load(), guard.guarded.Load(), guard.finished.Load())
	}
}

func TestRQLiteImport_aCheckThatCouldNotRunIsNotTheCallersFault(t *testing.T) {
	srv, loads := countingRQLite(t)
	g := nsGateway(t, srv.URL, &fakeLoadGuard{checkErr: errors.New("registry down")})
	rec := httptest.NewRecorder()
	g.rqliteImportHandler(rec, transferOwnerRequest(http.MethodPost, "/v1/rqlite/import", strings.NewReader(sqliteHead)))
	if rec.Code != http.StatusBadGateway || loads.Load() != 0 {
		t.Fatalf("%d, loads %d", rec.Code, loads.Load())
	}
}

// Once the image was handed to RQLite the scrub runs whatever came back, and
// not on the request's context: a client that went away cannot skip it.
func TestRQLiteImport_scrubsAfterEveryOutcomeOnceTheImageWasHandedOver(t *testing.T) {
	for name, tc := range map[string]struct {
		rqlite     func(cancel context.CancelFunc) http.HandlerFunc
		wantStatus int
	}{
		"client gone while RQLite loads": {func(cancel context.CancelFunc) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) { _, _ = io.Copy(io.Discard, r.Body); cancel() }
		}, http.StatusOK},
		"RQLite answers an error": {func(context.CancelFunc) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) {
				_, _ = io.Copy(io.Discard, r.Body)
				http.Error(w, "boom", 500)
			}
		}, http.StatusInternalServerError},
		"RQLite drops the connection": {func(context.CancelFunc) http.HandlerFunc {
			return func(w http.ResponseWriter, r *http.Request) { panic(http.ErrAbortHandler) }
		}, http.StatusBadGateway},
	} {
		t.Run(name, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			srv := httptest.NewServer(tc.rqlite(cancel))
			defer srv.Close()
			guard := &fakeLoadGuard{}
			g := nsGateway(t, srv.URL, guard)
			r := transferOwnerRequest(http.MethodPost, "/v1/rqlite/import", strings.NewReader(sqliteHead+"x")).WithContext(
				context.WithValue(ctx, CtxKeyNamespaceOverride, "anchat"))
			r = markGrant(r, &auth.Grant{Role: auth.RoleOwner})
			rec := httptest.NewRecorder()
			g.rqliteImportHandler(rec, r)
			if rec.Code != tc.wantStatus {
				t.Errorf("answered %d, want %d", rec.Code, tc.wantStatus)
			}
			if guard.finished.Load() != 1 {
				t.Fatalf("the scrub ran %d times", guard.finished.Load())
			}
			if got := guard.finishCtxErr.Load(); got != "<nil>" {
				t.Fatalf("the scrub ran on a cancelled context: %v", got)
			}
		})
	}
}

// The registry import on the cluster gateway is the operator's: streamed,
// not capped (its export is not), and not spooled or checked.
func TestRQLiteImport_theClusterGatewaysRegistryImportIsNotCapped(t *testing.T) {
	srv, loads := countingRQLite(t)
	g := &Gateway{cfg: &Config{RQLiteDSN: srv.URL}, logger: newRQLiteTestLogger(), loadGuard: &fakeLoadGuard{checkErr: errors.New("must not be asked")}}
	body := io.MultiReader(strings.NewReader(sqliteHead), &zeros{left: rqliteImportMaxBytes + 1 - int64(len(sqliteHead))})
	r := httptest.NewRequest(http.MethodPost, "/v1/rqlite/import", body)
	r.Header.Set("Content-Type", "application/octet-stream")
	r.ContentLength = rqliteImportMaxBytes + 1
	rec := httptest.NewRecorder()
	g.rqliteImportHandler(rec, r)
	if rec.Code != http.StatusOK || loads.Load() != 1 {
		t.Fatalf("%d, loads %d: %s", rec.Code, loads.Load(), rec.Body)
	}
}

// The budget an import started with may be spent by its check, load and scrub;
// the response gets its own, so a success is not reported as a dropped
// connection.
func TestRQLiteImport_theResponseGetsItsOwnDeadline(t *testing.T) {
	old := transferBudget
	transferBudget = 200 * time.Millisecond
	t.Cleanup(func() { transferBudget = old })

	slow := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		time.Sleep(500 * time.Millisecond) // longer than the budget
	}))
	defer slow.Close()
	g := nsGateway(t, slow.URL, &fakeLoadGuard{})
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		r = markGrant(r.WithContext(context.WithValue(r.Context(), CtxKeyNamespaceOverride, "anchat")), &auth.Grant{Role: auth.RoleOwner})
		g.rqliteImportHandler(w, r)
	}))
	srv.Config.WriteTimeout = 100 * time.Millisecond
	srv.Start()
	defer srv.Close()
	req, _ := http.NewRequest(http.MethodPost, srv.URL, strings.NewReader(sqliteHead+"x"))
	req.Header.Set("Content-Type", "application/octet-stream")
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		t.Fatalf("the client never saw the answer of an import that succeeded: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("%d", resp.StatusCode)
	}
}
