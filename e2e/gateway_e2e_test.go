//go:build e2e

package e2e

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/gorilla/websocket"
)

func getEnv(key, def string) string {
	if v := strings.TrimSpace(os.Getenv(key)); v != "" {
		return v
	}
	return def
}

func requireAPIKey(t *testing.T) string {
	t.Helper()
	key := strings.TrimSpace(os.Getenv("GATEWAY_API_KEY"))
	if key == "" {
		t.Skip("GATEWAY_API_KEY not set; skipping gateway auth-required tests")
	}
	return key
}

func gatewayBaseURL() string {
	return getEnv("GATEWAY_BASE_URL", "http://127.0.0.1:6001")
}

func httpClient() *http.Client {
	return &http.Client{Timeout: 10 * time.Second}
}

func authHeader(key string) http.Header {
	h := http.Header{}
	h.Set("Authorization", "Bearer "+key)
	h.Set("Content-Type", "application/json")
	return h
}

func TestGateway_Health(t *testing.T) {
	base := gatewayBaseURL()
	resp, err := httpClient().Get(base + "/v1/health")
	if err != nil {
		t.Fatalf("health request error: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("unexpected status: %d", resp.StatusCode)
	}
	var body map[string]any
	if err := json.NewDecoder(resp.Body).Decode(&body); err != nil {
		t.Fatalf("decode: %v", err)
	}
	if body["status"] != "ok" {
		t.Fatalf("status not ok: %+v", body)
	}
}

func TestGateway_PubSub_WS_Echo(t *testing.T) {
	key := requireAPIKey(t)
	base := gatewayBaseURL()

	topic := fmt.Sprintf("e2e-ws-%d", time.Now().UnixNano())
	wsURL, hdr := toWSURL(base+"/v1/pubsub/ws?topic="+url.QueryEscape(topic)), http.Header{}
	hdr.Set("Authorization", "Bearer "+key)

	c, _, err := websocket.DefaultDialer.Dial(wsURL, hdr)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer c.Close()
	defer c.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))

	msg := []byte("hello-ws")
	if err := c.WriteMessage(websocket.TextMessage, msg); err != nil {
		t.Fatalf("ws write: %v", err)
	}

	_, data, err := c.ReadMessage()
	if err != nil {
		t.Fatalf("ws read: %v", err)
	}
	if string(data) != string(msg) {
		t.Fatalf("ws echo mismatch: %q", string(data))
	}
}

func TestGateway_PubSub_RestPublishToWS(t *testing.T) {
	key := requireAPIKey(t)
	base := gatewayBaseURL()

	topic := fmt.Sprintf("e2e-rest-%d", time.Now().UnixNano())
	wsURL, hdr := toWSURL(base+"/v1/pubsub/ws?topic="+url.QueryEscape(topic)), http.Header{}
	hdr.Set("Authorization", "Bearer "+key)
	c, _, err := websocket.DefaultDialer.Dial(wsURL, hdr)
	if err != nil {
		t.Fatalf("ws dial: %v", err)
	}
	defer c.Close()

	// Publish via REST
	payload := randomBytes(24)
	b64 := base64.StdEncoding.EncodeToString(payload)
	body := fmt.Sprintf(`{"topic":"%s","data_base64":"%s"}`, topic, b64)
	req, _ := http.NewRequest(http.MethodPost, base+"/v1/pubsub/publish", strings.NewReader(body))
	req.Header = authHeader(key)
	resp, err := httpClient().Do(req)
	if err != nil {
		t.Fatalf("publish do: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("publish status: %d", resp.StatusCode)
	}

	// Expect the message via WS
	_ = c.SetReadDeadline(time.Now().Add(5 * time.Second))
	_, data, err := c.ReadMessage()
	if err != nil {
		t.Fatalf("ws read: %v", err)
	}
	if string(data) != string(payload) {
		t.Fatalf("payload mismatch: %q != %q", string(data), string(payload))
	}

	// Topics list should include our topic (without namespace prefix)
	req2, _ := http.NewRequest(http.MethodGet, base+"/v1/pubsub/topics", nil)
	req2.Header = authHeader(key)
	resp2, err := httpClient().Do(req2)
	if err != nil {
		t.Fatalf("topics do: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("topics status: %d", resp2.StatusCode)
	}
	var tlist struct {
		Topics []string `json:"topics"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&tlist); err != nil {
		t.Fatalf("topics decode: %v", err)
	}
	found := false
	for _, tt := range tlist.Topics {
		if tt == topic {
			found = true
			break
		}
	}
	if !found {
		t.Fatalf("topic %s not found in topics list", topic)
	}
}

func TestGateway_Database_CreateQueryMigrate(t *testing.T) {
	key := requireAPIKey(t)
	base := gatewayBaseURL()

	// Create table
	schema := `CREATE TABLE IF NOT EXISTS e2e_items (id INTEGER PRIMARY KEY AUTOINCREMENT, name TEXT, created_at DATETIME DEFAULT CURRENT_TIMESTAMP)`
	body := fmt.Sprintf(`{"schema":%q}`, schema)
	req, _ := http.NewRequest(http.MethodPost, base+"/v1/rqlite/create-table", strings.NewReader(body))
	req.Header = authHeader(key)
	resp, err := httpClient().Do(req)
	if err != nil {
		t.Fatalf("create-table do: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create-table status: %d", resp.StatusCode)
	}

	// Insert via transaction (simulate migration/data seed)
	txBody := `{"statements":["INSERT INTO e2e_items(name) VALUES ('one')","INSERT INTO e2e_items(name) VALUES ('two')"]}`
	req, _ = http.NewRequest(http.MethodPost, base+"/v1/rqlite/transaction", strings.NewReader(txBody))
	req.Header = authHeader(key)
	resp, err = httpClient().Do(req)
	if err != nil {
		t.Fatalf("tx do: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("tx status: %d", resp.StatusCode)
	}

	// Query rows
	qBody := `{"sql":"SELECT name FROM e2e_items ORDER BY id ASC"}`
	req, _ = http.NewRequest(http.MethodPost, base+"/v1/rqlite/query", strings.NewReader(qBody))
	req.Header = authHeader(key)
	resp, err = httpClient().Do(req)
	if err != nil {
		t.Fatalf("query do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("query status: %d", resp.StatusCode)
	}
	var qr struct {
		Columns []string `json:"columns"`
		Rows    [][]any  `json:"rows"`
		Count   int      `json:"count"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&qr); err != nil {
		t.Fatalf("query decode: %v", err)
	}
	if qr.Count < 2 {
		t.Fatalf("expected at least 2 rows, got %d", qr.Count)
	}

	// Schema endpoint returns tables
	req, _ = http.NewRequest(http.MethodGet, base+"/v1/rqlite/schema", nil)
	req.Header = authHeader(key)
	resp2, err := httpClient().Do(req)
	if err != nil {
		t.Fatalf("schema do: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("schema status: %d", resp2.StatusCode)
	}
}

func TestGateway_Database_DropTable(t *testing.T) {
	key := requireAPIKey(t)
	base := gatewayBaseURL()

	table := fmt.Sprintf("e2e_tmp_%d", time.Now().UnixNano())
	schema := fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (id INTEGER PRIMARY KEY, note TEXT)", table)
	// create
	body := fmt.Sprintf(`{"schema":%q}`, schema)
	req, _ := http.NewRequest(http.MethodPost, base+"/v1/rqlite/create-table", strings.NewReader(body))
	req.Header = authHeader(key)
	resp, err := httpClient().Do(req)
	if err != nil {
		t.Fatalf("create-table do: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusCreated {
		t.Fatalf("create-table status: %d", resp.StatusCode)
	}
	// drop
	dbody := fmt.Sprintf(`{"table":%q}`, table)
	req, _ = http.NewRequest(http.MethodPost, base+"/v1/rqlite/drop-table", strings.NewReader(dbody))
	req.Header = authHeader(key)
	resp, err = httpClient().Do(req)
	if err != nil {
		t.Fatalf("drop-table do: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("drop-table status: %d", resp.StatusCode)
	}
	// verify not in schema
	req, _ = http.NewRequest(http.MethodGet, base+"/v1/rqlite/schema", nil)
	req.Header = authHeader(key)
	resp2, err := httpClient().Do(req)
	if err != nil {
		t.Fatalf("schema do: %v", err)
	}
	defer resp2.Body.Close()
	if resp2.StatusCode != http.StatusOK {
		t.Fatalf("schema status: %d", resp2.StatusCode)
	}
	var schemaResp struct {
		Tables []struct {
			Name string `json:"name"`
		} `json:"tables"`
	}
	if err := json.NewDecoder(resp2.Body).Decode(&schemaResp); err != nil {
		t.Fatalf("schema decode: %v", err)
	}
	for _, tbl := range schemaResp.Tables {
		if tbl.Name == table {
			t.Fatalf("table %s still present after drop", table)
		}
	}
}

func TestGateway_Database_RecreateWithFK(t *testing.T) {
	key := requireAPIKey(t)
	base := gatewayBaseURL()

	// base tables
	orgs := fmt.Sprintf("e2e_orgs_%d", time.Now().UnixNano())
	users := fmt.Sprintf("e2e_users_%d", time.Now().UnixNano())
	createOrgs := fmt.Sprintf(`{"schema":%q}`, fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (id INTEGER PRIMARY KEY, name TEXT)", orgs))
	createUsers := fmt.Sprintf(`{"schema":%q}`, fmt.Sprintf("CREATE TABLE IF NOT EXISTS %s (id INTEGER PRIMARY KEY, name TEXT, org_id INTEGER, age TEXT)", users))

	for _, body := range []string{createOrgs, createUsers} {
		req, _ := http.NewRequest(http.MethodPost, base+"/v1/rqlite/create-table", strings.NewReader(body))
		req.Header = authHeader(key)
		resp, err := httpClient().Do(req)
		if err != nil {
			t.Fatalf("create-table do: %v", err)
		}
		resp.Body.Close()
		if resp.StatusCode != http.StatusCreated {
			t.Fatalf("create-table status: %d", resp.StatusCode)
		}
	}
	// seed data
	txSeed := fmt.Sprintf(`{"statements":["INSERT INTO %s(id,name) VALUES (1,'org')","INSERT INTO %s(id,name,org_id,age) VALUES (1,'alice',1,'30')"]}`, orgs, users)
	req, _ := http.NewRequest(http.MethodPost, base+"/v1/rqlite/transaction", strings.NewReader(txSeed))
	req.Header = authHeader(key)
	resp, err := httpClient().Do(req)
	if err != nil {
		t.Fatalf("seed tx do: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("seed tx status: %d", resp.StatusCode)
	}

	// migrate: change users.age TEXT -> INTEGER and add FK to orgs(id)
	// Note: Some backends may not support connection-scoped BEGIN/COMMIT or PRAGMA via HTTP.
	// We apply the standard recreate pattern without explicit PRAGMAs/transaction.
	txMig := fmt.Sprintf(`{"statements":[
      "CREATE TABLE %s_new (id INTEGER PRIMARY KEY, name TEXT, org_id INTEGER, age INTEGER, FOREIGN KEY(org_id) REFERENCES %s(id) ON DELETE CASCADE)",
      "INSERT INTO %s_new (id,name,org_id,age) SELECT id,name,org_id, CAST(age AS INTEGER) FROM %s",
      "DROP TABLE %s",
      "ALTER TABLE %s_new RENAME TO %s"
    ]}`, users, orgs, users, users, users, users, users)
	req, _ = http.NewRequest(http.MethodPost, base+"/v1/rqlite/transaction", strings.NewReader(txMig))
	req.Header = authHeader(key)
	resp, err = httpClient().Do(req)
	if err != nil {
		t.Fatalf("mig tx do: %v", err)
	}
	resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("mig tx status: %d", resp.StatusCode)
	}

	// verify schema type change
	qBody := fmt.Sprintf(`{"sql":"PRAGMA table_info(%s)"}`, users)
	req, _ = http.NewRequest(http.MethodPost, base+"/v1/rqlite/query", strings.NewReader(qBody))
	req.Header = authHeader(key)
	resp, err = httpClient().Do(req)
	if err != nil {
		t.Fatalf("pragma do: %v", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		t.Fatalf("pragma status: %d", resp.StatusCode)
	}
	var qr struct {
		Columns []string `json:"columns"`
		Rows    [][]any  `json:"rows"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&qr); err != nil {
		t.Fatalf("pragma decode: %v", err)
	}
	// column order: cid,name,type,notnull,dflt_value,pk
	ageIsInt := false
	for _, row := range qr.Rows {
		if len(row) >= 3 && fmt.Sprintf("%v", row[1]) == "age" {
			tstr := strings.ToUpper(fmt.Sprintf("%v", row[2]))
			if strings.Contains(tstr, "INT") {
				ageIsInt = true
				break
			}
		}
	}
	if !ageIsInt {
		// Fallback: inspect CREATE TABLE SQL from sqlite_master
		qBody2 := fmt.Sprintf(`{"sql":"SELECT sql FROM sqlite_master WHERE type='table' AND name='%s'"}`, users)
		req2, _ := http.NewRequest(http.MethodPost, base+"/v1/rqlite/query", strings.NewReader(qBody2))
		req2.Header = authHeader(key)
		resp3, err := httpClient().Do(req2)
		if err != nil {
			t.Fatalf("sqlite_master do: %v", err)
		}
		defer resp3.Body.Close()
		if resp3.StatusCode != http.StatusOK {
			t.Fatalf("sqlite_master status: %d", resp3.StatusCode)
		}
		var qr2 struct {
			Rows [][]any `json:"rows"`
		}
		if err := json.NewDecoder(resp3.Body).Decode(&qr2); err != nil {
			t.Fatalf("sqlite_master decode: %v", err)
		}
		found := false
		for _, row := range qr2.Rows {
			if len(row) > 0 {
				sql := strings.ToUpper(fmt.Sprintf("%v", row[0]))
				if strings.Contains(sql, "AGE INT") || strings.Contains(sql, "AGE INTEGER") {
					found = true
					break
				}
			}
		}
		if !found {
			t.Fatalf("age column type not INTEGER after migration")
		}
	}
}

func toWSURL(httpURL string) string {
	u, err := url.Parse(httpURL)
	if err != nil {
		return httpURL
	}
	if u.Scheme == "https" {
		u.Scheme = "wss"
	} else {
		u.Scheme = "ws"
	}
	return u.String()
}

func randomBytes(n int) []byte {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return b
}
