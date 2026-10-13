package ipfs

import (
	"bytes"
	"testing"
)

func TestSealOpenRoundTrip(t *testing.T) {
	key := bytes.Repeat([]byte{0x42}, 32)
	plain := []byte("tenant-bytes-not-for-disk")
	sealed, err := sealBlob(plain, key)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Contains(sealed, plain) {
		t.Fatal("ciphertext still contains plaintext")
	}
	if !hasWrapEnvelope(sealed) {
		t.Fatal("missing envelope magic")
	}
	got, err := openBlob(sealed, key)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("got %q", got)
	}
}

func TestOpenBlob_plaintextPassthrough(t *testing.T) {
	plain := []byte("historical-cid-bytes")
	got, err := openBlob(plain, bytes.Repeat([]byte{1}, 32))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, plain) {
		t.Fatalf("got %q", got)
	}
}

func TestWrapPrivateBlob_skipsTarball(t *testing.T) {
	if wrapPrivateBlob("app.tar.gz") {
		t.Fatal("tarball deploys must not be wrapped")
	}
	if !wrapPrivateBlob("photo.png") {
		t.Fatal("private blobs must wrap")
	}
}
