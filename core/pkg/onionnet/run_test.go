package onionnet

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// fakeTor writes an executable that stands in for tor: it saves the torrc it
// was given beside it, prints body, and then waits to be interrupted.
func fakeTor(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "tor")
	script := "#!/bin/sh\ncp \"$2\" \"$2.seen\"\necho \"$@\" > \"$2.args\"\ntrap 'exit 0' INT TERM\n" + body + "\n"
	if err := os.WriteFile(path, []byte(script), 0o700); err != nil {
		t.Fatal(err)
	}
	return path
}

func startOpts(t *testing.T) Options {
	t.Helper()
	addr, err := FreeLoopbackAddr()
	if err != nil {
		t.Fatal(err)
	}
	return Options{DataDir: filepath.Join(t.TempDir(), "data"), SocksAddr: addr}
}

func TestStart_returnsOnceBootstrappedAndRunsTheNetworksTorrc(t *testing.T) {
	bin := fakeTor(t, "echo 'Bootstrapped 5% (conn)'\necho 'Bootstrapped 100% (done): Done'\nwhile true; do sleep 0.1; done")
	opts := startOpts(t)
	var log bytes.Buffer
	tor, err := Start(context.Background(), bin, testNetwork(), opts, &log)
	if err != nil {
		t.Fatal(err)
	}
	if tor.SocksAddr != opts.SocksAddr {
		t.Errorf("SocksAddr = %q", tor.SocksAddr)
	}
	if defaults, err := os.ReadFile(filepath.Join(opts.DataDir, defaultsName)); err != nil || len(defaults) != 0 {
		t.Errorf("the distribution's torrc-defaults must be replaced by an empty file: %v %q", err, defaults)
	}
	args, err := os.ReadFile(filepath.Join(opts.DataDir, torrcName+".args"))
	if err != nil || !strings.Contains(string(args), "--defaults-torrc "+filepath.Join(opts.DataDir, defaultsName)) {
		t.Errorf("tor was not given the empty defaults file: %v %q", err, args)
	}
	seen, err := os.ReadFile(filepath.Join(opts.DataDir, torrcName+".seen"))
	if err != nil || !strings.Contains(string(seen), "DirAuthority autha") {
		t.Fatalf("tor was not started on the network's torrc: %v\n%s", err, seen)
	}
	info, _ := os.Stat(filepath.Join(opts.DataDir, torrcName))
	if info.Mode().Perm() != torrcMode {
		t.Errorf("torrc mode %o", info.Mode().Perm())
	}
	select {
	case <-tor.Done():
		t.Fatal("tor exited by itself")
	default:
	}
	if err := tor.Stop(); err != nil {
		t.Fatal(err)
	}
	select {
	case <-tor.Done():
	default:
		t.Fatal("Stop returned while tor was running")
	}
	if tor.Err() != nil {
		t.Errorf("a stop we asked for is an error: %v", tor.Err())
	}
	if !strings.Contains(log.String(), "Bootstrapped 100%") {
		t.Errorf("tor's log was not passed on: %q", log.String())
	}
	if err := tor.Stop(); err != nil {
		t.Errorf("second Stop: %v", err)
	}
}

func TestStart_aTorThatExitsBeforeBootstrappingIsAnErrorWithItsLog(t *testing.T) {
	bin := fakeTor(t, "echo '[warn] no directory authority answered'\nexit 3")
	_, err := Start(context.Background(), bin, testNetwork(), startOpts(t), nil)
	if err == nil || !strings.Contains(err.Error(), "no directory authority answered") || !strings.Contains(err.Error(), "exit status 3") {
		t.Fatalf("err = %v", err)
	}
}

func TestStart_contextEndsTor(t *testing.T) {
	bin := fakeTor(t, "echo 'Bootstrapped 100%'\nwhile true; do sleep 0.1; done")
	ctx, cancel := context.WithCancel(context.Background())
	tor, err := Start(ctx, bin, testNetwork(), startOpts(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	cancel()
	select {
	case <-tor.Done():
	case <-time.After(15 * time.Second):
		t.Fatal("tor outlived the context")
	}
}

func TestStart_refusals(t *testing.T) {
	if _, err := Start(context.Background(), "/nonexistent/tor", testNetwork(), startOpts(t), nil); err == nil || !strings.Contains(err.Error(), "start tor") {
		t.Errorf("a missing binary: %v", err)
	}
	n := testNetwork()
	n.Private = false
	if _, err := Start(context.Background(), fakeTor(t, "exit 0"), n, startOpts(t), nil); err == nil {
		t.Error("a public network started")
	}
	bad := startOpts(t)
	bad.SocksAddr = "0.0.0.0:9050"
	if _, err := Start(context.Background(), fakeTor(t, "exit 0"), testNetwork(), bad, nil); err == nil {
		t.Error("a non-loopback SOCKS address started")
	}
}

func TestStart_aTorThatDiesAfterBootstrappingReportsWhy(t *testing.T) {
	bin := fakeTor(t, "echo 'Bootstrapped 100%'\nsleep 0.2\nexit 7")
	tor, err := Start(context.Background(), bin, testNetwork(), startOpts(t), nil)
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-tor.Done():
	case <-time.After(15 * time.Second):
		t.Fatal("tor never exited")
	}
	if tor.Err() == nil || !strings.Contains(tor.Err().Error(), "exit status 7") {
		t.Errorf("an exit nobody asked for must carry its status, got %v", tor.Err())
	}
}

func TestStartOnFreePort_usesTheCacheDirectoryAndALoopbackPort(t *testing.T) {
	cache := t.TempDir()
	t.Setenv("HOME", cache)
	t.Setenv("XDG_CACHE_HOME", cache)
	tor, err := StartOnFreePort(context.Background(), fakeTor(t, "echo 'Bootstrapped 100%'\nwhile true; do sleep 0.1; done"), testNetwork(), "", nil)
	if err != nil {
		t.Fatal(err)
	}
	defer tor.Stop()
	if err := ValidateLoopback(tor.SocksAddr); err != nil {
		t.Error(err)
	}
	dir, err := DefaultDataDir("stagenet")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, torrcName)); err != nil {
		t.Errorf("no torrc in the network's cache directory %s: %v", dir, err)
	}
}

func TestFreeLoopbackAddr(t *testing.T) {
	a, err := FreeLoopbackAddr()
	if err != nil {
		t.Fatal(err)
	}
	if err := ValidateLoopback(a); err != nil {
		t.Fatal(err)
	}
}
