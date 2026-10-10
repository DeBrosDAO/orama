package globalnode

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func planServer(t *testing.T, status int, body string) *httptest.Server {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/cosmos/upgrade/v1beta1/current_plan" {
			t.Errorf("read %s, want x/upgrade's current_plan", r.URL.Path)
		}
		w.WriteHeader(status)
		_, _ = w.Write([]byte(body))
	}))
	t.Cleanup(srv.Close)
	return srv
}

func TestCurrentUpgradePlan_scheduledPlanIsNamed(t *testing.T) {
	srv := planServer(t, http.StatusOK, `{"plan":{"name":"v0-4-0","time":"0001-01-01T00:00:00Z","height":"123"}}`)

	got, err := CurrentUpgradePlan(context.Background(), srv.URL, srv.Client())

	if err != nil || got.Name != "v0-4-0" {
		t.Fatalf("CurrentUpgradePlan = %+v, %v; want v0-4-0", got, err)
	}
}

func TestCurrentUpgradePlan_noPlanIsEmpty(t *testing.T) {
	for _, body := range []string{`{"plan":null}`, `{}`, `{"plan":{"name":""}}`} {
		srv := planServer(t, http.StatusOK, body)

		got, err := CurrentUpgradePlan(context.Background(), srv.URL, srv.Client())

		if err != nil || got.Name != "" {
			t.Errorf("body %s: CurrentUpgradePlan = %+v, %v; want no plan", body, got, err)
		}
	}
}

func TestCurrentUpgradePlan_aNameCosmovisorCannotStageIsRefused(t *testing.T) {
	srv := planServer(t, http.StatusOK, `{"plan":{"name":"V2 Upgrade/../x"}}`)

	_, err := CurrentUpgradePlan(context.Background(), srv.URL, srv.Client())

	if err == nil || !strings.Contains(err.Error(), "cosmovisor cannot stage") {
		t.Fatalf("err = %v, want the unusable name refused", err)
	}
}

func TestCurrentUpgradePlan_chainErrorsAreNotAnAbsentPlan(t *testing.T) {
	srv := planServer(t, http.StatusInternalServerError, `boom`)

	if got, err := CurrentUpgradePlan(context.Background(), srv.URL, srv.Client()); err == nil {
		t.Fatalf("CurrentUpgradePlan = %+v, nil; a failed read must not look like no plan", got)
	}
}

func TestCurrentUpgradePlan_malformedAnswer(t *testing.T) {
	srv := planServer(t, http.StatusOK, `<html>`)

	if _, err := CurrentUpgradePlan(context.Background(), srv.URL, srv.Client()); err == nil || !strings.Contains(err.Error(), "did not answer JSON") {
		t.Fatalf("err = %v, want the non-JSON answer named", err)
	}
}

func TestCurrentUpgradePlan_unreachableChain(t *testing.T) {
	srv := planServer(t, http.StatusOK, `{}`)
	base := srv.URL
	srv.Close()

	if _, err := CurrentUpgradePlan(context.Background(), base, nil); err == nil {
		t.Fatal("an unreachable chain must be an error, not no plan")
	}
}

func TestCurrentUpgradePlan_carriesTheInfo(t *testing.T) {
	srv := planServer(t, http.StatusOK, `{"plan":{"name":"v2","info":"{\"binaries\":{}}"}}`)

	got, err := CurrentUpgradePlan(context.Background(), srv.URL, srv.Client())

	if err != nil || got.Info != `{"binaries":{}}` {
		t.Fatalf("plan = %+v, %v", got, err)
	}
}

func TestUpgradePlan_BinaryChecksum(t *testing.T) {
	sum := strings.Repeat("ab", 32)
	tests := []struct {
		name  string
		info  string
		want  string
		named bool
	}{
		{"the platform's binary", `{"binaries":{"linux/amd64":"https://x.example/oramad?checksum=sha256:` + sum + `"}}`, sum, true},
		{"any platform", `{"binaries":{"any":"https://x.example/oramad?checksum=sha256:` + strings.ToUpper(sum) + `"}}`, sum, true},
		{"another platform only", `{"binaries":{"linux/arm64":"https://x.example/oramad?checksum=sha256:` + sum + `"}}`, "", false},
		{"no checksum in the url", `{"binaries":{"linux/amd64":"https://x.example/oramad"}}`, "", false},
		{"free text", `upgrade to 0.4.0`, "", false},
		{"empty", ``, "", false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, named := UpgradePlan{Info: tt.info}.BinaryChecksum("linux/amd64")
			if got != tt.want || named != tt.named {
				t.Errorf("BinaryChecksum = %q, %v; want %q, %v", got, named, tt.want, tt.named)
			}
		})
	}
}
