package wireguard

import (
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

func seedConf(t *testing.T) *Conf {
	t.Helper()
	path := filepath.Join(t.TempDir(), "wg0.conf")
	body := "[Interface]\nPrivateKey = k=\nAddress = 10.0.0.1/24\n\n[Peer]\nPublicKey = a=\nEndpoint = 203.0.113.2:51820\nAllowedIPs = 10.0.0.2/32\nPersistentKeepalive = 25\n"
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatal(err)
	}
	return NewConf(path)
}

func TestConf_PeersParsesEachSection(t *testing.T) {
	peers, err := seedConf(t).Peers()
	if err != nil || len(peers) != 1 || peers[0] != (Peer{PublicKey: "a=", Endpoint: "203.0.113.2:51820", AllowedIP: "10.0.0.2/32"}) {
		t.Fatalf("got %+v, %v", peers, err)
	}
}

// A node replaced on the same overlay IP comes back with a new key; the old
// key must not keep the address.
func TestConf_AddPeerReplacesByKeyOrAddress(t *testing.T) {
	c := seedConf(t)
	if err := c.AddPeer(Peer{PublicKey: "b=", Endpoint: "203.0.113.9:51820", AllowedIP: "10.0.0.2/32"}); err != nil {
		t.Fatal(err)
	}
	peers, _ := c.Peers()
	if len(peers) != 1 || peers[0].PublicKey != "b=" {
		t.Fatalf("got %+v", peers)
	}
}

func TestConf_RemovePeersByAllowedIPIsExact(t *testing.T) {
	c := seedConf(t)
	c.AddPeer(Peer{PublicKey: "c=", AllowedIP: "10.0.0.22/32"})
	n, err := c.RemovePeersByAllowedIP("10.0.0.2/32")
	if err != nil || n != 1 {
		t.Fatalf("removed %d, %v", n, err)
	}
	peers, _ := c.Peers()
	if len(peers) != 1 || peers[0].AllowedIP != "10.0.0.22/32" {
		t.Fatalf("10.0.0.22 must survive removing 10.0.0.2, got %+v", peers)
	}
}

// The helper serves requests in parallel; without the lock concurrent adds
// read, modify and rename independently and lose each other's peers.
func TestConf_ConcurrentAddsLoseNothing(t *testing.T) {
	c := seedConf(t)
	var wg sync.WaitGroup
	for i := 10; i < 30; i++ {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			if err := c.AddPeer(Peer{PublicKey: fmt.Sprintf("k%d=", i), AllowedIP: fmt.Sprintf("10.0.0.%d/32", i)}); err != nil {
				t.Error(err)
			}
		}(i)
	}
	wg.Wait()
	peers, _ := c.Peers()
	if len(peers) != 21 {
		t.Fatalf("got %d peers, want 21 (1 seed + 20 concurrent adds)", len(peers))
	}
}
