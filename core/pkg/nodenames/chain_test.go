package nodenames

import (
	"context"
	"encoding/base64"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"google.golang.org/protobuf/encoding/protowire"

	"github.com/DeBrosOfficial/network/pkg/chainread"
)

// pageRequest decodes the QueryNodeNamesRequest a gateway query carries: the pagination key and
// limit.
func pageRequest(t *testing.T, r *http.Request) (key []byte, limit uint64) {
	t.Helper()
	data, err := base64.RawURLEncoding.DecodeString(r.URL.Query().Get("data"))
	if err != nil {
		t.Fatalf("data: %v", err)
	}
	pagination := firstBytes(t, data, 1)
	for len(pagination) > 0 {
		num, typ, n := protowire.ConsumeTag(pagination)
		pagination = pagination[n:]
		switch {
		case num == 1 && typ == protowire.BytesType:
			key, n = protowire.ConsumeBytes(pagination)
		case num == 3 && typ == protowire.VarintType:
			limit, n = protowire.ConsumeVarint(pagination)
		default:
			n = protowire.ConsumeFieldValue(num, typ, pagination)
		}
		pagination = pagination[n:]
	}
	return key, limit
}

func firstBytes(t *testing.T, msg []byte, want protowire.Number) []byte {
	t.Helper()
	for len(msg) > 0 {
		num, typ, n := protowire.ConsumeTag(msg)
		msg = msg[n:]
		if num == want && typ == protowire.BytesType {
			v, _ := protowire.ConsumeBytes(msg)
			return v
		}
		msg = msg[protowire.ConsumeFieldValue(num, typ, msg):]
	}
	return nil
}

func TestReaderChain_readsAPageThroughTheGatewayQueryRoute(t *testing.T) {
	var gotKey []byte
	var gotLimit uint64
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chain/query/orama.nodes.v1.Query/NodeNames" {
			http.NotFound(w, r)
			return
		}
		gotKey, gotLimit = pageRequest(t, r)
		_, _ = w.Write([]byte(`{"nodes":[{"name":"alice","node_id":"n1","operator":"orama1x","ips":["93.184.216.34"]},{"name":"bob","node_id":"n2","operator":"orama1y","ips":[]}],"pagination":{"next_key":"Ym9i","total":"0"}}`))
	}))
	defer srv.Close()

	page, err := ReaderChain{Reader: &chainread.Reader{Gateway: srv.URL}}.NodeNames(context.Background(), "YWxpY2U=")
	if err != nil {
		t.Fatal(err)
	}
	if string(gotKey) != "alice" || gotLimit != PageLimit {
		t.Errorf("request key %q limit %d, want key alice and limit %d", gotKey, gotLimit, PageLimit)
	}
	if len(page.Nodes) != 2 || page.Nodes[0].Name != "alice" || page.Nodes[0].IPs[0] != "93.184.216.34" || page.Nodes[1].IPs != nil && len(page.Nodes[1].IPs) != 0 {
		t.Errorf("nodes = %+v", page.Nodes)
	}
	if page.NextKey != "Ym9i" {
		t.Errorf("next key = %q", page.NextKey)
	}
}

func TestReaderChain_theLastPageHasNoKey(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte(`{"nodes":[],"pagination":{"next_key":null,"total":"0"}}`))
	}))
	defer srv.Close()
	page, err := ReaderChain{Reader: &chainread.Reader{Gateway: srv.URL}}.NodeNames(context.Background(), "")
	if err != nil || page.NextKey != "" || len(page.Nodes) != 0 {
		t.Fatalf("page %+v err %v", page, err)
	}
}

func TestReaderChain_errorsNameTheRead(t *testing.T) {
	down := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Error(w, "chain unreachable", http.StatusBadGateway)
	}))
	defer down.Close()
	_, err := ReaderChain{Reader: &chainread.Reader{Gateway: down.URL}}.NodeNames(context.Background(), "")
	if err == nil || !strings.Contains(err.Error(), "read the claimed node names from the chain") {
		t.Fatalf("err = %v", err)
	}
	if _, err := (ReaderChain{Reader: &chainread.Reader{}}).NodeNames(context.Background(), ""); err == nil {
		t.Fatal("a reader with no endpoint read names")
	}
	garbled := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { _, _ = w.Write([]byte(`[1,2]`)) }))
	defer garbled.Close()
	if _, err := (ReaderChain{Reader: &chainread.Reader{Gateway: garbled.URL}}).NodeNames(context.Background(), ""); err == nil {
		t.Fatal("an answer that is not a NodeNames response was accepted")
	}
}
