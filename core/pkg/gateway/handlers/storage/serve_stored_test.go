package storage

import (
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
)

// streamOnlyBody is a buffered object that must be copied through, not read
// into a second buffer: Read fails the test, WriteTo is the way out.
type streamOnlyBody struct {
	t       *testing.T
	content string
	wrote   bool
}

func (b *streamOnlyBody) Read([]byte) (int, error) {
	b.t.Error("the handler read the buffered object into a second buffer")
	return 0, io.EOF
}
func (b *streamOnlyBody) Close() error { return nil }
func (b *streamOnlyBody) Len() int     { return len(b.content) }
func (b *streamOnlyBody) WriteTo(w io.Writer) (int64, error) {
	b.wrote = true
	n, err := io.WriteString(w, b.content)
	return int64(n), err
}

func TestServeStored_copiesTheBufferedObjectWithoutBufferingItAgain(t *testing.T) {
	body := &streamOnlyBody{t: t, content: "file contents"}
	mock := &mockIPFSClient{getReader: body, heldLocally: true}
	h, gate := fetchHandlers(t, mock, &countingDB{ownershipDB: ownershipDB{owned: true}})
	caps := mintTokens(t, gate, testCID, "dev-1", 1)

	for name, serve := range map[string]func() *httptest.ResponseRecorder{
		"relayed": func() *httptest.ResponseRecorder { return serveRelayed(h, relayedGET(testCID, caps[0].Token)) },
		"get": func() *httptest.ResponseRecorder {
			rec := httptest.NewRecorder()
			h.DownloadHandler(rec, withNamespace(httptest.NewRequest(http.MethodGet, "/v1/storage/get/"+testCID, nil), fetchTestNS))
			return rec
		},
	} {
		body.wrote = false
		rec := serve()
		if rec.Code != http.StatusOK || rec.Body.String() != "file contents" {
			t.Fatalf("%s: status %d body %q", name, rec.Code, rec.Body.String())
		}
		if rec.Header().Get("Content-Length") != "13" {
			t.Errorf("%s: Content-Length = %q, want 13", name, rec.Header().Get("Content-Length"))
		}
		if !body.wrote {
			t.Errorf("%s: the object was not copied through", name)
		}
	}
}

// A client of the storage package that returns content without a size breaks
// the Content-Length promise; it is reported, not papered over by buffering.
func TestServeStored_contentOfUnknownLengthIs500(t *testing.T) {
	mock := &mockIPFSClient{getReader: io.NopCloser(unsizedReader{}), heldLocally: true}
	h, gate := fetchHandlers(t, mock, &countingDB{ownershipDB: ownershipDB{owned: true}})
	caps := mintTokens(t, gate, testCID, "dev-1", 1)
	rec := serveRelayed(h, relayedGET(testCID, caps[0].Token))
	if rec.Code != http.StatusInternalServerError {
		t.Errorf("status %d, want 500", rec.Code)
	}
}

type unsizedReader struct{}

func (unsizedReader) Read([]byte) (int, error) { return 0, io.EOF }
