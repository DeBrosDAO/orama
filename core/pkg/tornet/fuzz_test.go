package tornet

import (
	"reflect"
	"strings"
	"testing"
)

// FuzzTornetNetwork: the Tor network file is read by every relay and client, and rendered into
// torrc lines. For any bytes ParseNetwork never panics; an accepted file is private, has
// authorities at public IPv4 addresses, and writes and reads back unchanged; and no torrc line
// built from it can be split into another line (a nickname or address that held a newline would add
// a directive to every node's configuration).
func FuzzTornetNetwork(f *testing.F) {
	// The stagenet network file is in the committed corpus (testdata/fuzz, made from
	// networks/stagenet by make -C chain fuzz-corpus), so this target needs nothing outside core.
	f.Add([]byte(`{}`))
	f.Add([]byte(`{"name":"x","private":false}`))
	f.Add([]byte(`null`))
	f.Fuzz(func(t *testing.T, data []byte) {
		n, err := ParseNetwork(data)
		if err != nil {
			return
		}
		if !n.Private {
			t.Fatal("a network that is not private was accepted")
		}
		for _, a := range n.Authorities {
			if PublicIPv4(a.Address) != nil {
				t.Fatalf("authority %s at %q is not a public IPv4 address", a.Nickname, a.Address)
			}
			if a.ORPort <= 0 || a.ORPort > 65535 || a.DirPort <= 0 || a.DirPort > 65535 {
				t.Fatalf("authority %s has ports %d/%d", a.Nickname, a.ORPort, a.DirPort)
			}
		}
		for _, line := range n.DirAuthorityLines() {
			if strings.ContainsAny(line, "\n\r\x00") || len(strings.Fields(line)) != 6 || !strings.HasPrefix(line, "DirAuthority ") {
				t.Fatalf("torrc line %q can be split or is not one DirAuthority directive", line)
			}
		}
		out, err := n.Marshal()
		if err != nil {
			t.Fatalf("an accepted network does not marshal: %v", err)
		}
		again, err := ParseNetwork(out)
		if err != nil || !reflect.DeepEqual(again, n) {
			t.Fatalf("a network does not survive a round trip (%v)", err)
		}
		for _, a := range n.Authorities {
			if got, ok := n.AuthorityAt(a.Address); !ok || got.Address != a.Address {
				t.Fatalf("AuthorityAt(%s) does not find its authority", a.Address)
			}
		}
	})
}
