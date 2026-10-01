package process

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"go.uber.org/zap"
)

const listing = `orama-deploy-node@acme-web.service loaded active running Orama deployment
orama-deploy-go@acme-api.service loaded failed failed Orama deployment
orama-deploy-build@acme-web.service loaded inactive dead Orama build
orama-deploy-clean@acme-web.service loaded inactive dead Orama clean
`

func queryFake(show map[string]string, listErr error) func(context.Context, ...string) ([]byte, error) {
	return func(_ context.Context, args ...string) ([]byte, error) {
		if args[0] == "list-units" {
			return []byte(listing), listErr
		}
		return []byte(show[args[1]]), nil
	}
}

func TestListRuntimeUnits_runtimeOnlyAndDated(t *testing.T) {
	m := &Manager{logger: zap.NewNop(), query: queryFake(map[string]string{
		"orama-deploy-node@acme-web.service": "ActiveState=active\nActiveEnterTimestamp=Mon 2026-01-29 10:00:00 UTC\nInactiveExitTimestamp=Mon 2026-01-29 09:59:59 UTC\nStateChangeTimestamp=Mon 2026-01-29 10:00:00 UTC\n",
		"orama-deploy-go@acme-api.service":   "ActiveState=failed\nActiveEnterTimestamp=n/a\nInactiveExitTimestamp=Mon 2026-01-29 08:00:00 UTC\nStateChangeTimestamp=Mon 2026-01-29 08:05:00 UTC\n",
	}, nil)}
	units, err := m.ListRuntimeUnits(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(units) != 2 {
		t.Fatalf("got %d units, want the 2 runtime units (build@ and clean@ ignored): %+v", len(units), units)
	}
	if units[0].Runtime != RuntimeNode || units[0].Instance != "acme-web" {
		t.Errorf("unexpected first unit %+v", units[0])
	}
	if want := time.Date(2026, 1, 29, 8, 5, 0, 0, time.UTC); !units[1].Since.Equal(want) {
		t.Errorf("a failed unit is dated by its last state change: got %v want %v", units[1].Since, want)
	}
}

func TestListRuntimeUnits_undatedUnitIsAnError(t *testing.T) {
	m := &Manager{logger: zap.NewNop(), query: queryFake(map[string]string{}, nil)}
	units, err := m.ListRuntimeUnits(context.Background())
	if err == nil || len(units) != 0 {
		t.Fatalf("a unit with no timestamp was listed as old: %+v, %v", units, err)
	}
}

func TestListRuntimeUnits_listFailure(t *testing.T) {
	m := &Manager{logger: zap.NewNop(), query: queryFake(nil, errors.New("boom"))}
	if _, err := m.ListRuntimeUnits(context.Background()); err == nil {
		t.Fatal("a failed list-units was swallowed")
	}
}

func TestStopOrphan_stopsDisablesClearsSecretsAndDependencies(t *testing.T) {
	st := newRecordingStager()
	st.token["acme-web"] = "t"
	var calls []string
	m := &Manager{logger: zap.NewNop(), stager: st, systemctl: func(args ...string) error {
		calls = append(calls, strings.Join(args, " "))
		return nil
	}}
	if err := m.StopOrphan(RuntimeNode, "acme-web"); err != nil {
		t.Fatal(err)
	}
	want := []string{
		"stop orama-deploy-node@acme-web.service",
		"disable orama-deploy-node@acme-web.service",
		"start orama-deploy-clean@acme-web.service",
	}
	if strings.Join(calls, "|") != strings.Join(want, "|") {
		t.Errorf("calls %v, want %v", calls, want)
	}
	if _, ok := st.token["acme-web"]; ok {
		t.Error("the credential is still staged")
	}
}

func TestStopOrphan_goRuntimeHasNoDependencies(t *testing.T) {
	var calls []string
	m := &Manager{logger: zap.NewNop(), systemctl: func(args ...string) error { calls = append(calls, args[0]); return nil }}
	if err := m.StopOrphan(RuntimeGo, "acme-api"); err != nil {
		t.Fatal(err)
	}
	if strings.Join(calls, ",") != "stop,disable" {
		t.Errorf("calls %v", calls)
	}
}

func TestStopOrphan_errorsJoinedAndDependenciesKeptWhenStopFails(t *testing.T) {
	var calls []string
	m := &Manager{logger: zap.NewNop(), systemctl: func(args ...string) error {
		calls = append(calls, args[0])
		return errors.New("refused")
	}}
	err := m.StopOrphan(RuntimeNode, "acme-web")
	if err == nil || !strings.Contains(err.Error(), "stop ") || !strings.Contains(err.Error(), "disable ") {
		t.Fatalf("not every failure was returned: %v", err)
	}
	if strings.Contains(strings.Join(calls, ","), "start") {
		t.Error("dependencies were pulled from under a unit that is still running")
	}
}

func TestStopOrphan_invalidInstance(t *testing.T) {
	m := &Manager{logger: zap.NewNop(), systemctl: func(...string) error { t.Fatal("systemctl called"); return nil }}
	if err := m.StopOrphan(RuntimeNode, "../etc"); err == nil {
		t.Fatal("an invalid instance was accepted")
	}
}
