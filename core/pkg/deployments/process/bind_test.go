package process

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/pkg/deployments"
	"github.com/DeBrosOfficial/network/pkg/privhelper"
	"go.uber.org/zap"
)

// A deployment may bind the port it was allocated and no other (bugboard
// 2719). The templates deny every bind; the port is a drop-in per instance
// that the helper writes when the gateway asks.

// systemdManager is a Manager that drives systemd through a seam which
// records into the stager's log, so a test sees one ordered history.
func systemdManager(st *recordingStager) *Manager {
	m := &Manager{logger: zap.NewNop(), stager: st, useSystemd: true}
	m.systemctl = func(args ...string) error {
		st.log = append(st.log, "systemctl "+strings.Join(args, " "))
		return nil
	}
	m.SetWorkloadTokenMinter(func(context.Context, string, string) (string, error) { return "token", nil })
	return m
}

func indexOf(log []string, entry string) int {
	for i, e := range log {
		if e == entry {
			return i
		}
	}
	return -1
}

// Each runtime's unit is allowed the deployment's own port, and allowed it
// before systemd is asked to start it: a unit started first would find the
// template's deny-all and fail to listen.
func TestStart_allowsTheUnitItsOwnPortBeforeStartingIt(t *testing.T) {
	for _, tc := range []struct {
		deployment *deployments.Deployment
		runtime    Runtime
	}{
		{&deployments.Deployment{Namespace: "acme", Name: "web", Type: deployments.DeploymentTypeNextJS, Port: 10200}, RuntimeNode},
		{&deployments.Deployment{Namespace: "acme", Name: "web", Type: deployments.DeploymentTypeNodeJSBackend, Port: 10201,
			Environment: map[string]string{"ENTRY_POINT": "npm:start"}}, RuntimeNPM},
		{&deployments.Deployment{Namespace: "acme", Name: "web", Type: deployments.DeploymentTypeGoBackend, Port: 19999}, RuntimeGo},
	} {
		t.Run(string(tc.runtime), func(t *testing.T) {
			st := newRecordingStager()
			if err := systemdManager(st).Start(context.Background(), tc.deployment, "/unused"); err != nil {
				t.Fatalf("Start: %v", err)
			}
			if want := fmt.Sprintf("%s:%d", tc.runtime, tc.deployment.Port); st.ports["acme-web"] != want {
				t.Errorf("allowed %q, want %q", st.ports["acme-web"], want)
			}
			bind := indexOf(st.log, fmt.Sprintf("bind-port acme-web %s %d", tc.runtime, tc.deployment.Port))
			start := indexOf(st.log, "systemctl start "+UnitName(tc.runtime, "acme", "web"))
			if bind < 0 || start < 0 || bind > start {
				t.Errorf("the port must be allowed before the unit starts; history: %q", st.log)
			}
			// The environment tells the app the port it is allowed.
			if !strings.Contains(st.env["acme-web"], fmt.Sprintf("PORT=\"%d\"", tc.deployment.Port)) {
				t.Errorf("the environment names another port than the one allowed:\n%s", st.env["acme-web"])
			}
		})
	}
}

// A reconfigure rewrites the environment, PORT included, so it rewrites the
// allow too, before the restart that reads both.
func TestReconfigure_allowsThePortTheNewEnvironmentNames(t *testing.T) {
	st := newRecordingStager()
	m := systemdManager(st)
	deployment := &deployments.Deployment{Namespace: "acme", Name: "api", Type: deployments.DeploymentTypeGoBackend, Port: 10300}
	if err := m.Reconfigure(context.Background(), deployment, "/unused"); err != nil {
		t.Fatalf("Reconfigure: %v", err)
	}
	if st.ports["acme-api"] != "go:10300" {
		t.Errorf("allowed %q, want go:10300", st.ports["acme-api"])
	}
	bind := indexOf(st.log, "bind-port acme-api go 10300")
	restart := indexOf(st.log, "systemctl restart "+UnitName(RuntimeGo, "acme", "api"))
	if bind < 0 || restart < 0 || bind > restart {
		t.Errorf("the port must be allowed before the restart; history: %q", st.log)
	}
}

// A port outside the allocator's range is a platform port — the tenant block,
// the index gateway's 10104, the chain RPC's 31001 — or none at all. It is
// refused before anything is started.
func TestStart_refusesAPortOutsideTheDeploymentRange(t *testing.T) {
	for _, port := range []int{0, -1, 10000, 10104, 10199, 20000, 31001, 65535} {
		st := newRecordingStager()
		deployment := &deployments.Deployment{Namespace: "acme", Name: "web", Type: deployments.DeploymentTypeGoBackend, Port: port}
		err := systemdManager(st).Start(context.Background(), deployment, "/unused")
		if err == nil || !strings.Contains(err.Error(), "outside the deployment range") {
			t.Errorf("port %d: err = %v, want a refusal naming the range", port, err)
		}
		for _, entry := range st.log {
			if strings.HasPrefix(entry, "systemctl start") || strings.HasPrefix(entry, "bind-port") {
				t.Errorf("port %d: %q ran although the port was refused", port, entry)
			}
		}
	}
}

func TestAllowPort_refusesWithNoStager(t *testing.T) {
	m := &Manager{logger: zap.NewNop()}
	deployment := &deployments.Deployment{Namespace: "acme", Name: "web", Type: deployments.DeploymentTypeGoBackend, Port: 10200}
	if err := m.allowPort(deployment, "orama-deploy-acme-web"); err == nil {
		t.Fatal("a port was reported allowed with nowhere to write it")
	}
}

// A static deployment has no unit, so it has no port to allow.
func TestAllowPort_refusesADeploymentWithNoRuntime(t *testing.T) {
	m := &Manager{logger: zap.NewNop(), stager: newRecordingStager()}
	deployment := &deployments.Deployment{Namespace: "acme", Name: "site", Type: deployments.DeploymentTypeStatic, Port: 10200}
	if err := m.allowPort(deployment, "orama-deploy-acme-site"); err == nil {
		t.Fatal("a static deployment was allowed a port")
	}
}

// The helper refuses a port outside its own range, which has to be the
// allocator's: a narrower one would refuse a deployment the allocator placed.
func TestDeployPortRange_isTheAllocatorsRange(t *testing.T) {
	if privhelper.DeployPortMin != deployments.UserMinPort || privhelper.DeployPortMax != deployments.MaxPort {
		t.Fatalf("the helper allows %d-%d, the allocator hands out %d-%d",
			privhelper.DeployPortMin, privhelper.DeployPortMax, deployments.UserMinPort, deployments.MaxPort)
	}
}

// Every runtime the gateway starts must be one the helper writes a drop-in
// for, at both ends of the range, or that runtime's deployments cannot start.
func TestAllowPort_everyRuntimeIsOneTheHelperAccepts(t *testing.T) {
	for _, runtime := range []Runtime{RuntimeNode, RuntimeNPM, RuntimeGo} {
		for _, port := range []int{deployments.UserMinPort, deployments.MaxPort} {
			argv := []string{privhelper.ToolDeploy, "bind-port", InstanceName("acme", "web.v2"), string(runtime), fmt.Sprint(port)}
			if _, err := privhelper.Validate(argv); err != nil {
				t.Errorf("the helper refuses %q: %v", argv, err)
			}
		}
	}
}

// The allow goes with the rest of the deployment's staged files.
func TestStop_removesTheAllowedPort(t *testing.T) {
	st := newRecordingStager()
	m := systemdManager(st)
	deployment := &deployments.Deployment{Namespace: "acme", Name: "web", Type: deployments.DeploymentTypeGoBackend, Port: 10200}
	if err := m.Start(context.Background(), deployment, "/unused"); err != nil {
		t.Fatal(err)
	}
	if err := m.Stop(context.Background(), deployment); err != nil {
		t.Fatal(err)
	}
	if _, ok := st.ports["acme-web"]; ok || indexOf(st.cleared, "acme-web") < 0 {
		t.Errorf("the port is still allowed after the deployment was stopped: %q", st.ports)
	}
}

// refusingStager is a recordingStager whose helper refuses the port.
type refusingStager struct{ *recordingStager }

func (r refusingStager) AllowPort(string, Runtime, int) error {
	return fmt.Errorf("orama-privhelper: refused")
}

// A port the helper would not allow stops the deploy before systemd runs
// anything: the unit would start and fail to listen.
func TestStartAndReconfigure_stopWhenThePortIsNotAllowed(t *testing.T) {
	deployment := &deployments.Deployment{Namespace: "acme", Name: "web", Type: deployments.DeploymentTypeGoBackend, Port: 10200}
	for name, run := range map[string]func(*Manager) error{
		"start":       func(m *Manager) error { return m.Start(context.Background(), deployment, "/unused") },
		"reconfigure": func(m *Manager) error { return m.Reconfigure(context.Background(), deployment, "/unused") },
	} {
		st := newRecordingStager()
		m := systemdManager(st)
		m.stager = refusingStager{st}
		err := run(m)
		if err == nil || !strings.Contains(err.Error(), "refused") {
			t.Errorf("%s: err = %v, want the helper's refusal", name, err)
		}
		for _, entry := range st.log {
			if strings.HasPrefix(entry, "systemctl start") || strings.HasPrefix(entry, "systemctl restart") {
				t.Errorf("%s: %q ran although the port was refused", name, entry)
			}
		}
	}
}

// A restart starts the unit on whatever its credential file holds. The redeploy
// paths restart without a Start, so the file has to be minted again first: it
// held the token of the last Start, which expires after an hour and carried the
// grant of that day.
func TestRestart_mintsTheCredentialBeforeTheRestart(t *testing.T) {
	st := newRecordingStager()
	m := systemdManager(st)
	minted := 0
	m.SetWorkloadTokenMinter(func(context.Context, string, string) (string, error) {
		minted++
		return fmt.Sprintf("token-%d", minted), nil
	})
	deployment := &deployments.Deployment{Namespace: "acme", Name: "api", Type: deployments.DeploymentTypeGoBackend, Port: 10300}

	for want := 1; want <= 2; want++ {
		if err := m.Restart(context.Background(), deployment); err != nil {
			t.Fatalf("Restart: %v", err)
		}
		if got := st.token["acme-api"]; got != fmt.Sprintf("token-%d", want) {
			t.Errorf("restart %d left %q staged", want, got)
		}
	}
}

// A restart that cannot mint does not restart: the app would start on a
// credential nobody can vouch for.
func TestRestart_refusesWhenTheCredentialCannotBeMinted(t *testing.T) {
	st := newRecordingStager()
	m := systemdManager(st)
	m.SetWorkloadTokenMinter(func(context.Context, string, string) (string, error) {
		return "", fmt.Errorf("the registry did not answer")
	})
	deployment := &deployments.Deployment{Namespace: "acme", Name: "api", Type: deployments.DeploymentTypeGoBackend, Port: 10300}

	err := m.Restart(context.Background(), deployment)
	if err == nil || !strings.Contains(err.Error(), "registry did not answer") {
		t.Fatalf("Restart = %v, want the mint failure", err)
	}
	for _, entry := range st.log {
		if strings.HasPrefix(entry, "systemctl restart") {
			t.Errorf("%q ran although the credential could not be minted", entry)
		}
	}
}

// jwtExpiring is a token whose payload says it expires in d.
func jwtExpiring(d time.Duration) string {
	payload, _ := json.Marshal(map[string]int64{"exp": time.Now().Add(d).Unix()})
	return "h." + base64.RawURLEncoding.EncodeToString(payload) + ".s"
}

// The registry being down must not keep a crashed app down while the token
// staged for it is still good: the restart runs on it.
func TestRestart_runsOnTheStagedTokenWhileItHasLifeLeft(t *testing.T) {
	st := newRecordingStager()
	m := systemdManager(st)
	deployment := &deployments.Deployment{Namespace: "acme", Name: "api", Type: deployments.DeploymentTypeGoBackend, Port: 10300}
	m.SetWorkloadTokenMinter(func(context.Context, string, string) (string, error) { return jwtExpiring(time.Hour), nil })
	if err := m.Start(context.Background(), deployment, "/unused"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	m.SetWorkloadTokenMinter(func(context.Context, string, string) (string, error) {
		return "", errors.New("the registry did not answer")
	})

	if err := m.Restart(context.Background(), deployment); err != nil {
		t.Fatalf("Restart refused although the staged token is good for an hour: %v", err)
	}
	if indexOf(st.log, "systemctl restart "+UnitName(RuntimeGo, "acme", "api")) < 0 {
		t.Errorf("the unit was not restarted; history: %q", st.log)
	}
}

// With less life than the margin left, or none known, there is nothing to run on.
func TestRestart_refusesWhenTheStagedTokenIsAboutToExpireOrUnknown(t *testing.T) {
	for name, staged := range map[string]string{
		"about to expire": jwtExpiring(time.Minute),
		"expired":         jwtExpiring(-time.Minute),
		"unreadable":      "token",
	} {
		t.Run(name, func(t *testing.T) {
			st := newRecordingStager()
			m := systemdManager(st)
			deployment := &deployments.Deployment{Namespace: "acme", Name: "api", Type: deployments.DeploymentTypeGoBackend, Port: 10300}
			m.SetWorkloadTokenMinter(func(context.Context, string, string) (string, error) { return staged, nil })
			if err := m.Start(context.Background(), deployment, "/unused"); err != nil {
				t.Fatalf("Start: %v", err)
			}
			m.SetWorkloadTokenMinter(func(context.Context, string, string) (string, error) {
				return "", errors.New("the registry did not answer")
			})
			if err := m.Restart(context.Background(), deployment); err == nil {
				t.Error("Restart ran on a token that is not usable")
			}
		})
	}
	t.Run("nothing staged by this gateway", func(t *testing.T) {
		m := systemdManager(newRecordingStager())
		m.SetWorkloadTokenMinter(func(context.Context, string, string) (string, error) { return "", errors.New("down") })
		deployment := &deployments.Deployment{Namespace: "acme", Name: "api", Type: deployments.DeploymentTypeGoBackend, Port: 10300}
		if err := m.Restart(context.Background(), deployment); err == nil {
			t.Error("Restart ran with no known credential")
		}
	})
}

// A restart that raced a delete does not bring the deployment back, whatever
// life its old token has left.
func TestRestart_neverRestartsADeletedDeployment(t *testing.T) {
	st := newRecordingStager()
	m := systemdManager(st)
	activeState(m, "active")
	deployment := &deployments.Deployment{Namespace: "acme", Name: "api", Type: deployments.DeploymentTypeGoBackend, Port: 10300}
	m.SetWorkloadTokenMinter(func(context.Context, string, string) (string, error) { return jwtExpiring(time.Hour), nil })
	if err := m.Start(context.Background(), deployment, "/unused"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	m.SetWorkloadTokenMinter(func(context.Context, string, string) (string, error) { return "", ErrDeploymentGone })

	if err := m.Restart(context.Background(), deployment); !errors.Is(err, ErrDeploymentGone) {
		t.Fatalf("Restart = %v, want ErrDeploymentGone", err)
	}
	if err := m.RefreshToken(context.Background(), deployment); !errors.Is(err, ErrDeploymentGone) {
		t.Errorf("RefreshToken = %v, want ErrDeploymentGone", err)
	}
	for _, e := range st.log {
		if strings.HasPrefix(e, "systemctl restart") {
			t.Errorf("%q ran for a deleted deployment", e)
		}
	}
}

// activeState makes the manager's read-only systemctl answer ActiveState.
func activeState(m *Manager, state string) {
	m.query = func(_ context.Context, args ...string) ([]byte, error) {
		return []byte("ActiveState=" + state + "\n"), nil
	}
}

// RefreshToken stages a new credential and leaves the unit alone.
func TestRefreshToken_stagesWithoutRestarting(t *testing.T) {
	st := newRecordingStager()
	m := systemdManager(st)
	activeState(m, "active")
	m.SetWorkloadTokenMinter(func(context.Context, string, string) (string, error) { return "fresh", nil })
	deployment := &deployments.Deployment{Namespace: "acme", Name: "api", Type: deployments.DeploymentTypeGoBackend, Port: 10300}

	if err := m.RefreshToken(context.Background(), deployment); err != nil {
		t.Fatalf("RefreshToken: %v", err)
	}
	if st.token["acme-api"] != "fresh" {
		t.Errorf("staged %q", st.token["acme-api"])
	}
	for _, e := range st.log {
		if strings.HasPrefix(e, "systemctl") {
			t.Errorf("%q ran; a refresh must not touch the unit", e)
		}
	}
}

// A refresh uses the cheaper mint: no principal write, no second read of the
// row. Start and Restart keep the full one.
func TestRefreshToken_usesTheRefresherNotTheMinter(t *testing.T) {
	st := newRecordingStager()
	m := systemdManager(st)
	activeState(m, "active")
	m.SetWorkloadTokenMinter(func(context.Context, string, string) (string, error) { return "full", nil })
	m.SetWorkloadTokenRefresher(func(context.Context, string, string) (string, error) { return "cheap", nil })
	deployment := &deployments.Deployment{Namespace: "acme", Name: "api", Type: deployments.DeploymentTypeGoBackend, Port: 10300}

	if err := m.RefreshToken(context.Background(), deployment); err != nil || st.token["acme-api"] != "cheap" {
		t.Fatalf("refresh staged %q, err %v", st.token["acme-api"], err)
	}
	if err := m.Restart(context.Background(), deployment); err != nil || st.token["acme-api"] != "full" {
		t.Fatalf("restart staged %q, err %v", st.token["acme-api"], err)
	}
}

// A refresh that raced a delete's stop must not bring the credential back: the
// stop clears the staged files, and the deployment's row is removed only after.
func TestRefreshToken_afterAStopStagesNothing(t *testing.T) {
	st := newRecordingStager()
	m := systemdManager(st)
	activeState(m, "active")
	minted := 0
	m.SetWorkloadTokenMinter(func(context.Context, string, string) (string, error) {
		minted++
		return jwtExpiring(time.Hour), nil
	})
	deployment := &deployments.Deployment{Namespace: "acme", Name: "api", Type: deployments.DeploymentTypeGoBackend, Port: 10300}
	if err := m.Start(context.Background(), deployment, "/unused"); err != nil {
		t.Fatalf("Start: %v", err)
	}
	if err := m.Stop(context.Background(), deployment); err != nil {
		t.Fatalf("Stop: %v", err)
	}
	minted = 0

	if err := m.RefreshToken(context.Background(), deployment); !errors.Is(err, ErrStopped) {
		t.Errorf("RefreshToken = %v, want ErrStopped", err)
	}
	if err := m.Restart(context.Background(), deployment); !errors.Is(err, ErrStopped) {
		t.Errorf("Restart = %v, want ErrStopped", err)
	}
	if _, ok := st.token["acme-api"]; ok || minted != 0 {
		t.Errorf("a stopped deployment was given a credential: %v, minted %d", st.token, minted)
	}
	if m.stagedTokenUsable("acme-api") {
		t.Error("the stopped instance still has a recorded staged token")
	}
	m.stagedMu.Lock()
	_, recorded := m.stagedUntil["acme-api"]
	m.stagedMu.Unlock()
	if recorded {
		t.Error("stagedUntil still holds the stopped instance")
	}

	// The same name deployed again is a new deployment.
	if err := m.Start(context.Background(), deployment, "/unused"); err != nil {
		t.Fatalf("Start after Stop: %v", err)
	}
	if _, ok := st.token["acme-api"]; !ok {
		t.Error("a redeploy of the same name got no credential")
	}
}

// A stop the helper refused leaves the unit running, so it keeps its identity.
func TestStop_aRefusedStopDoesNotMarkTheDeploymentStopped(t *testing.T) {
	st := newRecordingStager()
	m := systemdManager(st)
	activeState(m, "active")
	m.systemctl = func(args ...string) error {
		if len(args) > 0 && args[0] == "stop" {
			return errors.New("refused")
		}
		return nil
	}
	m.SetWorkloadTokenMinter(func(context.Context, string, string) (string, error) { return "tok", nil })
	deployment := &deployments.Deployment{Namespace: "acme", Name: "api", Type: deployments.DeploymentTypeGoBackend, Port: 10300}

	if err := m.Stop(context.Background(), deployment); err == nil {
		t.Fatal("Stop reported success although the stop was refused")
	}
	if err := m.RefreshToken(context.Background(), deployment); err != nil {
		t.Errorf("a unit still running was refused its refresh: %v", err)
	}
}

// Only a running unit is refreshed, and one the gateway serves has no unit.
func TestRefreshToken_skipsUnitsThatAreNotRunning(t *testing.T) {
	for _, state := range []string{"inactive", "failed", "deactivating"} {
		st := newRecordingStager()
		m := systemdManager(st)
		activeState(m, state)
		deployment := &deployments.Deployment{Namespace: "acme", Name: "api", Type: deployments.DeploymentTypeGoBackend, Port: 10300}
		if err := m.RefreshToken(context.Background(), deployment); err != nil || len(st.token) != 0 {
			t.Errorf("%s: err %v, staged %v", state, err, st.token)
		}
	}

	st := newRecordingStager()
	m := systemdManager(st)
	if err := m.RefreshToken(context.Background(), &deployments.Deployment{Namespace: "acme", Name: "site", Type: deployments.DeploymentTypeStatic}); err != nil || len(st.token) != 0 {
		t.Errorf("a served deployment: err %v, staged %v", err, st.token)
	}

	m = systemdManager(newRecordingStager())
	m.query = func(context.Context, ...string) ([]byte, error) { return nil, errors.New("systemctl missing") }
	if err := m.RefreshToken(context.Background(), &deployments.Deployment{Namespace: "acme", Name: "api", Type: deployments.DeploymentTypeGoBackend, Port: 10300}); err == nil {
		t.Error("an unreadable unit state was taken for a running unit")
	}
}
