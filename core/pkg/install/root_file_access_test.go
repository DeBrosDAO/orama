package install

import (
	"go/ast"
	"go/parser"
	"go/token"
	"go/types"
	"path/filepath"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"testing"
)

// Install and upgrade run as root, and most of what they touch is in
// /opt/orama/.orama, which belongs to the orama user. The os functions follow
// symlinks, so a compromised orama process could point a file there at
// /etc/sudoers.d or /etc/shadow and have root write it on the next upgrade.
// Everything in that tree goes through pkg/rootfs (OramaRoot), which refuses
// symlinks. This scan fails on any new direct os call, or file-touching
// command, in the code root runs for install and upgrade; a call that really
// touches only a root-owned tree is added to rootFileAccessAllowed with the
// reason.

// rootScannedDirs are the packages root runs during install and upgrade,
// relative to the module root.
var rootScannedDirs = []string{
	"pkg/install",
	"pkg/install/installers",
	"cmd/orama/internal/production/install",
	"cmd/orama/internal/production/upgrade",
	"cmd/orama/internal/production/lifecycle",
}

// guardedOSFuncs are the os functions that resolve a path and follow symlinks.
var guardedOSFuncs = map[string]bool{
	"WriteFile": true, "Create": true, "CreateTemp": true, "OpenFile": true,
	"Mkdir": true, "MkdirAll": true, "MkdirTemp": true,
	"Chmod": true, "Chown": true, "Lchown": true, "Chtimes": true,
	"Remove": true, "RemoveAll": true, "Rename": true, "Symlink": true, "Link": true,
	"Truncate": true, "ReadFile": true, "Open": true,
}

// guardedCommands are programs that write the paths they are given.
var guardedCommands = map[string]bool{
	"chown": true, "chmod": true, "mkdir": true, "cp": true, "mv": true,
	"tee": true, "ln": true, "rm": true, "install": true, "touch": true, "truncate": true,
}

// Why each allowed call is safe: the tree it touches is root's.
const (
	allowSys        = "reads a root-owned system file"
	allowEtc        = "root-owned /etc tree"
	allowUnit       = "unit file in root-owned /etc/systemd/system"
	allowUsrbin     = "binary in root-owned /usr/bin, /usr/local/bin or /usr/local/go"
	allowCopyBinary = "copies a release binary from /opt/orama/bin (root:orama 0750) to root-owned /usr/bin or /usr/local/bin"
	allowBin        = "/opt/orama/bin is root:orama 0750: the orama user cannot add or replace entries"
	allowOptroot    = "file directly in root-owned /opt/orama, not in the orama user's .orama"
	allowChownR     = "GNU chown -R without -H/-L never dereferences a symlink (it lchowns)"
	allowCaddyVar   = "/var/lib is root-owned; chown -R without -H/-L never dereferences a symlink"
	allowNtfy       = "root-owned /etc/ntfy; /var/lib/ntfy is only created, then chown -R (no dereference)"
	allowAnyone     = "legacy Anyone paths in root-owned /etc and /var trees; its .orama files go through rootfs"
	allowTor        = "root-owned /etc apt and tor paths (ti.path prefixes a test root)"
	allowTorkey     = "private os.MkdirTemp GNUPGHOME"
	allowPrivhelper = "root-owned /usr/local/bin, /etc/systemd/system and /etc/sudoers.d"
	allowWg         = "root-owned /etc/wireguard"
)

// rootFileAccessAllowed are the direct calls that touch only root-owned trees
// (or, for the chown -R lines, never dereference a symlink), keyed by
// "file func call(first argument)", with the reason.
var rootFileAccessAllowed = map[string]string{
	"pkg/install/checks.go (*OSDetector) Detect os.ReadFile(\"/etc/os-release\")":                                                           allowSys,
	"pkg/install/checks.go (*ResourceChecker) CheckRAM os.ReadFile(\"/proc/meminfo\")":                                                      allowSys,
	"pkg/install/firewall.go (*FirewallProvisioner) persistIPv6Disable exec.Command(\"tee\", \"/etc/sysctl.d/99-orama-disable-ipv6.conf\")": allowEtc,
	"pkg/install/firewall.go (*FirewallProvisioner) persistRAMHygiene exec.Command(\"mkdir\", \"-p\", \"/etc/systemd/coredump.conf.d\")":    allowEtc,
	"pkg/install/firewall.go (*FirewallProvisioner) persistRAMHygiene exec.Command(\"tee\", \"/etc/sysctl.d/99-orama-ram-hygiene.conf\")":   allowEtc,
	"pkg/install/firewall.go (*FirewallProvisioner) persistRAMHygiene exec.Command(\"tee\", \"/etc/systemd/coredump.conf.d/orama.conf\")":   allowEtc,
	"pkg/install/installers/anyone_legacy.go (*LegacyAnyoneCleaner) Remove os.RemoveAll(path)":                                              allowAnyone,
	"pkg/install/installers/caddy.go (*CaddyInstaller) Configure os.MkdirAll(configDir)":                                                    allowEtc,
	"pkg/install/installers/caddy.go (*CaddyInstaller) Configure os.WriteFile(filepath.Join(configDir, \"Caddyfile\"))":                     allowEtc,
	"pkg/install/installers/caddy.go writeCaddyACMEKey os.WriteFile(CaddyACMEKeyPath)":                                                      allowEtc,
	"pkg/install/installers/coredns.go (*CoreDNSInstaller) Configure os.MkdirAll(configDir)":                                                allowEtc,
	"pkg/install/installers/coredns.go (*CoreDNSInstaller) Configure os.WriteFile(corefilePath)":                                            allowEtc,
	"pkg/install/installers/coredns.go (*CoreDNSInstaller) DisableResolvedStubListener os.MkdirAll(\"/etc/systemd/resolved.conf.d\")":       allowEtc,
	"pkg/install/installers/coredns.go (*CoreDNSInstaller) DisableResolvedStubListener os.Remove(\"/etc/resolv.conf\")":                     allowEtc,
	"pkg/install/installers/coredns.go (*CoreDNSInstaller) DisableResolvedStubListener os.WriteFile(\"/etc/resolv.conf\")":                  allowEtc,
	"pkg/install/installers/coredns.go (*CoreDNSInstaller) DisableResolvedStubListener os.WriteFile(resolvedConf)":                          allowEtc,
	"pkg/install/installers/coredns.go restrictToOramaGroup os.Chmod(path)":                                                                 allowEtc,
	"pkg/install/installers/coredns.go restrictToOramaGroup os.Chown(path)":                                                                 allowEtc,
	"pkg/install/installers/host_units_legacy.go (*LegacyHostUnitCleaner) removeUnit os.Remove(path)":                                       allowUnit,
	"pkg/install/installers/ntfy.go (*NtfyInstaller) Configure exec.Command(\"chown\", \"root:\" + ntfyUser, ntfyConfigPath)":               allowNtfy,
	"pkg/install/installers/ntfy.go (*NtfyInstaller) Configure os.WriteFile(ntfyConfigPath)":                                                allowNtfy,
	"pkg/install/installers/ntfy.go (*NtfyInstaller) ensureDirs exec.Command(\"chown\", \"-R\", ntfyUser + \":\" + ntfyUser, ntfyDataDir)":  allowNtfy,
	"pkg/install/installers/ntfy.go (*NtfyInstaller) ensureDirs os.MkdirAll(ntfyConfigDir)":                                                 allowNtfy,
	"pkg/install/installers/ntfy.go (*NtfyInstaller) ensureDirs os.MkdirAll(ntfyDataDir)":                                                   allowNtfy,
	"pkg/install/installers/ntfy.go replaceBinary os.CreateTemp(filepath.Dir(path))":                                                        allowUsrbin,
	"pkg/install/installers/ntfy.go replaceBinary os.Remove(tmp.Name())":                                                                    allowUsrbin,
	"pkg/install/installers/ntfy.go replaceBinary os.Rename(tmp.Name())":                                                                    allowUsrbin,
	"pkg/install/installers/tor_installer.go (*TorInstaller) Configure os.MkdirAll(filepath.Dir(path))":                                     allowTor,
	"pkg/install/installers/tor_installer.go (*TorInstaller) Configure os.WriteFile(path)":                                                  allowTor,
	"pkg/install/installers/tor_installer.go (*TorInstaller) addRepository os.WriteFile(ti.path(TorAptSourcePath))":                         allowTor,
	"pkg/install/installers/tor_installer.go (*TorInstaller) repositoryCurrent os.ReadFile(ti.path(TorAptSourcePath))":                      allowTor,
	"pkg/install/installers/tor_installer.go (*TorInstaller) suite os.ReadFile(ti.path(\"/etc/os-release\"))":                               allowTor,
	"pkg/install/installers/tor_keyring.go (*TorInstaller) installVerifiedKeyring os.MkdirTemp(\"\")":                                       allowTorkey,
	"pkg/install/installers/tor_keyring.go (*TorInstaller) installVerifiedKeyring os.RemoveAll(home)":                                       allowTorkey,
	"pkg/install/installers/tor_keyring.go (*TorInstaller) installVerifiedKeyring os.WriteFile(keyFile)":                                    allowTorkey,
	"pkg/install/logrotate.go InstallLogrotateConfig os.WriteFile(logrotateConfigPath)":                                                     allowEtc,
	"pkg/install/orchestrator.go (*ProductionSetup) chownOramaTree exec.Command(\"chown\", \"-R\", \"orama:orama\", ps.oramaDir)":           allowChownR,
	"pkg/install/orchestrator.go ensureCaddyDataDir exec.Command(\"chown\", \"-R\", \"orama:orama\", caddyDataDir)":                         allowCaddyVar,
	"pkg/install/orchestrator.go ensureCaddyDataDir exec.Command(\"mkdir\", \"-p\", caddyDataDir)":                                          allowCaddyVar,
	"pkg/install/prebuilt.go LoadPreBuiltManifest os.ReadFile(OramaManifest)":                                                               allowOptroot,
	"pkg/install/prebuilt.go copyBinary os.MkdirAll(filepath.Dir(dest))":                                                                    allowCopyBinary,
	"pkg/install/prebuilt.go copyBinary os.Open(src)":                                                                                       allowCopyBinary,
	"pkg/install/prebuilt.go copyBinary os.OpenFile(dest)":                                                                                  allowCopyBinary,
	"pkg/install/prebuilt.go copyBinary os.Remove(dest)":                                                                                    allowCopyBinary,
	"pkg/install/privhelper.go (*ProductionSetup) ensurePrivHelper os.Chmod(privHelperDest)":                                                allowPrivhelper,
	"pkg/install/privhelper.go (*ProductionSetup) ensurePrivHelper os.Remove(legacySudoersPath)":                                            allowPrivhelper,
	"pkg/install/privhelper.go (*ProductionSetup) ensurePrivHelper os.WriteFile(path)":                                                      allowPrivhelper,
	"pkg/install/privhelper.go fileSHA256 os.Open(path)":                                                                                    allowPrivhelper,
	"pkg/install/privhelper.go var privHelperDest os.Chown(path)":                                                                           allowPrivhelper,
	"pkg/install/provisioner.go (*FilesystemProvisioner) EnsureOramaUser exec.Command(\"chown\", \"-R\", \"orama:orama\", fp.oramaDir)":     allowChownR,
	"pkg/install/provisioner.go lockOramaBinDir exec.Command(\"chown\", \"root:orama\", binDir)":                                            allowBin,
	"pkg/install/provisioner.go lockOramaBinDir exec.Command(\"chown\", \"root:orama\", p)":                                                 allowBin,
	"pkg/install/provisioner.go lockOramaBinDir os.Chmod(binDir)":                                                                           allowBin,
	"pkg/install/provisioner.go lockOramaBinDir os.Chmod(p)":                                                                                allowBin,
	"pkg/install/services.go (*SystemdController) WriteServiceUnit os.WriteFile(unitPath)":                                                  allowUnit,
	"pkg/install/gateway_unit.go installIndexGatewayDropIn os.MkdirAll(indexGatewayDropInDir)":                                              allowUnit,
	"pkg/install/gateway_unit.go installIndexGatewayDropIn os.WriteFile(path)":                                                              allowUnit,
	"pkg/install/wireguard.go (*WireGuardProvisioner) WriteConfig exec.Command(\"tee\", confPath)":                                          allowWg,
	"pkg/install/wireguard.go (*WireGuardProvisioner) WriteConfig os.MkdirAll(wp.configDir)":                                                allowWg,
	"pkg/install/wireguard.go (*WireGuardProvisioner) WriteConfig os.WriteFile(confPath)":                                                   allowWg,
	"pkg/install/wireguard.go forcePrivateMode os.Chmod(path)":                                                                              allowWg,
}

func TestRootRunCode_touchesOramaTreeOnlyThroughRootfs(t *testing.T) {
	_, file, _, _ := runtime.Caller(0)
	moduleRoot := filepath.Join(filepath.Dir(file), "..", "..")
	found := map[string]bool{}
	for _, dir := range rootScannedDirs {
		for _, key := range scanRootFileAccess(t, moduleRoot, dir) {
			found[key] = true
		}
	}
	var unexpected []string
	for key := range found {
		if _, ok := rootFileAccessAllowed[key]; !ok {
			unexpected = append(unexpected, key)
		}
	}
	sort.Strings(unexpected)
	for _, key := range unexpected {
		t.Errorf("direct file access in root-run code: %s\n\tuse OramaRoot (pkg/rootfs) for anything under /opt/orama/.orama, or add it to rootFileAccessAllowed with the root-owned tree it touches", key)
	}
	for key := range rootFileAccessAllowed {
		if !found[key] {
			t.Errorf("rootFileAccessAllowed entry matches no call any more; remove it: %s", key)
		}
	}
}

// scanRootFileAccess lists the guarded calls in dir's non-test files.
func scanRootFileAccess(t *testing.T, moduleRoot, dir string) []string {
	t.Helper()
	files, err := filepath.Glob(filepath.Join(moduleRoot, dir, "*.go"))
	if err != nil || len(files) == 0 {
		t.Fatalf("no Go files in %s: %v", dir, err)
	}
	var keys []string
	fset := token.NewFileSet()
	for _, path := range files {
		if strings.HasSuffix(path, "_test.go") {
			continue
		}
		f, err := parser.ParseFile(fset, path, nil, 0)
		if err != nil {
			t.Fatal(err)
		}
		rel := filepath.ToSlash(filepath.Join(dir, filepath.Base(path)))
		osName, execName := importNames(f)
		for _, decl := range f.Decls {
			scope := declName(decl)
			ast.Inspect(decl, func(n ast.Node) bool {
				call, ok := n.(*ast.CallExpr)
				if !ok {
					return true
				}
				if key, ok := guardedCall(call, osName, execName); ok {
					keys = append(keys, rel+" "+scope+" "+key)
				}
				return true
			})
		}
	}
	return keys
}

// importNames are the names os and os/exec are imported under ("" if not).
func importNames(f *ast.File) (osName, execName string) {
	for _, imp := range f.Imports {
		path, _ := strconv.Unquote(imp.Path.Value)
		name := ""
		switch path {
		case "os":
			name = "os"
		case "os/exec":
			name = "exec"
		default:
			continue
		}
		if imp.Name != nil {
			name = imp.Name.Name
		}
		if path == "os" {
			osName = name
		} else {
			execName = name
		}
	}
	return osName, execName
}

// declName names a top-level declaration: "(*T) Method", "Func", or the
// first name of a var/const block.
func declName(decl ast.Decl) string {
	switch d := decl.(type) {
	case *ast.FuncDecl:
		if d.Recv != nil && len(d.Recv.List) > 0 {
			return "(" + types.ExprString(d.Recv.List[0].Type) + ") " + d.Name.Name
		}
		return d.Name.Name
	case *ast.GenDecl:
		for _, spec := range d.Specs {
			if vs, ok := spec.(*ast.ValueSpec); ok && len(vs.Names) > 0 {
				return "var " + vs.Names[0].Name
			}
		}
	}
	return "decl"
}

// guardedCall reports whether call is a guarded os function or runs a guarded
// command, and names it with its first argument (the command's arguments).
func guardedCall(call *ast.CallExpr, osName, execName string) (string, bool) {
	sel, ok := call.Fun.(*ast.SelectorExpr)
	if !ok {
		return "", false
	}
	pkg, ok := sel.X.(*ast.Ident)
	if !ok {
		return "", false
	}
	switch {
	case osName != "" && pkg.Name == osName && guardedOSFuncs[sel.Sel.Name]:
		arg := ""
		if len(call.Args) > 0 {
			arg = types.ExprString(call.Args[0])
		}
		return "os." + sel.Sel.Name + "(" + arg + ")", true
	case execName != "" && pkg.Name == execName && (sel.Sel.Name == "Command" || sel.Sel.Name == "CommandContext"):
		args := call.Args
		if sel.Sel.Name == "CommandContext" && len(args) > 0 {
			args = args[1:]
		}
		if len(args) == 0 {
			return "", false
		}
		lit, ok := args[0].(*ast.BasicLit)
		if !ok {
			return "", false
		}
		name, err := strconv.Unquote(lit.Value)
		if err != nil || !guardedCommands[name] {
			return "", false
		}
		parts := make([]string, len(args))
		for i, a := range args {
			parts[i] = types.ExprString(a)
		}
		return "exec." + sel.Sel.Name + "(" + strings.Join(parts, ", ") + ")", true
	}
	return "", false
}
