package netregistry

import (
	"context"
	"crypto/tls"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

// networkServer serves a network under /nets/teststage/. files maps a file name
// to its body; a file left out answers 404.
func networkServer(t *testing.T, files map[string][]byte) (*httptest.Server, *http.Client) {
	t.Helper()
	mux := http.NewServeMux()
	mux.HandleFunc("/nets/teststage/", func(w http.ResponseWriter, r *http.Request) {
		body, ok := files[strings.TrimPrefix(r.URL.Path, "/nets/teststage/")]
		if !ok {
			http.NotFound(w, r)
			return
		}
		_, _ = w.Write(body)
	})
	srv := httptest.NewTLSServer(mux)
	t.Cleanup(srv.Close)
	client := NewHTTPClient()
	client.Transport = srv.Client().Transport
	return srv, client
}

func publishedFiles(t *testing.T) map[string][]byte {
	t.Helper()
	return map[string][]byte{
		ManifestFile:    marshalManifest(t, validManifest()),
		ReleaseRootFile: testRoot,
		GenesisFile:     testGenesis,
	}
}

func TestFetchNetwork_fetchesAndVerifies(t *testing.T) {
	srv, client := networkServer(t, publishedFiles(t))
	n, err := FetchNetwork(context.Background(), client, srv.URL+"/nets/teststage/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	if n.Manifest.ChainID != "orama-teststage-1" || n.Builtin {
		t.Errorf("fetched %+v", n)
	}
	genesis, err := n.FetchGenesis(context.Background(), client)
	if err != nil || string(genesis) != string(testGenesis) {
		t.Fatalf("FetchGenesis = %q, %v", genesis, err)
	}
}

func TestFetchNetwork_refusals(t *testing.T) {
	good := publishedFiles(t)
	with := func(file string, body []byte) map[string][]byte {
		files := map[string][]byte{}
		for k, v := range good {
			files[k] = v
		}
		if body == nil {
			delete(files, file)
		} else {
			files[file] = body
		}
		return files
	}
	for name, files := range map[string]map[string][]byte{
		"manifest missing":    with(ManifestFile, nil),
		"manifest invalid":    with(ManifestFile, []byte(`{"name":"teststage"}`)),
		"root missing":        with(ReleaseRootFile, nil),
		"root is not pinned":  with(ReleaseRootFile, []byte(`{"tampered":1}`)),
		"manifest oversized":  with(ManifestFile, []byte(strings.Repeat(" ", maxManifestBytes+1))),
		"manifest not a json": with(ManifestFile, []byte(`<html>`)),
	} {
		t.Run(name, func(t *testing.T) {
			srv, client := networkServer(t, files)
			if _, err := FetchNetwork(context.Background(), client, srv.URL+"/nets/teststage/manifest.json"); err == nil {
				t.Fatal("FetchNetwork accepted it")
			}
		})
	}
}

func TestFetchNetwork_urlRules(t *testing.T) {
	srv, client := networkServer(t, publishedFiles(t))
	for name, url := range map[string]string{
		"plain http":        strings.Replace(srv.URL, "https://", "http://", 1) + "/nets/teststage/manifest.json",
		"not a manifest":    srv.URL + "/nets/teststage/network.json",
		"no file":           srv.URL + "/nets/teststage/",
		"unsupported":       "file:///etc/passwd",
		"no scheme at all":  "orama.network/networks/x/manifest.json",
		"control character": srv.URL + "/nets/\x7f/manifest.json",
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := FetchNetwork(context.Background(), client, url); err == nil {
				t.Fatal("FetchNetwork accepted the URL")
			}
		})
	}
}

func TestFetchGenesis_unpublishedIsAClearError(t *testing.T) {
	files := publishedFiles(t)
	delete(files, GenesisFile)
	srv, client := networkServer(t, files)
	n, err := FetchNetwork(context.Background(), client, srv.URL+"/nets/teststage/manifest.json")
	if err != nil {
		t.Fatalf("a manifest without a genesis must still fetch: %v", err)
	}
	_, err = n.FetchGenesis(context.Background(), client)
	if !errors.Is(err, ErrGenesisUnpublished) {
		t.Fatalf("FetchGenesis = %v, want ErrGenesisUnpublished", err)
	}
	for _, want := range []string{"teststage", "orama-teststage-1", "genesis.json"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("error %q does not say %q", err, want)
		}
	}
}

func TestFetchGenesis_wrongGenesisAndOversized(t *testing.T) {
	files := publishedFiles(t)
	files[GenesisFile] = []byte(`{"chain_id":"orama-teststage-1","extra":true}`)
	srv, client := networkServer(t, files)
	n, err := FetchNetwork(context.Background(), client, srv.URL+"/nets/teststage/manifest.json")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := n.FetchGenesis(context.Background(), client); !errors.Is(err, ErrGenesisMismatch) {
		t.Fatalf("a swapped genesis gave %v, want ErrGenesisMismatch", err)
	}
}

func TestGet_refusesARedirectToHTTP(t *testing.T) {
	plain := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("{}"))
	}))
	defer plain.Close()
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, plain.URL+"/manifest.json", http.StatusFound)
	}))
	defer srv.Close()
	client := NewHTTPClient()
	client.Transport = &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}}

	_, err := FetchNetwork(context.Background(), client, srv.URL+"/x/manifest.json")
	if err == nil || !strings.Contains(err.Error(), "only https is followed") {
		t.Fatalf("a redirect to http gave %v", err)
	}
}

func TestGet_stopsRedirectLoops(t *testing.T) {
	var srv *httptest.Server
	srv = httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, srv.URL+r.URL.Path, http.StatusFound)
	}))
	defer srv.Close()
	client := NewHTTPClient()
	client.Transport = srv.Client().Transport

	if _, err := FetchNetwork(context.Background(), client, srv.URL+"/x/manifest.json"); err == nil {
		t.Fatal("a redirect loop was followed forever")
	}
}
