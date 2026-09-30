package sshx_test

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/harness/sshx"
	"github.com/DeBrosOfficial/network/e2e/harness/sshx/sshxtest"
)

// echoServer answers each line with "echo: <line>".
func echoServer(t *testing.T) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { ln.Close() })
	go func() {
		for {
			c, err := ln.Accept()
			if err != nil {
				return
			}
			go func() {
				defer c.Close()
				sc := bufio.NewScanner(c)
				for sc.Scan() {
					fmt.Fprintf(c, "echo: %s\n", sc.Text())
				}
			}()
		}
	}()
	return ln.Addr().String()
}

func roundTrip(t *testing.T, addr, line string) string {
	t.Helper()
	c, err := net.Dial("tcp", addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	fmt.Fprintln(c, line)
	got, err := bufio.NewReader(c).ReadString('\n')
	if err != nil {
		t.Fatalf("read through %s: %v", addr, err)
	}
	return strings.TrimSpace(got)
}

func TestLocalForward_reachesTheNodesLoopback(t *testing.T) {
	srv := sshxtest.Start(t)
	remote := echoServer(t) // "on the node": the test sshd dials from this machine
	f, err := sshx.LocalForward(context.Background(), srv.Target(), remote)
	if err != nil {
		t.Fatal(err)
	}
	for i := range 3 {
		if got := roundTrip(t, f.Addr, fmt.Sprint("ping", i)); got != fmt.Sprint("echo: ping", i) {
			t.Fatalf("got %q", got)
		}
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if c, err := net.Dial("tcp", f.Addr); err == nil {
		c.Close()
		t.Fatal("the local listener is still open after Close")
	}
}

func TestRemoteForward_nodeReachesTheTestProcess(t *testing.T) {
	srv := sshxtest.Start(t)
	mock := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.WriteString(w, "mock provider saw "+r.URL.Path)
	}))
	defer mock.Close()
	f, err := sshx.RemoteForward(context.Background(), srv.Target(), strings.TrimPrefix(mock.URL, "http://"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(f.Addr, "127.0.0.1:") || f.Addr == "127.0.0.1:0" {
		t.Fatalf("node address %q", f.Addr)
	}
	out, _, exit, err := sshx.Run(context.Background(), srv.Target(), "curl -s http://"+f.Addr+"/3/device/abc")
	if err != nil || exit != 0 || out != "mock provider saw /3/device/abc" {
		t.Fatalf("from the node: %q exit %d err %v", out, exit, err)
	}
	if err := f.Close(); err != nil {
		t.Fatal(err)
	}
	if c, err := net.Dial("tcp", f.Addr); err == nil {
		c.Close()
		t.Fatal("the node-side listener is still open after Close")
	}
}

func TestLocalForward_unreachableTargetClosesTheConnection(t *testing.T) {
	srv := sshxtest.Start(t)
	f, err := sshx.LocalForward(context.Background(), srv.Target(), "127.0.0.1:1")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	c, err := net.Dial("tcp", f.Addr)
	if err != nil {
		t.Fatal(err)
	}
	defer c.Close()
	if _, err := bufio.NewReader(c).ReadString('\n'); err == nil {
		t.Fatal("a forward to a closed port returned data")
	}
}

func TestLocalForward_badTargetRefused(t *testing.T) {
	srv := sshxtest.Start(t)
	bad := srv.Target()
	bad.KnownHostsFile = t.TempDir() + "/empty"
	if _, err := sshx.LocalForward(context.Background(), bad, "127.0.0.1:80"); err == nil {
		t.Fatal("a forward to an unpinned host was opened")
	}
	if _, err := sshx.RemoteForward(context.Background(), sshx.Target{}, "127.0.0.1:80"); err == nil {
		t.Fatal("a forward with an empty target was opened")
	}
}
