// Package tornettest builds a Tor network file for the tests of the packages that carry one.
package tornettest

import (
	"encoding/base64"
	"fmt"
	"testing"

	"github.com/DeBrosOfficial/network/pkg/tornet"
)

// NetworkFile is a valid private tor-network.json: three directory authorities on public
// addresses.
func NetworkFile(t testing.TB) []byte {
	t.Helper()
	n := tornet.Network{Name: "orama-teststage", Private: true, VotingIntervalMinutes: 30, VoteDelaySeconds: 300, DistDelaySeconds: 300}
	for i, addr := range []string{"57.129.166.16", "57.129.166.17", "161.97.184.199"} {
		n.Authorities = append(n.Authorities, tornet.Authority{
			Nickname: fmt.Sprintf("OramaAuth%d", i+1), Address: addr, ORPort: 31020, DirPort: 31021,
			V3Ident: fmt.Sprintf("%040X", 0xA0+i), Fingerprint: fmt.Sprintf("%040X", 0xB0+i),
			Ed25519ID: base64.RawStdEncoding.EncodeToString(append(make([]byte, 31), byte(i+1))),
		})
	}
	data, err := n.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	return data
}
