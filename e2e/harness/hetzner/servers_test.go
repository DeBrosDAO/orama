package hetzner

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

func TestCreateServer_sendsLabelsKeyAndFirewall(t *testing.T) {
	var got createServerBody
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != "POST" || r.URL.Path != "/servers" {
			t.Errorf("%s %s", r.Method, r.URL.Path)
		}
		raw, _ := io.ReadAll(r.Body)
		_ = json.Unmarshal(raw, &got)
		_, _ = w.Write([]byte(`{"server":{"id":11,"name":"e2e-x-n1","status":"initializing"}}`))
	}))
	s, err := c.CreateServer(context.Background(), CreateServerOpts{
		Name: "e2e-x-n1", ServerType: "cx23", Image: "ubuntu-24.04", Location: "nbg1",
		SSHKeyID: 5, FirewallID: 9, Labels: map[string]string{LabelRun: "x"}, UserData: "#cloud-config\n",
	})
	if err != nil || s.ID != 11 {
		t.Fatalf("CreateServer: %v", err)
	}
	if got.Labels[LabelRun] != "x" || len(got.SSHKeys) != 1 || got.SSHKeys[0] != 5 ||
		len(got.Firewalls) != 1 || got.Firewalls[0].Firewall != 9 || !got.Start || got.UserData != "#cloud-config\n" {
		t.Fatalf("request body %+v", got)
	}
}

func TestCreateServer_noFirewall(t *testing.T) {
	var raw []byte
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		raw, _ = io.ReadAll(r.Body)
		_, _ = w.Write([]byte(`{"server":{"id":1}}`))
	}))
	if _, err := c.CreateServer(context.Background(), CreateServerOpts{Name: "a", SSHKeyID: 1}); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "firewalls") || strings.Contains(string(raw), "user_data") {
		t.Fatalf("body names a firewall or user data: %s", raw)
	}
}

func TestCreateServer_refusesOversizedUserData(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		t.Error("an oversized request reached the API")
	}))
	_, err := c.CreateServer(context.Background(), CreateServerOpts{Name: "a", SSHKeyID: 1, UserData: strings.Repeat("x", maxUserDataBytes+1)})
	if err == nil || !strings.Contains(err.Error(), "user data") {
		t.Fatalf("CreateServer with oversized user data: %v", err)
	}
}

func TestListServers_followsPagesWithSelector(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("label_selector") != "e2e-run=abc" {
			t.Errorf("selector %q", r.URL.Query().Get("label_selector"))
		}
		if r.URL.Query().Get("page") == "1" {
			_, _ = w.Write([]byte(`{"servers":[{"id":1}],"meta":{"pagination":{"next_page":2}}}`))
			return
		}
		_, _ = w.Write([]byte(`{"servers":[{"id":2}],"meta":{"pagination":{"next_page":null}}}`))
	}))
	servers, err := c.ListServers(context.Background(), "e2e-run=abc")
	if err != nil || len(servers) != 2 || servers[1].ID != 2 {
		t.Fatalf("ListServers: %v %+v", err, servers)
	}
}

func TestListServers_emptyProject(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"servers":[],"meta":{"pagination":{"next_page":null}}}`))
	}))
	servers, err := c.ListServers(context.Background(), "")
	if err != nil || len(servers) != 0 {
		t.Fatalf("ListServers: %v %+v", err, servers)
	}
}

func TestListServers_nonAdvancingPageFails(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"servers":[],"meta":{"pagination":{"next_page":1}}}`))
	}))
	if _, err := c.ListServers(context.Background(), ""); err == nil {
		t.Fatal("a pagination loop was followed")
	}
}

func TestDeleteServer_alreadyGoneIsNotAnError(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(errorBody("not_found", "gone")))
	}))
	if err := c.DeleteServer(context.Background(), 4); err != nil {
		t.Fatalf("DeleteServer of a gone server: %v", err)
	}
}

func TestDeleteServer_serverErrorIsReported(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
	}))
	if err := c.DeleteServer(context.Background(), 4); err == nil {
		t.Fatal("a 500 on delete was swallowed")
	}
}

func TestWaitRunning_pollsUntilRunningWithIP(t *testing.T) {
	var calls atomic.Int32
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 3 {
			_, _ = w.Write([]byte(`{"server":{"id":1,"status":"initializing"}}`))
			return
		}
		_, _ = w.Write([]byte(`{"server":{"id":1,"status":"running","public_net":{"ipv4":{"ip":"203.0.113.9"}}}}`))
	}))
	s, err := c.WaitRunning(context.Background(), 1, 10*time.Millisecond)
	if err != nil || s.IPv4() != "203.0.113.9" {
		t.Fatalf("WaitRunning: %v", err)
	}
}

func TestWaitRunning_deadline(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"server":{"id":1,"status":"starting"}}`))
	}))
	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	_, err := c.WaitRunning(ctx, 1, 10*time.Millisecond)
	if err == nil || !strings.Contains(err.Error(), "starting") {
		t.Fatalf("WaitRunning past its deadline: %v", err)
	}
}

func TestWaitGone_untilNotFound(t *testing.T) {
	var calls atomic.Int32
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) < 2 {
			_, _ = w.Write([]byte(`{"server":{"id":1,"status":"deleting"}}`))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	if err := c.WaitGone(context.Background(), 1, 10*time.Millisecond); err != nil {
		t.Fatalf("WaitGone: %v", err)
	}
}

func TestSSHKeysAndFirewalls_roundTrip(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("POST /ssh_keys", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"ssh_key":{"id":21,"name":"k"}}`))
	})
	mux.HandleFunc("GET /firewalls", func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"firewalls":[{"id":31}],"meta":{"pagination":{"next_page":null}}}`))
	})
	mux.HandleFunc("DELETE /firewalls/31", func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	})
	c := newTestClient(t, mux)
	k, err := c.CreateSSHKey(context.Background(), "k", "ssh-ed25519 AAAA", map[string]string{LabelRun: "x"})
	if err != nil || k.ID != 21 {
		t.Fatalf("CreateSSHKey: %v", err)
	}
	fws, err := c.ListFirewalls(context.Background(), LabelRun+"=x")
	if err != nil || len(fws) != 1 {
		t.Fatalf("ListFirewalls: %v", err)
	}
	if err := c.DeleteFirewall(context.Background(), 31); err != nil {
		t.Fatalf("DeleteFirewall of a gone firewall: %v", err)
	}
}
