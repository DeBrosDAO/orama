package gateway

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	nodeauth "github.com/DeBrosOfficial/network/pkg/auth"
	"github.com/DeBrosOfficial/network/pkg/client"
	"github.com/DeBrosOfficial/network/pkg/logging"
)

// acmeQueryDB records Query calls so the present/cleanup handlers can be
// exercised without a real rqlite.
type acmeQueryDB struct {
	client.DatabaseClient
	calls int
}

func (d *acmeQueryDB) Query(_ context.Context, _ string, _ ...interface{}) (*client.QueryResult, error) {
	d.calls++
	return &client.QueryResult{}, nil
}

const (
	acmeTestSecret = "cluster-secret-for-acme-tests"
	acmeTestZone   = "dbrs.space"
	// acmeTestValue is the shape of a DNS-01 answer: 43 base64url characters.
	acmeTestValue = "LoqXcYV8q5ONbJQxbmR7SCTNo3tiAXDfowyjxAjEuX0"
)

func acmeGateway(t *testing.T, db client.DatabaseClient) *Gateway {
	t.Helper()
	log, err := logging.NewColoredLogger(logging.ComponentGateway, false)
	if err != nil {
		t.Fatalf("logger: %v", err)
	}
	return &Gateway{
		logger: log,
		client: &fakeNetworkClient{db: db},
		cfg:    &Config{ClusterSecret: acmeTestSecret, BaseDomain: acmeTestZone},
	}
}

// acmeRequest is a present or cleanup call from remote, signed with key when
// key is non-nil.
func acmeRequest(t *testing.T, path, remote string, key []byte, body any) *http.Request {
	t.Helper()
	var buf bytes.Buffer
	if err := json.NewEncoder(&buf).Encode(body); err != nil {
		t.Fatalf("encode: %v", err)
	}
	raw := buf.Bytes()
	r := httptest.NewRequest(http.MethodPost, path, bytes.NewReader(raw))
	r.RemoteAddr = remote
	r.Header.Set("Content-Type", "application/json")
	if key != nil {
		if err := nodeauth.SignACME(key, r, raw, time.Now()); err != nil {
			t.Fatalf("sign: %v", err)
		}
	}
	return r
}

func caddyKey(t *testing.T) []byte {
	t.Helper()
	key, err := nodeauth.ACMEChallengeKey(acmeTestSecret)
	if err != nil {
		t.Fatal(err)
	}
	return key
}

type acmeHandler func(*Gateway) http.HandlerFunc

var acmeEndpoints = map[string]acmeHandler{
	"/v1/internal/acme/present": func(g *Gateway) http.HandlerFunc { return g.acmePresentHandler },
	"/v1/internal/acme/cleanup": func(g *Gateway) http.HandlerFunc { return g.acmeCleanupHandler },
}

// The finding: loopback with no forwarding header was the whole check, and
// every process on the node — a tenant's deployment included — passes it. So
// any of them could publish a TXT record and be issued a certificate for any
// name under the cluster's domain.
func TestACME_refusesAnUnsignedCallerOnLoopback(t *testing.T) {
	for path, h := range acmeEndpoints {
		t.Run(path, func(t *testing.T) {
			db := &acmeQueryDB{}
			g := acmeGateway(t, db)
			rec := httptest.NewRecorder()
			h(g)(rec, acmeRequest(t, path, "127.0.0.1:54321", nil,
				ACMERequest{FQDN: "_acme-challenge." + acmeTestZone + ".", Value: acmeTestValue}))
			if rec.Code != http.StatusNotFound {
				t.Fatalf("status = %d, want 404", rec.Code)
			}
			if db.calls != 0 {
				t.Fatalf("an unsigned local process touched %d DNS rows", db.calls)
			}
		})
	}
}

// A MAC made with any other key — the coordination key every node holds
// included — is not Caddy's.
func TestACME_refusesAMACUnderAnotherKey(t *testing.T) {
	other, err := nodeauth.CoordinationKey(acmeTestSecret)
	if err != nil {
		t.Fatal(err)
	}
	db := &acmeQueryDB{}
	g := acmeGateway(t, db)
	rec := httptest.NewRecorder()
	g.acmePresentHandler(rec, acmeRequest(t, "/v1/internal/acme/present", "127.0.0.1:54321", other,
		ACMERequest{FQDN: "_acme-challenge." + acmeTestZone + ".", Value: acmeTestValue}))
	if rec.Code != http.StatusNotFound || db.calls != 0 {
		t.Fatalf("status %d, %d writes: the coordination key published a challenge", rec.Code, db.calls)
	}
}

func TestACME_acceptsCaddy(t *testing.T) {
	for path, h := range acmeEndpoints {
		t.Run(path, func(t *testing.T) {
			db := &acmeQueryDB{}
			g := acmeGateway(t, db)
			rec := httptest.NewRecorder()
			h(g)(rec, acmeRequest(t, path, "127.0.0.1:54321", caddyKey(t),
				ACMERequest{FQDN: "_acme-challenge.ns-alice." + acmeTestZone + ".", Value: acmeTestValue}))
			if rec.Code != http.StatusOK {
				t.Fatalf("status = %d, want 200: %s", rec.Code, rec.Body.String())
			}
			if db.calls != 1 {
				t.Fatalf("Query calls = %d, want 1", db.calls)
			}
		})
	}
}

// Even a signed caller writes only a challenge record, only in this cluster's
// zone: the endpoint is not a general way to publish DNS.
func TestACME_refusesRecordsItHasNoBusinessWriting(t *testing.T) {
	for name, req := range map[string]ACMERequest{
		"not a challenge record":   {FQDN: "www." + acmeTestZone + ".", Value: acmeTestValue},
		"another zone":             {FQDN: "_acme-challenge.example.com.", Value: acmeTestValue},
		"a zone ending in ours":    {FQDN: "_acme-challenge.evil" + acmeTestZone + ".", Value: acmeTestValue},
		"a label that is not one":  {FQDN: "_acme-challenge.a..b." + acmeTestZone + ".", Value: acmeTestValue},
		"a value that is not one":  {FQDN: "_acme-challenge." + acmeTestZone + ".", Value: "v=spf1 include:evil -all"},
		"a quote in the value":     {FQDN: "_acme-challenge." + acmeTestZone + ".", Value: `"` + acmeTestValue[1:]},
		"a challenge inside a tag": {FQDN: "_acme-challenge._acme-challenge." + acmeTestZone + ".", Value: acmeTestValue},
	} {
		t.Run(name, func(t *testing.T) {
			db := &acmeQueryDB{}
			g := acmeGateway(t, db)
			rec := httptest.NewRecorder()
			g.acmePresentHandler(rec, acmeRequest(t, "/v1/internal/acme/present", "127.0.0.1:54321", caddyKey(t), req))
			if rec.Code != http.StatusBadRequest {
				t.Fatalf("status = %d, want 400", rec.Code)
			}
			if db.calls != 0 {
				t.Fatalf("wrote %d rows for %+v", db.calls, req)
			}
		})
	}
}

// A gateway with no base domain serves no zone, so there is nothing to publish.
func TestACME_noBaseDomainPublishesNothing(t *testing.T) {
	db := &acmeQueryDB{}
	g := acmeGateway(t, db)
	g.cfg.BaseDomain = ""
	rec := httptest.NewRecorder()
	g.acmePresentHandler(rec, acmeRequest(t, "/v1/internal/acme/present", "127.0.0.1:54321", caddyKey(t),
		ACMERequest{FQDN: "_acme-challenge." + acmeTestZone + ".", Value: acmeTestValue}))
	if rec.Code != http.StatusBadRequest || db.calls != 0 {
		t.Fatalf("status %d, %d writes with no base domain", rec.Code, db.calls)
	}
}

func TestACMEChallengeFQDN_normalises(t *testing.T) {
	got, err := acmeChallengeFQDN(" _ACME-Challenge.Node-1.DBRS.space ", "dbrs.space.")
	if err != nil {
		t.Fatal(err)
	}
	if got != "_acme-challenge.node-1.dbrs.space." {
		t.Errorf("got %q", got)
	}
}
