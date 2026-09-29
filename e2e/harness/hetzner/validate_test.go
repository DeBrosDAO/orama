package hetzner

import (
	"context"
	"fmt"
	"net/http"
	"strings"
	"testing"
)

const serverTypesJSON = `{"server_types":[
 {"name":"cx23","cores":2,"memory":4,"disk":40,"architecture":"x86","prices":[{"location":"nbg1"},{"location":"hel1"}]},
 {"name":"cx11","cores":1,"memory":2,"disk":20,"architecture":"x86","prices":[{"location":"nbg1"}]},
 {"name":"cx22","cores":2,"memory":4,"disk":40,"architecture":"x86","deprecation":{"unavailable_after":"2026-01-01"},"prices":[{"location":"nbg1"}]},
 {"name":"cax11","cores":2,"memory":4,"disk":40,"architecture":"arm","prices":[{"location":"nbg1"}]}
],"meta":{"pagination":{"next_page":null}}}`

var nodeReq = Requirements{MinCores: 2, MinMemoryGB: 2, MinDiskGB: 10, Architecture: "x86"}

func typesClient(t *testing.T) *Client {
	return newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(serverTypesJSON))
	}))
}

func TestValidateServerType_fits(t *testing.T) {
	if err := typesClient(t).ValidateServerType(context.Background(), "cx23", "nbg1", nodeReq); err != nil {
		t.Fatalf("cx23 in nbg1: %v", err)
	}
}

func TestValidateServerType_refusals(t *testing.T) {
	cases := map[string]string{
		"cx22":  "deprecated",
		"cx11":  "below the node minimum",
		"cax11": "below the node minimum",
		"nope":  "no server type",
	}
	for name, want := range cases {
		err := typesClient(t).ValidateServerType(context.Background(), name, "nbg1", nodeReq)
		if err == nil || !strings.Contains(err.Error(), want) || !strings.Contains(err.Error(), "cx23") {
			t.Errorf("%s: %v, want %q and the fitting cx23 named", name, err, want)
		}
	}
}

func TestValidateServerType_notOfferedInLocation(t *testing.T) {
	err := typesClient(t).ValidateServerType(context.Background(), "cx23", "ash", nodeReq)
	if err == nil || !strings.Contains(err.Error(), "not offered in ash") || !strings.Contains(err.Error(), "(none)") {
		t.Fatalf("cx23 in ash: %v", err)
	}
}

func TestValidateLocation(t *testing.T) {
	c := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"locations":[{"name":"nbg1"},{"name":"hel1"}]}`))
	}))
	if err := c.ValidateLocation(context.Background(), "hel1"); err != nil {
		t.Fatalf("hel1: %v", err)
	}
	err := c.ValidateLocation(context.Background(), "mars1")
	if err == nil || !strings.Contains(err.Error(), "hel1, nbg1") {
		t.Fatalf("mars1: %v", err)
	}
}

func serversClient(t *testing.T, n int) *Client {
	var items []string
	for i := 0; i < n; i++ {
		items = append(items, fmt.Sprintf(`{"id":%d}`, i+1))
	}
	body := `{"servers":[` + strings.Join(items, ",") + `],"meta":{"pagination":{"next_page":null}}}`
	return newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(body))
	}))
}

func TestCheckCapacity(t *testing.T) {
	if err := serversClient(t, 0).CheckCapacity(context.Background(), 4, DefaultProjectServerLimit); err != nil {
		t.Fatalf("empty project: %v", err)
	}
	if err := serversClient(t, 6).CheckCapacity(context.Background(), 4, DefaultProjectServerLimit); err != nil {
		t.Fatalf("exactly at the limit: %v", err)
	}
	err := serversClient(t, 7).CheckCapacity(context.Background(), 4, DefaultProjectServerLimit)
	if err == nil || !strings.Contains(err.Error(), "holds 7 servers") {
		t.Fatalf("over the limit: %v", err)
	}
}
