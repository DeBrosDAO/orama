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

	if err != nil || got != "v0-4-0" {
		t.Fatalf("CurrentUpgradePlan = %q, %v; want v0-4-0", got, err)
	}
}

func TestCurrentUpgradePlan_noPlanIsEmpty(t *testing.T) {
	for _, body := range []string{`{"plan":null}`, `{}`, `{"plan":{"name":""}}`} {
		srv := planServer(t, http.StatusOK, body)

		got, err := CurrentUpgradePlan(context.Background(), srv.URL, srv.Client())

		if err != nil || got != "" {
			t.Errorf("body %s: CurrentUpgradePlan = %q, %v; want no plan", body, got, err)
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
		t.Fatalf("CurrentUpgradePlan = %q, nil; a failed read must not look like no plan", got)
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
