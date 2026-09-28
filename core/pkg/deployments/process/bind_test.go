package process

import (
	"context"
	"fmt"
	"strings"
	"testing"

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
