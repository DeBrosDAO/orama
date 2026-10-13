package provider

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

const testCID = "bafkreihdwdcefgh4dqkjv67uzcmw7ojee6xedzdetojuzjevtenxquvyku"

// fakeKuboServer answers the RPC calls the provider makes and records them.
type fakeKuboServer struct {
	token    string
	calls    []string
	addBody  []byte
	catBody  []byte
	failPath string
	failMsg  string
	redirect string
}

func (f *fakeKuboServer) handler() http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("Authorization") != "Bearer "+f.token {
			http.Error(w, "unauthorized", http.StatusUnauthorized)
			return
		}
		f.calls = append(f.calls, r.URL.Path+"?"+r.URL.RawQuery)
		if f.redirect != "" {
			http.Redirect(w, r, f.redirect, http.StatusTemporaryRedirect)
			return
		}
		if f.failPath == r.URL.Path {
			http.Error(w, f.failMsg, http.StatusInternalServerError)
			return
		}
		switch r.URL.Path {
		case "/api/v0/add":
			require := func(err error) {
				if err != nil {
					http.Error(w, err.Error(), http.StatusBadRequest)
				}
			}
			file, _, err := r.FormFile("file")
			require(err)
			if err == nil {
				f.addBody, err = io.ReadAll(file)
				require(err)
			}
			_, _ = w.Write([]byte(`{"Hash":"` + testCID + `"}`))
		case "/api/v0/cat":
			_, _ = w.Write(f.catBody)
		default:
			_, _ = w.Write([]byte(`{"Pins":["x"]}`))
		}
	})
}

func newFakeKubo(t *testing.T) (*Kubo, *fakeKuboServer, *httptest.Server) {
	t.Helper()
	f := &fakeKuboServer{token: "tok"}
	srv := httptest.NewServer(f.handler())
	t.Cleanup(srv.Close)
	k, err := NewKubo(srv.URL, "tok\n")
	require.NoError(t, err)
	return k, f, srv
}

func TestNewKubo_refusesWhatCouldLeakTheToken(t *testing.T) {
	for name, url := range map[string]string{
		"https":       "https://127.0.0.1:31011",
		"remote host": "http://203.0.113.5:31011",
		"hostname":    "http://kubo.example.com:31011",
		"no port":     "http://127.0.0.1",
		"a path":      "http://127.0.0.1:31011/api",
		"empty":       "",
	} {
		_, err := NewKubo(url, "tok")
		require.Errorf(t, err, name)
	}
	_, err := NewKubo("http://127.0.0.1:31011", "  ")
	require.Error(t, err, "an empty token")
	_, err = NewKubo("http://[::1]:31011", "tok")
	require.NoError(t, err, "IPv6 loopback")
	_, err = NewKubo("http://198.18.0.2:31011", "tok")
	require.NoError(t, err, "the co-located namespace address")
	_, err = NewKubo("http://198.18.0.3:31011", "tok")
	require.Error(t, err, "another address in the namespace's range")
}

func TestKubo_addPinsUnderACIDv1AndSendsTheBearer(t *testing.T) {
	k, f, _ := newFakeKubo(t)
	cid, err := k.Add(context.Background(), []byte("piece bytes"))
	require.NoError(t, err)
	require.Equal(t, testCID, cid)
	require.Equal(t, []byte("piece bytes"), f.addBody)
	require.Equal(t, []string{"/api/v0/add?" + kuboAddQuery}, f.calls)
	require.Contains(t, kuboAddQuery, "cid-version=1")
	require.Contains(t, kuboAddQuery, "raw-leaves=true")
	require.Contains(t, kuboAddQuery, "pin=true")
}

func TestKubo_addRefusesAResponseThatIsNotACID(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"Hash":"../etc"}`))
	}))
	defer srv.Close()
	k, err := NewKubo(srv.URL, "tok")
	require.NoError(t, err)
	_, err = k.Add(context.Background(), []byte("x"))
	require.Error(t, err)
}

func TestKubo_catIsBoundedAndNeverTruncates(t *testing.T) {
	k, f, _ := newFakeKubo(t)
	f.catBody = []byte("0123456789")
	got, err := k.Cat(context.Background(), testCID, 10)
	require.NoError(t, err)
	require.Equal(t, f.catBody, got)
	_, err = k.Cat(context.Background(), testCID, 9)
	require.ErrorContains(t, err, "larger than")
	_, err = k.Cat(context.Background(), "../x", 10)
	require.Error(t, err)
	got, err = k.Cat(context.Background(), testCID, 100)
	require.NoError(t, err)
	require.Len(t, got, 10)
}

func TestKubo_unpinIgnoresANotPinnedAnswerOnly(t *testing.T) {
	k, f, _ := newFakeKubo(t)
	require.NoError(t, k.Pin(context.Background(), testCID))
	require.NoError(t, k.Unpin(context.Background(), testCID))
	require.Equal(t, []string{"/api/v0/pin/add?arg=" + testCID, "/api/v0/pin/rm?arg=" + testCID}, f.calls)

	f.failPath, f.failMsg = "/api/v0/pin/rm", "not pinned or pinned indirectly"
	require.NoError(t, k.Unpin(context.Background(), testCID))
	f.failMsg = "datastore is locked"
	require.ErrorContains(t, k.Unpin(context.Background(), testCID), "datastore is locked")
	require.Error(t, k.Pin(context.Background(), "bad cid"))
}

func TestKubo_wrongTokenAndRedirectsAreErrors(t *testing.T) {
	_, f, srv := newFakeKubo(t)
	wrong, err := NewKubo(srv.URL, "other")
	require.NoError(t, err)
	_, err = wrong.Add(context.Background(), []byte("x"))
	require.ErrorContains(t, err, "401")

	f.redirect = "http://203.0.113.9/steal"
	k, err := NewKubo(srv.URL, "tok")
	require.NoError(t, err)
	require.Error(t, k.Pin(context.Background(), testCID), "a redirect must not carry the token elsewhere")
}

func TestKubo_unreachableNamesTheUnit(t *testing.T) {
	srv := httptest.NewServer(http.NotFoundHandler())
	url := srv.URL
	srv.Close()
	k, err := NewKubo(url, "tok")
	require.NoError(t, err)
	err = k.Pin(context.Background(), testCID)
	require.Error(t, err)
	require.True(t, strings.Contains(err.Error(), "orama-global-ipfs.service"))
}

func TestValidIPFSCID(t *testing.T) {
	require.True(t, ValidIPFSCID(testCID))
	require.True(t, ValidIPFSCID("QmYwAPJzv5CZsnA625s3Xf2nemtYgPpHdWEz79ojWnPbdG"))
	for _, bad := range []string{"", "Qm", "/ipfs/" + testCID, testCID + "/x", "bAFK", "b" + strings.Repeat("!", 55)} {
		require.Falsef(t, ValidIPFSCID(bad), "%q", bad)
	}
}
