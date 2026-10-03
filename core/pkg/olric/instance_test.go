package olric

import (
	"context"
	"net"
	"testing"
)

func TestOlricInstanceIsHealthy_listeningPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	oi := &OlricInstance{AdvertiseAddr: "127.0.0.1", MemberlistPort: ln.Addr().(*net.TCPAddr).Port}
	if ok, err := oi.IsHealthy(context.Background()); !ok || err != nil {
		t.Fatalf("a listening memberlist port read unhealthy: %v", err)
	}
}

func TestOlricInstanceIsHealthy_closedPort(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	port := ln.Addr().(*net.TCPAddr).Port
	ln.Close()
	oi := &OlricInstance{AdvertiseAddr: "127.0.0.1", MemberlistPort: port}
	if ok, err := oi.IsHealthy(context.Background()); ok || err == nil {
		t.Fatal("a closed memberlist port read healthy")
	}
}

func TestOlricInstanceAdvertisedDSN(t *testing.T) {
	if got := (&OlricInstance{AdvertiseAddr: "10.0.0.2", HTTPPort: 10002}).AdvertisedDSN(); got != "10.0.0.2:10002" {
		t.Fatalf("AdvertisedDSN = %q", got)
	}
}
