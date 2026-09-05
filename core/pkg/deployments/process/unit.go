package process

import (
	"fmt"
	"sort"
	"strings"

	"github.com/DeBrosOfficial/network/pkg/deployments"
)

// A deployment is a tenant's own code, uploaded through the API and run on a
// node that also runs the cluster's control plane. Its unit had no User=, so
// it ran as root, and none of the hardening CHG-240 put on every other unit on
// the box. This file is what that unit looks like now, and it is a pure
// function of the deployment so it can be read back in a test rather than only
// on a node.

const (
	// deploymentTasksMax caps how many processes and threads one deployment
	// may create. Without it, a fork loop in tenant code takes down every
	// service on the node, not just that deployment.
	deploymentTasksMax = 512

	// stateDirectoryRoot is where systemd creates each deployment's writable
	// directory, under /var/lib. The app's own directory is read-only: the
	// files there are its build output, and a process that can rewrite its own
	// code cannot be rolled back to a known version.
	stateDirectoryRoot = "/var/lib"
	// cacheDirectoryRoot mirrors stateDirectoryRoot, under /var/cache.
	cacheDirectoryRoot = "/var/cache"
)

// deploymentHardening is the directive block every deployment unit carries.
//
// DynamicUser is what gives each deployment its own identity: systemd allocates
// a UID for the unit and reclaims it when the unit stops, so there is no
// per-deployment account to create, leak, or forget to delete, and no two
// deployments ever share a user. The rest is the set CHG-240 applied to the
// platform's own services, which a deployment has less claim to than they do.
//
// IPAddressDeny is the one addition. Every node reaches the cluster's control
// plane — rqlite, Olric, every other namespace's services — over the WireGuard
// overlay on 10.0.0.0/8, and a deployment sits on the same host as that
// overlay. Tenant code has no business on it, so the private ranges and the
// link-local metadata range are denied while the public internet and loopback
// stay reachable: loopback is how the node's own reverse proxy reaches the app.
const deploymentHardening = `DynamicUser=yes
ProtectSystem=strict
ProtectHome=yes
NoNewPrivileges=yes
PrivateDevices=yes
PrivateTmp=yes
ProtectKernelTunables=yes
ProtectKernelModules=yes
ProtectControlGroups=yes
RestrictNamespaces=yes
RestrictSUIDSGID=yes
RestrictRealtime=yes
LockPersonality=yes
RemoveIPC=yes
ProtectProc=invisible
IPAddressAllow=localhost
IPAddressDeny=10.0.0.0/8 172.16.0.0/12 192.168.0.0/16 169.254.0.0/16 fc00::/7 fe80::/10`

// UnitSpec is everything a deployment unit is rendered from.
type UnitSpec struct {
	ServiceName     string
	Namespace       string
	Name            string
	WorkDir         string
	StartCmd        string
	EnvFilePath     string
	RestartPolicy   string
	MemoryLimitMB   int
	CPULimitPercent int
}

// RenderUnit returns the systemd unit for one deployment.
func RenderUnit(spec UnitSpec) (string, error) {
	if err := spec.validate(); err != nil {
		return "", err
	}

	memoryMB := spec.MemoryLimitMB
	if memoryMB <= 0 {
		memoryMB = deployments.DefaultMemoryLimitMB
	}
	cpuPercent := spec.CPULimitPercent
	if cpuPercent <= 0 {
		cpuPercent = deployments.DefaultCPULimitPercent
	}

	var b strings.Builder
	fmt.Fprintf(&b, `[Unit]
Description=Orama Deployment - %s/%s
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
WorkingDirectory=%s

%s
StateDirectory=%s
CacheDirectory=%s

EnvironmentFile=%s

ExecStart=%s

Restart=%s
RestartSec=5s

MemoryMax=%dM
MemorySwapMax=0
CPUQuota=%d%%
TasksMax=%d

StandardOutput=journal
StandardError=journal
SyslogIdentifier=%s

[Install]
WantedBy=multi-user.target
`,
		spec.Namespace, spec.Name,
		spec.WorkDir,
		deploymentHardening,
		spec.ServiceName,
		spec.ServiceName,
		spec.EnvFilePath,
		spec.StartCmd,
		spec.RestartPolicy,
		memoryMB,
		cpuPercent,
		deploymentTasksMax,
		spec.ServiceName,
	)
	return b.String(), nil
}

// validate refuses a spec that would render a unit meaning something other than
// what it says.
//
// Every field below is interpolated into a line of a unit file, where a newline
// starts a new directive. The service name, namespace and deployment name are
// constrained upstream, but "constrained upstream" is exactly the assumption
// that put unescaped tenant input into this file in the first place.
func (s UnitSpec) validate() error {
	for _, f := range []struct {
		name  string
		value string
	}{
		{"service name", s.ServiceName},
		{"namespace", s.Namespace},
		{"deployment name", s.Name},
		{"working directory", s.WorkDir},
		{"start command", s.StartCmd},
		{"environment file path", s.EnvFilePath},
		{"restart policy", s.RestartPolicy},
	} {
		if strings.TrimSpace(f.value) == "" {
			return fmt.Errorf("cannot write a deployment unit with no %s", f.name)
		}
		if strings.ContainsAny(f.value, "\n\r") {
			return fmt.Errorf("the %s contains a newline, which would write an unintended systemd directive: %q", f.name, f.value)
		}
	}
	return nil
}

// StateDirectoryPath is the writable directory systemd creates for a
// deployment, exported to it as ORAMA_STATE_DIR.
func StateDirectoryPath(serviceName string) string {
	return stateDirectoryRoot + "/" + serviceName
}

// CacheDirectoryPath is the deployment's cache directory, exported to it as
// ORAMA_CACHE_DIR.
func CacheDirectoryPath(serviceName string) string {
	return cacheDirectoryRoot + "/" + serviceName
}

// PlatformEnvKeys are the variables the platform sets in every deployment. A
// tenant cannot set them: they are how the app knows which namespace it belongs
// to and where it may write, and a deployment that could overwrite them could
// point itself at another namespace's gateway.
var PlatformEnvKeys = []string{
	"PORT",
	"ORAMA_NAMESPACE",
	"ORAMA_GATEWAY_URL",
	"ORAMA_STATE_DIR",
	"ORAMA_CACHE_DIR",
}

// platformEnv returns the variables the platform sets for one deployment.
func platformEnv(namespace, serviceName, gatewayURL string, port int) map[string]string {
	env := map[string]string{
		"PORT":            fmt.Sprintf("%d", port),
		"ORAMA_NAMESPACE": namespace,
		"ORAMA_STATE_DIR": StateDirectoryPath(serviceName),
		"ORAMA_CACHE_DIR": CacheDirectoryPath(serviceName),
	}
	if gatewayURL != "" {
		env["ORAMA_GATEWAY_URL"] = gatewayURL
	}
	return env
}

// mergeEnv returns the deployment's environment with the platform's variables
// applied last, so a tenant value can never displace one of them.
func mergeEnv(tenant map[string]string, platform map[string]string) map[string]string {
	merged := make(map[string]string, len(tenant)+len(platform))
	for key, value := range tenant {
		merged[key] = value
	}
	for key, value := range platform {
		merged[key] = value
	}
	return merged
}

// sortedEnv returns "KEY=value" strings in a stable order, for the direct
// (non-systemd) runner.
func sortedEnv(env map[string]string) []string {
	keys := make([]string, 0, len(env))
	for key := range env {
		keys = append(keys, key)
	}
	sort.Strings(keys)

	out := make([]string, 0, len(keys))
	for _, key := range keys {
		out = append(out, key+"="+env[key])
	}
	return out
}
