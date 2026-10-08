package orama

import (
	"encoding/hex"
	"net/http"
	"strings"
	"testing"
	"time"
)

// The same vectors are in core/pkg/tlsstore/vectors_test.go and
// core/pkg/auth/caddy_vectors_test.go. A drift on either side fails a test
// instead of a renewal on a node.
const (
	vectorMasterHex = "000102030405060708090a0b0c0d0e0f101112131415161718191a1b1c1d1e1f"
	vectorMACHex    = "619c0812d37db0ca397535bf2e097d1554f6f72e784d327cdd463281b33e5700"
	vectorSealHex   = "93db0bdb3c098c60f4a50623c2aa8ba4ca2c3750cd61b69b2ac7990d5322f1d5"
	vectorKey       = "certificates/ca/example/example.crt"
	vectorPlain     = "orama tls store vector"
	vectorSealed    = "v1.fOJgV3sz5FQVNCyYK4r7zX/9XbbQfOJ/COpWPCP8VdeB1ysFuiPNONVOfP9Eh7oSKSY="
)

func vectorStoreKeys(t *testing.T) (mac, seal []byte) {
	t.Helper()
	master, _ := hex.DecodeString(vectorMasterHex)
	mac, seal, err := storeKeys(master)
	if err != nil {
		t.Fatal(err)
	}
	return mac, seal
}

func TestStoreKeys_matchCore(t *testing.T) {
	mac, seal := vectorStoreKeys(t)
	if got := hex.EncodeToString(mac); got != vectorMACHex {
		t.Errorf("MAC key = %s, want %s", got, vectorMACHex)
	}
	if got := hex.EncodeToString(seal); got != vectorSealHex {
		t.Errorf("seal key = %s, want %s", got, vectorSealHex)
	}
}

func TestOpenValue_theCoreVector(t *testing.T) {
	_, seal := vectorStoreKeys(t)
	got, err := openValue(seal, vectorKey, vectorSealed)
	if err != nil || string(got) != vectorPlain {
		t.Fatalf("openValue = %q, %v", got, err)
	}
	if _, err := openValue(seal, "certificates/ca/other/other.crt", vectorSealed); err == nil {
		t.Error("a value opened under a key it was not sealed for")
	}
}

func TestSealValue_roundTripAndFormat(t *testing.T) {
	_, seal := vectorStoreKeys(t)
	sealed, err := sealValue(seal, "acme/a.key", []byte("PRIVATE KEY"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(sealed, sealedPrefix) || strings.Contains(sealed, "PRIVATE") {
		t.Fatalf("sealed value %q has the wrong shape", sealed)
	}
	got, err := openValue(seal, "acme/a.key", sealed)
	if err != nil || string(got) != "PRIVATE KEY" {
		t.Fatalf("openValue = %q, %v", got, err)
	}
}

func TestCoordinationV2MAC_matchesCore(t *testing.T) {
	mac, _ := vectorStoreKeys(t)
	got := coordinationV2MAC(mac, "post", storeAudience, "/v1/internal/tls-store", "",
		[]byte(`{"op":"load","key":"a"}`), "00112233445566778899aabbccddeeff", 1790000000)
	if want := "37ec7972806cd402f94ead221547054c3238dee9d8bfe8dbcd61d6ecc100863a"; got != want {
		t.Fatalf("coordination v2 MAC = %s, want %s", got, want)
	}
}

func TestProviderSign_matchesCore(t *testing.T) {
	req, _ := http.NewRequest(http.MethodPost, "http://localhost:6001/v1/internal/acme/present", nil)
	sign([]byte("provider-test-key"), req, []byte(`{"fqdn":"_acme-challenge.example.com.","value":"x"}`), time.Unix(1790000000, 0))
	want := "1790000000.932194e4680fbf6651b9cf0642913158f59c7a25f4092808e906c12af317c419"
	if got := req.Header.Get(acmeMACHeader); got != want {
		t.Fatalf("provider stamp = %s, want %s", got, want)
	}
}

// maxLease is core/pkg/tlsstore.MaxLease, which the store enforces.
func TestMaxLease_matchesCore(t *testing.T) {
	if maxLease != 2*time.Hour {
		t.Fatalf("maxLease = %s, want core's MaxLease of 2h", maxLease)
	}
}
