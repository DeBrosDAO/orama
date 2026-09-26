package install

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/systemd"
	"go.uber.org/zap"
)

// oramaNodeHardening is the sandbox orama-node runs in. It has no explicit
// NoNewPrivileges= line. That omission once let the supervisor sudo; it no
// longer matters: root actions go through the socket-activated privileged
// helper (pkg/privhelper), and the sandboxing directives below imply
// no_new_privs in systemd regardless.
const oramaNodeHardening = `User=orama
Group=orama
ProtectSystem=strict
ProtectHome=yes
PrivateDevices=yes
ProtectKernelTunables=yes
ProtectProc=invisible
ProtectKernelModules=yes
RestrictNamespaces=yes`

// SystemdServiceGenerator generates systemd unit files
type SystemdServiceGenerator struct {
	oramaHome string
	oramaDir  string
}

// NewSystemdServiceGenerator creates a new service generator
func NewSystemdServiceGenerator(oramaHome, oramaDir string) *SystemdServiceGenerator {
	return &SystemdServiceGenerator{
		oramaHome: oramaHome,
		oramaDir:  oramaDir,
	}
}

// GenerateNodeService generates the Orama Node systemd unit, the only host
// unit install writes: everything else runs from the orama-namespace-*@
// templates the supervisor starts.
//
// It logs to the journal, which `orama node logs` reads. It used to append to
// .orama/logs/node.log, and systemd opens a StandardOutput=append:/file:
// target as PID 1 — before it drops to User= — with a plain open(2) that
// follows symlinks and creates what is missing (src/core/exec-invoke.c,
// setup_output → acquire_path). The orama user owns logs/, so it could point
// node.log at any root-owned file and have root append the node's output to it
// on the next start.
func (ssg *SystemdServiceGenerator) GenerateNodeService() string {
	configFile := "node.yaml"

	return fmt.Sprintf(`[Unit]
Description=Orama Network Node
After=network-online.target
Wants=network-online.target
# The node no longer exits because the cluster is unreachable — it degrades and
# keeps converging — so a restart now means a genuine crash. Disabling the start
# limit explicitly keeps systemd from giving up on a node that crash-looped for
# an unrelated reason, which would otherwise leave it in failed state until an
# operator noticed. StartLimitIntervalSec belongs in [Unit], not [Service].
StartLimitIntervalSec=0

[Service]
Type=simple
%[4]s
AmbientCapabilities=CAP_NET_ADMIN
# /etc/wireguard stays read-only to the node: wg-quick runs wg0.conf's PostUp
# lines as root, so a conf the orama user could write would be root code
# execution. The peer sync persists mesh membership through the privileged
# helper (orama-privhelper wireguard persist-peers), which validates each peer
# and rewrites only the [Peer] sections.
ReadWritePaths=%[2]s
WorkingDirectory=%[1]s
Environment=HOME=%[1]s
ExecStart=%[1]s/bin/orama-node --config %[2]s/configs/%[3]s
Restart=always
RestartSec=5
StandardOutput=journal
StandardError=journal
SyslogIdentifier=orama-node

PrivateTmp=yes
LimitNOFILE=65536
# Shutdown is: announce maintenance, wait up to 10s for the boot supervisor to
# leave its current attempt, then tear services down and hand raft leadership
# over. 60s leaves that sequence room; at 30s systemd used to SIGKILL right as
# the leadership transfer began.
TimeoutStopSec=60
KillMode=mixed
MemoryMax=8G
MemorySwapMax=0
OOMScoreAdjust=-500

[Install]
WantedBy=multi-user.target
`, ssg.oramaHome, ssg.oramaDir, configFile, oramaNodeHardening)
}

// SystemdController manages systemd service operations
type SystemdController struct {
	systemdDir string
}

// NewSystemdController creates a new controller
func NewSystemdController() *SystemdController {
	return &SystemdController{
		systemdDir: "/etc/systemd/system",
	}
}

// WriteServiceUnit writes a systemd unit file
func (sc *SystemdController) WriteServiceUnit(name string, content string) error {
	unitPath := filepath.Join(sc.systemdDir, name)
	if err := os.WriteFile(unitPath, []byte(content), 0644); err != nil {
		return fmt.Errorf("failed to write unit file %s: %w", name, err)
	}
	return nil
}

// DaemonReload reloads the systemd daemon
func (sc *SystemdController) DaemonReload() error {
	cmd := exec.Command("systemctl", "daemon-reload")
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to reload systemd daemon: %w", err)
	}
	return nil
}

// EnableService enables a service to start on boot
func (sc *SystemdController) EnableService(name string) error {
	cmd := exec.Command("systemctl", "enable", name)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to enable service %s: %w", name, err)
	}
	return nil
}

// RestartService restarts a service
func (sc *SystemdController) RestartService(name string) error {
	cmd := exec.Command("systemctl", "restart", name)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to restart service %s: %w", name, err)
	}
	return nil
}

// StopService stops a service
func (sc *SystemdController) StopService(name string) error {
	cmd := exec.Command("systemctl", "stop", name)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to stop service %s: %w", name, err)
	}
	return nil
}

// DisableService disables a service from starting on boot
func (sc *SystemdController) DisableService(name string) error {
	cmd := exec.Command("systemctl", "disable", name)
	if err := cmd.Run(); err != nil {
		return fmt.Errorf("failed to disable service %s: %w", name, err)
	}
	return nil
}

// StatusService gets the status of a service
func (sc *SystemdController) StatusService(name string) (bool, error) {
	cmd := exec.Command("systemctl", "is-active", "--quiet", name)
	err := cmd.Run()
	if err == nil {
		return true, nil
	}

	// Check for "inactive" vs actual error
	if strings.Contains(err.Error(), "exit status 3") {
		return false, nil // Service is inactive
	}

	return false, fmt.Errorf("failed to check service status %s: %w", name, err)
}

// InstallNamespaceTemplates installs the systemd template units the namespace
// services are instantiated from.
//
// This must run BEFORE anything starts orama-node: the supervisor's first act
// is to start orama-namespace-wireguard@index, and with no template installed
// systemd answers "Unit ... not found", the supervisor exits, and systemd
// restarts it. Install used to depend on that retry loop to converge — it
// worked, so nobody noticed the ordering was backwards.
//
// Every template is required, and the error names the one that failed. The two
// byte-identical copies this replaces logged a warning and `continue`d past
// each read or write error, so a node missing half its templates finished
// installing and reported success; and when every template failed, the
// installed count stayed zero, the daemon-reload was skipped, and the function
// returned nil — total failure, reported as success.
//
// It holds the archive lock, so the templates come from one verified build even
// while `orama node stage-archive` runs.
func (ps *ProductionSetup) InstallNamespaceTemplates() (err error) {
	unlock, err := lockArchive(OramaBase)
	if err != nil {
		return err
	}
	defer func() { err = errors.Join(err, unlock()) }()

	// The templates come from the verified build archive only; a copy under
	// /opt/orama/src used to be taken instead, unverified, when it was missing.
	sourceDir := OramaSystemdDir
	if _, err := os.Stat(sourceDir); err != nil {
		return fmt.Errorf("no systemd template directory at %s (the build archive installs it): %w", sourceDir, err)
	}

	// Before the templates: installing them reloads systemd, which is what
	// picks the drop-ins up.
	if err := installIndexGatewayDropIn(); err != nil {
		return err
	}
	publicIP, err := ps.PublicIP()
	if err != nil {
		return fmt.Errorf("the build sandbox denies this node's public address: %w", err)
	}
	if err := installBuildSandbox(publicIP); err != nil {
		return err
	}

	// The manager logs each template; the operator-facing summary is ours.
	if err := systemd.NewManager("", zap.NewNop()).InstallTemplateUnits(sourceDir); err != nil {
		return fmt.Errorf("install namespace systemd templates from %s: %w", sourceDir, err)
	}

	ps.logf("  ✓ Installed %d namespace template units from %s (daemon reloaded)",
		len(systemd.UnitFilesToInstall()), sourceDir)
	return nil
}
