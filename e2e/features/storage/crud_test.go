//go:build e2e_fleet

package storage

import (
	"bytes"
	"net/http"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/e2e/features/internal/tenancy"
	"github.com/DeBrosOfficial/network/e2e/harness"
	"github.com/DeBrosOfficial/network/e2e/harness/gw"
	"github.com/DeBrosOfficial/network/e2e/harness/ns"
)

const (
	smallBytes = 4 << 10
	// largeBytes stays under the harness's 32 MiB response cap and over the
	// multipart parser's in-memory threshold, so the part goes to disk.
	largeBytes = 20 << 20
)

// TestUpload_multipartRoundTrip: the SDK's storage.upload()/get()
// (docs/whitepaper/technical-reference/appendices/i-api-surface.md#storage) stores bytes and returns them unchanged,
// named as uploaded, with the logical size. A multipart file name is reduced to
// its last element by the standard library, so a path-like name travels in the
// JSON form.
func TestUpload_multipartRoundTrip(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	data := randomBytes(t, smallBytes)
	u := upload(t, n.Client, tenancy.Owner(n), "readme.bin", data)
	if u.Name != "readme.bin" || u.Size != int64(len(data)) {
		t.Errorf("upload answered name %q size %d, want readme.bin %d", u.Name, u.Size, len(data))
	}
	waitContent(t, n.Client, tenancy.Owner(n), u.Cid, data)
	r := get(t, n.Client, tenancy.Owner(n), u.Cid)
	if ct := r.Header.Get("Content-Type"); ct != "application/octet-stream" {
		t.Errorf("download content type %q, want application/octet-stream", ct)
	}
	if cd := r.Header.Get("Content-Disposition"); !strings.Contains(cd, u.Cid) {
		t.Errorf("download disposition %q does not name the CID", cd)
	}
	// A download is a user's data: it must not land in a client's disk cache
	// (docs/whitepaper/technical-reference/vol1/14-authorization.md#consistency-and-caching, bugboard #735).
	if cc := r.Header.Values("Cache-Control"); len(cc) != 1 || cc[0] != "no-store" {
		t.Errorf("download Cache-Control %v, want exactly no-store", cc)
	}
}

// TestUpload_jsonBase64RoundTrip: the JSON form {name, data} is what the
// SDK sends for small blobs; unicode names survive.
func TestUpload_jsonBase64RoundTrip(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	data := randomBytes(t, smallBytes)
	var u uploaded
	if err := uploadJSON(t, n.Client, tenancy.Owner(n), "фото/日本.png", data).Expect(t, http.StatusOK).Decode(&u); err != nil {
		t.Fatal(err)
	}
	if u.Name != "фото/日本.png" {
		t.Errorf("unicode name came back as %q", u.Name)
	}
	waitContent(t, n.Client, tenancy.Owner(n), u.Cid, data)
}

// TestUpload_largeFile: a 20 MiB multipart upload round-trips byte for byte.
func TestUpload_largeFile(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	data := randomBytes(t, largeBytes)
	u := upload(t, n.Client, tenancy.Owner(n), "big.bin", data)
	if u.Size != largeBytes {
		t.Errorf("size %d, want %d", u.Size, largeBytes)
	}
	waitContent(t, n.Client, tenancy.Owner(n), u.Cid, data)
}

// TestUpload_malformedInput: every malformed upload is a 4xx, never a 5xx
// and never a stored object.
func TestUpload_malformedInput(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	who := tenancy.Owner(n)
	over := bytes.Repeat([]byte("A"), jsonBodyLimit+1)
	cases := map[string]*gw.Response{
		"json without data":      tenancy.Post(t, n.Client, pathUpload, who, map[string]string{"name": "x"}),
		"json invalid base64":    tenancy.Post(t, n.Client, pathUpload, who, map[string]string{"data": "!!not base64!!"}),
		"json wrong type":        tenancy.Post(t, n.Client, pathUpload, who, []byte(`{"data":42}`)),
		"json truncated":         tenancy.Post(t, n.Client, pathUpload, who, []byte(`{"data":"aGk=`)),
		"json over 1 MiB":        tenancy.Post(t, n.Client, pathUpload, who, append(append([]byte(`{"data":"`), over...), '"', '}')),
		"multipart without file": multipartNoFile(t, n.Client, who),
		"name with ..":           uploadJSON(t, n.Client, who, "avatars/../keys/x", []byte("x")),
		"name over 1024 chars":   uploadRaw(t, n.Client, who, strings.Repeat("n", 1025), []byte("x")),
	}
	for name, r := range cases {
		if r.Status != http.StatusBadRequest && r.Status != http.StatusRequestEntityTooLarge {
			t.Errorf("%s: want 400/413, got %d: %.200s", name, r.Status, r.Body)
		}
	}
	if r := n.Client.MustSend(t, gw.Req{Path: pathUpload, Bearer: who.Bearer}); r.Status != http.StatusMethodNotAllowed {
		t.Errorf("GET upload: want 405, got %d", r.Status)
	}
}

func multipartNoFile(t testing.TB, c *gw.Client, who tenancy.Cred) *gw.Response {
	t.Helper()
	body := "--b\r\nContent-Disposition: form-data; name=\"pin\"\r\n\r\ntrue\r\n--b--\r\n"
	return c.MustSend(t, gw.Req{Method: http.MethodPost, Path: pathUpload, Bearer: who.Bearer,
		Header: http.Header{"Content-Type": {"multipart/form-data; boundary=b"}}, Body: []byte(body)})
}

// TestUpload_nameNormalized: `/a//b/./c` and `a/b/c` are the same name
// (docs/whitepaper/technical-reference/vol1/14-authorization.md#where-a-requests-permissions-come-from; core NormalizeStoragePath).
func TestUpload_nameNormalized(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	var u uploaded
	if err := uploadJSON(t, n.Client, tenancy.Owner(n), "  /avatars//me/./photo.png ", []byte("x")).Expect(t, http.StatusOK).Decode(&u); err != nil {
		t.Fatal(err)
	}
	if u.Name != "avatars/me/photo.png" {
		t.Errorf("name normalised to %q, want avatars/me/photo.png", u.Name)
	}
}

// TestUpload_emptyFile: a zero-byte object is stored and returned empty, or
// refused as bad input — never a server error.
func TestUpload_emptyFile(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	r := uploadRaw(t, n.Client, tenancy.Owner(n), "empty", nil)
	switch r.Status {
	case http.StatusBadRequest:
	case http.StatusOK:
		var u uploaded
		if err := r.Decode(&u); err != nil || u.Cid == "" {
			t.Fatalf("empty upload: %v %s", err, r.Body)
		}
		waitContent(t, n.Client, tenancy.Owner(n), u.Cid, []byte{})
	default:
		t.Fatalf("empty upload: %d %.200s", r.Status, r.Body)
	}
}

// TestGet_rangeHeaderNeverWrongSlice: the download handler streams the whole
// object; a Range request gets 206 with exactly the range or 200 with all of
// it, never a different slice.
func TestGet_rangeHeaderNeverWrongSlice(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	data := randomBytes(t, smallBytes)
	u := upload(t, n.Client, tenancy.Owner(n), "range.bin", data)
	waitContent(t, n.Client, tenancy.Owner(n), u.Cid, data)
	r := n.Client.MustSend(t, gw.Req{Path: pathGet + u.Cid, Bearer: n.Owner.Token(), Header: http.Header{"Range": {"bytes=10-19"}}})
	switch r.Status {
	case http.StatusPartialContent:
		if !bytes.Equal(r.Body, data[10:20]) {
			t.Errorf("206 body is not bytes 10-19")
		}
	case http.StatusOK:
		if !bytes.Equal(r.Body, data) {
			t.Errorf("200 to a Range request is not the whole object")
		}
	default:
		t.Errorf("Range request: %d %.200s", r.Status, r.Body)
	}
}

// TestGet_invalidCID: a CID that does not parse, or is not in canonical form,
// is 400 VALIDATION_FAILED before any lookup (download_handler.go).
func TestGet_invalidCID(t *testing.T) {
	t.Parallel()
	n := tenancy.Namespace(t, harness.Fleet(t), ns.Options{})
	for _, cid := range []string{"not-a-cid", "QmInvalid", "bafy" + strings.Repeat("z", 60), "%00", "Qm%20x"} {
		r := get(t, n.Client, tenancy.Owner(n), cid)
		if r.Status != http.StatusBadRequest {
			t.Errorf("GET %q: want 400, got %d: %.200s", cid, r.Status, r.Body)
		}
	}
}
