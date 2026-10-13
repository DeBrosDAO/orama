package operator

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/updatepolicy"
)

func TestUpdateSettings_aNewClusterNotifiesOnTheStableChannelWithNoRepository(t *testing.T) {
	h, _, _ := settingsHandler(t, "0xoperator")
	w := httptest.NewRecorder()
	h.HandleSettings(w, walletRequest(http.MethodGet, "/v1/operator/settings", "0xoperator"))
	var got map[string]any
	if err := json.Unmarshal(w.Body.Bytes(), &got); err != nil {
		t.Fatal(err)
	}
	want := map[string]string{
		updatepolicy.KeyMode: "notify", updatepolicy.KeyChannel: "stable", updatepolicy.KeyWindow: "", updatepolicy.KeyRepo: "",
	}
	for key, v := range want {
		if got[key] != v {
			t.Errorf("%s = %v, want %q", key, got[key], v)
		}
	}
}

func TestUpdateSettings_setIsStoredAuditedAndShownBack(t *testing.T) {
	h, db, audit := settingsHandler(t, "0xoperator")
	for name, value := range map[string]string{
		"auto-update":    `{"value":"auto"}`,
		"update-channel": `{"value":"nightly"}`,
		"update-window":  `{"value":"1-5"}`,
		"release-repo":   `{"value":"https://releases.example.org/tuf"}`,
	} {
		w := httptest.NewRecorder()
		h.HandleSettings(w, putJSON(http.MethodPut, "/v1/operator/settings/"+name, "0xoperator", value))
		if w.Code != http.StatusOK {
			t.Fatalf("%s: %d %s", name, w.Code, w.Body.String())
		}
	}
	if n := settingCount(t, db, updatepolicy.KeyMode); n != 1 {
		t.Fatalf("auto_update rows = %d", n)
	}
	if len(audit.rows) != 4 {
		t.Fatalf("%d audit rows for 4 changes", len(audit.rows))
	}
	got, err := loadUpdateSettings(t.Context(), h.rqliteClient)
	if err != nil {
		t.Fatal(err)
	}
	if got[updatepolicy.KeyMode] != "auto" || got[updatepolicy.KeyChannel] != "nightly" ||
		got[updatepolicy.KeyWindow] != "1-5" || got[updatepolicy.KeyRepo] != "https://releases.example.org/tuf" {
		t.Fatalf("settings = %v", got)
	}
}

func TestUpdateSettings_aValueTheAgentCouldNotUseIsRefusedAndNotStored(t *testing.T) {
	h, db, audit := settingsHandler(t, "0xoperator")
	for name, value := range map[string]string{
		"auto-update":    `{"value":"sometimes"}`,
		"update-channel": `{"value":"Not A Channel"}`,
		"update-window":  `{"value":"night"}`,
		"release-repo":   `{"value":"http://releases.example.org"}`,
		"auto_update":    `{"value":7}`,
	} {
		w := httptest.NewRecorder()
		h.HandleSettings(w, putJSON(http.MethodPut, "/v1/operator/settings/"+name, "0xoperator", value))
		if w.Code != http.StatusBadRequest {
			t.Errorf("%s %s: status %d", name, value, w.Code)
		}
	}
	for _, key := range updatepolicy.Keys {
		if n := settingCount(t, db, key); n != 0 {
			t.Errorf("a refused value was stored for %s", key)
		}
	}
	if len(audit.rows) != 0 {
		t.Fatal("a refused change was audited")
	}
}

func TestUpdateSettings_aStoredValueTheAgentCannotUseIsAnErrorNotADefault(t *testing.T) {
	h, db, _ := settingsHandler(t, "0xoperator")
	if _, err := db.Exec(`INSERT INTO cluster_settings (key, value, updated_by) VALUES ('auto_update', 'maybe', 'test')`); err != nil {
		t.Fatal(err)
	}
	if _, err := loadUpdateSettings(t.Context(), h.rqliteClient); err == nil {
		t.Fatal("a stored mode the agent cannot use was read as the default")
	}
	w := httptest.NewRecorder()
	h.HandleSettings(w, walletRequest(http.MethodGet, "/v1/operator/settings", "0xoperator"))
	if w.Code != http.StatusInternalServerError {
		t.Fatalf("status %d, want 500 naming the bad row", w.Code)
	}
}

func TestUpdateSettings_aNonOperatorCannotSetThem(t *testing.T) {
	h, db, _ := settingsHandler(t, "0xoperator")
	w := httptest.NewRecorder()
	h.HandleSettings(w, putJSON(http.MethodPut, "/v1/operator/settings/auto-update", "0xstranger", `{"value":"auto"}`))
	if w.Code != http.StatusForbidden {
		t.Fatalf("status %d", w.Code)
	}
	if n := settingCount(t, db, updatepolicy.KeyMode); n != 0 {
		t.Fatal("a stranger set auto-update")
	}
}
