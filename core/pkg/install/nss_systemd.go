package install

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
)

// Tenant deployments run as DynamicUser=. Such a user has no line in
// /etc/passwd: systemd hands out the uid at start, and the name and uid resolve
// only through nss-systemd, the "systemd" source in nsswitch.conf's passwd and
// group lines (libnss-systemd). Without it getpwuid() fails inside the unit,
// and with it every program that asks for its own user or home directory:
// npm exits 254 on os.homedir() (ENOENT) before it installs anything, in the
// build unit and in `npm start` alike. Ubuntu installs the module and
// configures it; Debian 12's cloud image does neither.

const (
	// nssSystemdPackage is the Debian/Ubuntu package that ships the module.
	nssSystemdPackage = "libnss-systemd"
	// nssSource is the nsswitch.conf source the module is named by.
	nssSource = "systemd"
	// nsswitchPath is the host's name-service configuration.
	nsswitchPath = "/etc/nsswitch.conf"
)

// nssDatabases are the nsswitch.conf lines a dynamic user is looked up by.
var nssDatabases = []string{"passwd", "group"}

// nssModuleGlobs locate the installed module, on the multiarch and the
// merged-usr layouts.
var nssModuleGlobs = []string{
	"/lib/*/libnss_systemd.so.2",
	"/usr/lib/*/libnss_systemd.so.2",
	"/lib/libnss_systemd.so.2",
	"/usr/lib/libnss_systemd.so.2",
}

// nssSystemd is the state EnsureDynamicUserNSS acts on, held in a struct so a
// test can point it at a directory.
type nssSystemd struct {
	nsswitch    string
	moduleGlobs []string
	installPkg  func(pkg string) error
}

func defaultNSSSystemd() nssSystemd {
	return nssSystemd{nsswitch: nsswitchPath, moduleGlobs: nssModuleGlobs, installPkg: aptInstall}
}

// EnsureDynamicUserNSS makes dynamic users resolvable on this host: it installs
// libnss-systemd when the module is missing, adds the systemd source to the
// passwd and group lines of nsswitch.conf when they lack it, and then verifies
// both. It returns an error naming the missing piece when it cannot, so an
// install or upgrade stops here instead of shipping units that fail at their
// first start.
func EnsureDynamicUserNSS() error { return defaultNSSSystemd().ensure() }

func (n nssSystemd) ensure() error {
	if !n.moduleInstalled() {
		if err := n.installPkg(nssSystemdPackage); err != nil {
			return fmt.Errorf("failed to install %s, which dynamic users (every tenant deployment) need to resolve: %w; install it with `apt-get install %s`", nssSystemdPackage, err, nssSystemdPackage)
		}
	}
	if err := n.editNsswitch(); err != nil {
		return err
	}
	return n.verify()
}

func (n nssSystemd) moduleInstalled() bool {
	for _, glob := range n.moduleGlobs {
		if matches, _ := filepath.Glob(glob); len(matches) > 0 {
			return true
		}
	}
	return false
}

// verify is the fail-closed check: the module is on disk and both lines name it.
func (n nssSystemd) verify() error {
	if !n.moduleInstalled() {
		return fmt.Errorf("libnss_systemd.so.2 is not installed: dynamic users cannot resolve, so npm and every program that looks up its own user fails in a tenant deployment; install %s", nssSystemdPackage)
	}
	data, err := os.ReadFile(n.nsswitch)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", n.nsswitch, err)
	}
	for _, db := range nssDatabases {
		sources, found := nssLineSources(string(data), db)
		if !found || !containsString(sources, nssSource) {
			return fmt.Errorf("%s has no %q source on its %s line: dynamic users cannot resolve, so npm and every program that looks up its own user fails in a tenant deployment; add it (e.g. `%s: files %s`)", n.nsswitch, nssSource, db, db, nssSource)
		}
	}
	return nil
}

// editNsswitch adds the systemd source after files (or compat) on each line
// that lacks it, leaving every other line and any trailing comment as it was.
// A file that needs no change is not rewritten. A symlinked nsswitch.conf is
// edited at its target, so the link survives.
func (n nssSystemd) editNsswitch() error {
	data, err := os.ReadFile(n.nsswitch)
	if err != nil {
		return fmt.Errorf("failed to read %s: %w", n.nsswitch, err)
	}
	edited, changed, err := addNSSSource(string(data), nssDatabases, nssSource)
	if err != nil {
		return fmt.Errorf("%s: %w", n.nsswitch, err)
	}
	if !changed {
		return nil
	}
	target, err := filepath.EvalSymlinks(n.nsswitch)
	if err != nil {
		return fmt.Errorf("failed to resolve %s: %w", n.nsswitch, err)
	}
	return replaceFileAtomic(target, []byte(edited))
}

// replaceFileAtomic replaces path with data, keeping its mode and owner: the
// data goes to a temp file in the same directory, is synced, and is renamed
// over path, then the directory is synced so the rename survives a crash. The
// temp file is removed on any failure.
func replaceFileAtomic(path string, data []byte) error {
	info, err := os.Lstat(path)
	if err != nil {
		return fmt.Errorf("failed to stat %s: %w", path, err)
	}
	dir := filepath.Dir(path)
	f, err := os.CreateTemp(dir, filepath.Base(path)+".orama-*")
	if err != nil {
		return fmt.Errorf("failed to create a temp file next to %s: %w", path, err)
	}
	tmp := f.Name()
	if err := writeTemp(f, data, info); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("failed to write %s for %s: %w", tmp, path, err)
	}
	if err := os.Rename(tmp, path); err != nil {
		os.Remove(tmp)
		return fmt.Errorf("failed to replace %s: %w", path, err)
	}
	d, err := os.Open(dir)
	if err != nil {
		return fmt.Errorf("failed to open %s to sync it: %w", dir, err)
	}
	defer d.Close()
	if err := d.Sync(); err != nil {
		return fmt.Errorf("failed to sync %s: %w", dir, err)
	}
	return nil
}

// writeTemp fills f, gives it info's mode and owner, syncs and closes it.
func writeTemp(f *os.File, data []byte, info os.FileInfo) error {
	if _, err := f.Write(data); err != nil {
		f.Close()
		return err
	}
	if err := f.Chmod(info.Mode().Perm()); err != nil {
		f.Close()
		return err
	}
	if st, ok := info.Sys().(*syscall.Stat_t); ok {
		if err := f.Chown(int(st.Uid), int(st.Gid)); err != nil {
			f.Close()
			return err
		}
	}
	if err := f.Sync(); err != nil {
		f.Close()
		return err
	}
	return f.Close()
}

// nssLineSources returns the sources of db's line, with the comment dropped.
func nssLineSources(conf, db string) ([]string, bool) {
	for _, line := range strings.Split(conf, "\n") {
		body, _, _ := strings.Cut(line, "#")
		name, rest, ok := strings.Cut(body, ":")
		if ok && strings.TrimSpace(name) == db {
			return strings.Fields(rest), true
		}
	}
	return nil, false
}

// addNSSSource returns conf with source added to each of dbs' lines that lacks
// it, and whether anything changed. A missing line is an error: inventing one
// would make up a lookup order for the host.
func addNSSSource(conf string, dbs []string, source string) (string, bool, error) {
	lines := strings.Split(conf, "\n")
	changed := false
	for _, db := range dbs {
		idx := -1
		for i, line := range lines {
			body, _, _ := strings.Cut(line, "#")
			if name, _, ok := strings.Cut(body, ":"); ok && strings.TrimSpace(name) == db {
				idx = i
				break
			}
		}
		if idx < 0 {
			return "", false, fmt.Errorf("has no %s line to add %s to", db, source)
		}
		body, comment, hasComment := strings.Cut(lines[idx], "#")
		name, rest, _ := strings.Cut(body, ":")
		fields := strings.Fields(rest)
		if containsString(fields, source) {
			continue
		}
		if len(fields) == 0 {
			return "", false, fmt.Errorf("line %d (%q) has an empty %s source list, whose default depends on the glibc version: write the list explicitly, e.g. `%s: files %s`", idx+1, strings.TrimSpace(lines[idx]), db, db, source)
		}
		at := nssInsertAt(fields)
		fields = append(fields[:at], append([]string{source}, fields[at:]...)...)
		out := strings.TrimSpace(name) + ":" + strings.Repeat(" ", max(1, 16-len(strings.TrimSpace(name))-1)) + strings.Join(fields, " ")
		if hasComment {
			out += " #" + comment
		}
		lines[idx] = out
		changed = true
	}
	return strings.Join(lines, "\n"), changed, nil
}

// nssInsertAt returns where a new source goes in fields: after files (or
// compat) and after the [STATUS=action] group that follows it, which belongs to
// that source. With neither present it is the end of the list.
func nssInsertAt(fields []string) int {
	for i, f := range fields {
		if f != "files" && f != "compat" {
			continue
		}
		at := i + 1
		for at < len(fields) && strings.HasPrefix(fields[at], "[") {
			for at < len(fields) && !strings.HasSuffix(fields[at], "]") {
				at++
			}
			at++
		}
		return min(at, len(fields))
	}
	return len(fields)
}

func containsString(list []string, s string) bool {
	for _, v := range list {
		if v == s {
			return true
		}
	}
	return false
}

const (
	aptGetPath = "/usr/bin/apt-get"
	aptPath    = "/usr/sbin:/usr/bin:/sbin:/bin"
)

func aptInstall(pkg string) error {
	cmd := exec.Command(aptGetPath, "install", "-y", "-qq", pkg)
	cmd.Env = []string{"DEBIAN_FRONTEND=noninteractive", "PATH=" + aptPath}
	if out, err := cmd.CombinedOutput(); err != nil {
		return fmt.Errorf("%s install %s: %w: %s", aptGetPath, pkg, err, strings.TrimSpace(string(out)))
	}
	return nil
}
