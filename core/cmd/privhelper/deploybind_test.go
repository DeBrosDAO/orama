package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/privhelper"
	"github.com/DeBrosOfficial/network/pkg/rootfs"
)

// useBindTree points the helper's drop-in writes and reloads at a temporary
// tree and a recorder; it returns the unit directory and the reload count.
func useBindTree(t *testing.T, reload privhelper.Response) (string, *int) {
	t.Helper()
	anchor := t.TempDir()
	unitDir := filepath.Join(anchor, "systemd", "system")
	if err := os.MkdirAll(unitDir, 0o755); err != nil {
		t.Fatal(err)
	}
	reloads := 0
	oldRoot, oldDir, oldLock, oldReload := deployBindRoot, deployBindUnitDir, deployBindLockPath, daemonReload
	deployBindRoot, deployBindUnitDir = rootfs.At(anchor), unitDir
	deployBindLockPath = filepath.Join(t.TempDir(), "deploy-bind.lock")
	daemonReload = func() privhelper.Response { reloads++; return reload }
	t.Cleanup(func() {
		deployBindRoot, deployBindUnitDir, deployBindLockPath, daemonReload = oldRoot, oldDir, oldLock, oldReload
	})
	return unitDir, &reloads
}

// systemd reads a new drop-in only after a reload — systemd 249 does not even
// find a new drop-in directory for a unit it has never loaded — so a change
// reloads, and a rewrite of the same port does not.
func TestDeploy_BindPortWritesTheDropInAndReloadsOnlyOnAChange(t *testing.T) {
	unitDir, reloads := useBindTree(t, privhelper.Response{})

	resp := deploy([]string{"bind-port", "acme-web", "node", "10200"}, nil)
	if resp.ExitCode != 0 {
		t.Fatalf("bind-port: %+v", resp)
	}
	data, err := os.ReadFile(privhelper.DeployBindDropInPath(unitDir, "node", "acme-web"))
	if err != nil || string(data) != privhelper.DeployBindDropIn("acme-web", 10200) {
		t.Fatalf("drop-in %q, %v", data, err)
	}
	if *reloads != 1 {
		t.Errorf("%d reloads after a new drop-in, want 1", *reloads)
	}

	if resp := deploy([]string{"bind-port", "acme-web", "node", "10200"}, nil); resp.ExitCode != 0 {
		t.Fatalf("same port again: %+v", resp)
	}
	if *reloads != 1 {
		t.Errorf("%d reloads after an unchanged drop-in, want still 1", *reloads)
	}

	if resp := deploy([]string{"bind-port", "acme-web", "node", "10201"}, nil); resp.ExitCode != 0 {
		t.Fatalf("new port: %+v", resp)
	}
	if *reloads != 2 {
		t.Errorf("%d reloads after a port change, want 2", *reloads)
	}
}

// A failed reload leaves systemd with the previous drop-in loaded. The file is
// put back to match, so the next attempt sees a change and reloads again
// instead of reporting "unchanged" over a drop-in systemd never read.
func TestDeploy_BindPortPutsTheDropInBackWhenTheReloadFails(t *testing.T) {
	unitDir, reloads := useBindTree(t, privhelper.Response{})
	if resp := deploy([]string{"bind-port", "acme-web", "go", "10200"}, nil); resp.ExitCode != 0 {
		t.Fatalf("bind-port: %+v", resp)
	}

	daemonReload = func() privhelper.Response {
		*reloads++
		return privhelper.Response{ExitCode: 1, Output: "Failed to reload daemon: Connection timed out"}
	}
	resp := deploy([]string{"bind-port", "acme-web", "go", "10300"}, nil)
	if resp.ExitCode == 0 || !strings.Contains(resp.Output, "daemon-reload") {
		t.Fatalf("a failed reload reported %+v", resp)
	}
	data, _ := os.ReadFile(privhelper.DeployBindDropInPath(unitDir, "go", "acme-web"))
	if string(data) != privhelper.DeployBindDropIn("acme-web", 10200) {
		t.Errorf("after the failed reload the drop-in is %q, want the previous one", data)
	}

	daemonReload = func() privhelper.Response { *reloads++; return privhelper.Response{} }
	before := *reloads
	if resp := deploy([]string{"bind-port", "acme-web", "go", "10300"}, nil); resp.ExitCode != 0 {
		t.Fatalf("retry: %+v", resp)
	}
	if *reloads != before+1 {
		t.Error("the retry did not reload")
	}
}

// A first write whose reload fails leaves no drop-in at all.
func TestDeploy_BindPortLeavesNothingWhenTheFirstReloadFails(t *testing.T) {
	unitDir, _ := useBindTree(t, privhelper.Response{ExitCode: 1, Output: "no bus"})
	if resp := deploy([]string{"bind-port", "acme-web", "npm", "10200"}, nil); resp.ExitCode == 0 {
		t.Fatalf("a failed reload reported success: %+v", resp)
	}
	if _, err := os.Stat(filepath.Dir(privhelper.DeployBindDropInPath(unitDir, "npm", "acme-web"))); !os.IsNotExist(err) {
		t.Errorf("the drop-in directory is left behind: %v", err)
	}
}

// The executor re-checks the port it is handed, whichever way it arrived.
func TestDeploy_BindPortRefusesAPlatformPort(t *testing.T) {
	unitDir, reloads := useBindTree(t, privhelper.Response{})
	resp := deploy([]string{"bind-port", "acme-web", "node", "10104"}, nil)
	if resp.ExitCode != privhelper.ExitRefused {
		t.Fatalf("got %+v, want a refusal", resp)
	}
	if entries, _ := os.ReadDir(unitDir); len(entries) != 0 || *reloads != 0 {
		t.Errorf("a refused port left %v and %d reloads", entries, *reloads)
	}
}

// Removing a deployment removes its allowed port with its secrets.
func TestDeploy_ClearRemovesTheBindDropIns(t *testing.T) {
	if _, err := os.Stat("/var/lib/orama-deploy"); err == nil {
		t.Skip("this host has real deployment secrets; clear would touch them")
	}
	unitDir, _ := useBindTree(t, privhelper.Response{})
	if resp := deploy([]string{"bind-port", "acme-web", "node", "10200"}, nil); resp.ExitCode != 0 {
		t.Fatalf("bind-port: %+v", resp)
	}
	if resp := deploy([]string{"clear", "acme-web"}, nil); resp.ExitCode != 0 {
		t.Fatalf("clear: %+v", resp)
	}
	if _, err := os.Stat(privhelper.DeployBindDropInPath(unitDir, "node", "acme-web")); !os.IsNotExist(err) {
		t.Errorf("the drop-in outlived the deployment: %v", err)
	}
}
