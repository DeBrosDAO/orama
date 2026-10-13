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
	if err := sign([]byte("provider-test-key"), req, []byte(`{"fqdn":"_acme-challenge.example.com.","value":"x"}`), time.Unix(1790000000, 0)); err != nil {
		t.Fatal(err)
	}
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

func TestACMENoncedMAC_matchesCore(t *testing.T) {
	got := acmeNoncedMAC([]byte("provider-test-key"), "post", "/v1/internal/acme/present", "",
		[]byte(`{"fqdn":"_acme-challenge.example.com.","value":"x"}`), "00112233445566778899aabbccddeeff", 1790000000)
	if want := "979cf48ef2b884a0b5bad5129fd9a4c471bc3e9b031587e7d82f1b5675b82af8"; got != want {
		t.Fatalf("nonced ACME MAC = %s, want %s", got, want)
	}
}

func TestProviderSign_carriesTheNoncedStampBesideTheUnnoncedOne(t *testing.T) {
	key, body := []byte("provider-test-key"), []byte(`{"fqdn":"_acme-challenge.example.com.","value":"x"}`)
	req, _ := http.NewRequest(http.MethodPost, "http://localhost:6001/v1/internal/acme/cleanup", nil)
	if err := sign(key, req, body, time.Unix(1790000000, 0)); err != nil {
		t.Fatal(err)
	}
	nonce := req.Header.Get(acmeNonceHeader)
	want := "1790000000." + acmeNoncedMAC(key, "POST", "/v1/internal/acme/cleanup", "", body, nonce, 1790000000)
	if got := req.Header.Get(acmeMACV2Header); got != want || len(nonce) != 2*nonceBytes {
		t.Fatalf("nonced stamp = %s (nonce %q), want %s", got, nonce, want)
	}
	if req.Header.Get(acmeMACHeader) == "" {
		t.Fatal("no unnonced stamp for a gateway built before the nonce")
	}
	again, _ := http.NewRequest(http.MethodPost, "http://localhost:6001/v1/internal/acme/cleanup", nil)
	if err := sign(key, again, body, time.Unix(1790000000, 0)); err != nil {
		t.Fatal(err)
	}
	if again.Header.Get(acmeNonceHeader) == nonce {
		t.Fatal("two calls drew the same nonce")
	}
}

func TestCoordinationV3MAC_matchesCore(t *testing.T) {
	mac, _ := vectorStoreKeys(t)
	got := coordinationV3MAC(mac, "post", storeAudience, "6001", "/v1/internal/tls-store", "",
		[]byte(`{"op":"load","key":"a"}`), "00112233445566778899aabbccddeeff", 1790000000)
	if want := "e9c2a85ba619b989508ab660c2f98e006aa81cb37113f768a3a3ec1860795ff6"; got != want {
		t.Fatalf("coordination v3 MAC = %s, want %s", got, want)
	}
}

func TestSignV2_stampsV3ForTheTargetPortBesideV2(t *testing.T) {
	mac, _ := vectorStoreKeys(t)
	body := []byte(`{"op":"load","key":"a"}`)
	req, _ := http.NewRequest(http.MethodPost, "http://127.0.0.1:6001/v1/internal/tls-store", nil)
	if err := signV2(mac, req, body, storeAudience, time.Unix(1790000000, 0)); err != nil {
		t.Fatal(err)
	}
	nonce := req.Header.Get(nonceHeader)
	if got, want := req.Header.Get(macV3Header), "1790000000."+coordinationV3MAC(mac, "POST", storeAudience, "6001", "/v1/internal/tls-store", "", body, nonce, 1790000000); got != want {
		t.Fatalf("v3 stamp = %s, want %s", got, want)
	}
	if got, want := req.Header.Get(macV2Header), "1790000000."+coordinationV2MAC(mac, "POST", storeAudience, "/v1/internal/tls-store", "", body, nonce, 1790000000); got != want {
		t.Fatalf("v2 stamp = %s, want %s", got, want)
	}
}
