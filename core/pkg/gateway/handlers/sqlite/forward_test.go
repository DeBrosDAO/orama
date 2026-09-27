package sqlite

import (
	"bytes"
	"context"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strconv"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"go.uber.org/zap"
)

func fillStringField(dest interface{}, field, value string) {
	rv := reflect.ValueOf(dest).Elem()
	elem := reflect.New(rv.Type().Elem()).Elem()
	f := elem.FieldByName(field)
	if f.IsValid() && f.CanSet() {
		f.SetString(value)
	}
	rv.Set(reflect.Append(rv, elem))
}

func forwardFixture(t *testing.T, overlayIP string) (*SQLiteHandler, *httptest.Server, *int) {
	t.Helper()
	hits := 0
	home := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits++
		if r.Header.Get("Authorization") != "Bearer session" {
			t.Errorf("forward dropped the caller credential")
		}
		if r.Header.Get(forwardHeader) != "1" {
			t.Errorf("forward did not mark itself, so the home would forward again")
		}
		body, _ := io.ReadAll(r.Body)
		if !bytes.Contains(body, []byte("proofdb")) {
			t.Errorf("forward body = %s", body)
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"rows_affected":1,"last_insert_id":4}`))
	}))
	t.Cleanup(home.Close)
	_, port, err := net.SplitHostPort(home.Listener.Addr().String())
	if err != nil {
		t.Fatal(err)
	}
	n, err := strconv.Atoi(port)
	if err != nil {
		t.Fatal(err)
	}
	prev := sqliteGatewayPort
	sqliteGatewayPort = n
	t.Cleanup(func() { sqliteGatewayPort = prev })

	h := NewSQLiteHandler(&mockRQLiteClient{
		QueryFunc: func(ctx context.Context, dest interface{}, query string, args ...interface{}) error {
			switch {
			case strings.Contains(query, "namespace_sqlite_databases"):
				fillStringField(dest, "HomeNodeID", "home-peer")
			case strings.Contains(query, "dns_nodes"):
				fillStringField(dest, "IP", overlayIP)
			}
			return nil
		},
	}, nil, zap.NewNop(), t.TempDir(), "local-peer")
	return h, home, &hits
}

func queryRequest(body string, forwarded bool) *http.Request {
	req := httptest.NewRequest(http.MethodPost, "/v1/db/sqlite/query", strings.NewReader(body))
	req.Header.Set("Authorization", "Bearer session")
	req.Header.Set("Content-Type", "application/json")
	if forwarded {
		req.Header.Set(forwardHeader, "1")
	}
	return req.WithContext(context.WithValue(req.Context(), ctxkeys.NamespaceOverride, "stagenetproof"))
}

func TestQueryDatabase_forwardsToTheHomeNode(t *testing.T) {
	h, _, hits := forwardFixture(t, "127.0.0.1")
	rr := httptest.NewRecorder()
	h.QueryDatabase(rr, queryRequest(`{"database_name":"proofdb","query":"SELECT 1"}`, false))

	if rr.Code != http.StatusOK {
		t.Fatalf("status %d body %s", rr.Code, rr.Body.String())
	}
	if !bytes.Contains(rr.Body.Bytes(), []byte(`"last_insert_id":4`)) {
		t.Fatalf("body %s", rr.Body.String())
	}
	if *hits != 1 {
		t.Fatalf("home was asked %d times", *hits)
	}
}

func TestQueryDatabase_aForwardedRequestIsNotForwardedAgain(t *testing.T) {
	h, _, hits := forwardFixture(t, "127.0.0.1")
	rr := httptest.NewRecorder()
	h.QueryDatabase(rr, queryRequest(`{"database_name":"proofdb","query":"SELECT 1"}`, true))

	if rr.Code != http.StatusMisdirectedRequest {
		t.Fatalf("status %d body %s", rr.Code, rr.Body.String())
	}
	if *hits != 0 {
		t.Fatalf("the home was asked again")
	}
}

func TestQueryDatabase_doesNotForwardToAPublicAddress(t *testing.T) {
	h, _, hits := forwardFixture(t, "8.8.8.8")
	rr := httptest.NewRecorder()
	h.QueryDatabase(rr, queryRequest(`{"database_name":"proofdb","query":"SELECT 1"}`, false))

	if rr.Code != http.StatusMisdirectedRequest {
		t.Fatalf("status %d body %s", rr.Code, rr.Body.String())
	}
	if *hits != 0 {
		t.Fatal("a public address was used as the home node")
	}
}
