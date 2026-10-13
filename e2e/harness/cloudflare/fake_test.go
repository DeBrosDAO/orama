package cloudflare

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"strings"
	"sync"
	"testing"
)

const testZoneID = "zone123"

// fakeCF is an in-memory Cloudflare zone.
type fakeCF struct {
	mu      sync.Mutex
	records map[string]Record
	next    int
	// failDelete makes every DELETE answer 500.
	failDelete bool
	// keepOnDelete answers DELETE 200 without removing the record.
	keepOnDelete bool
	calls        []string
}

func newFakeCF(t *testing.T) (*fakeCF, *Client) {
	t.Helper()
	f := &fakeCF{records: map[string]Record{}}
	srv := httptest.NewServer(f)
	t.Cleanup(srv.Close)
	c, err := New("cf-token", AllowedZone, srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	return f, c
}

func (f *fakeCF) add(typ, name, content string) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.next++
	id := "r" + strconv.Itoa(f.next)
	f.records[id] = Record{ID: id, Type: typ, Name: name, Content: content}
}

func (f *fakeCF) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls = append(f.calls, r.Method+" "+r.URL.Path)
	if r.Header.Get("Authorization") != "Bearer cf-token" {
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"success":false,"errors":[{"code":10000,"message":"Authentication error"}]}`))
		return
	}
	if r.URL.Path == "/zones" {
		reply(w, []map[string]string{{"id": testZoneID, "name": r.URL.Query().Get("name")}}, 1)
		return
	}
	base := "/zones/" + testZoneID + "/dns_records"
	id := strings.TrimPrefix(strings.TrimPrefix(r.URL.Path, base), "/")
	switch {
	case r.Method == "GET":
		f.list(w, r)
	case r.Method == "POST":
		f.next++
		rec := decodeRecord(r)
		rec.ID = "r" + strconv.Itoa(f.next)
		f.records[rec.ID] = rec
		reply(w, rec, 1)
	case r.Method == "PUT":
		rec := decodeRecord(r)
		rec.ID = id
		f.records[id] = rec
		reply(w, rec, 1)
	case r.Method == "DELETE":
		f.remove(w, id)
	}
}

func (f *fakeCF) remove(w http.ResponseWriter, id string) {
	if f.failDelete {
		w.WriteHeader(http.StatusInternalServerError)
		_, _ = w.Write([]byte(`{"success":false,"errors":[{"code":1,"message":"boom"}]}`))
		return
	}
	if _, ok := f.records[id]; !ok {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte(`{"success":false,"errors":[{"code":81044,"message":"Record does not exist."}]}`))
		return
	}
	if !f.keepOnDelete {
		delete(f.records, id)
	}
	reply(w, map[string]string{"id": id}, 1)
}

func (f *fakeCF) list(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	var out []Record
	for _, rec := range f.records {
		if (q.Get("type") == "" || q.Get("type") == rec.Type) && (q.Get("name") == "" || q.Get("name") == rec.Name) {
			out = append(out, rec)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	per, _ := strconv.Atoi(q.Get("per_page"))
	page, _ := strconv.Atoi(q.Get("page"))
	pages := (len(out) + per - 1) / per
	start, end := (page-1)*per, page*per
	if start > len(out) {
		start = len(out)
	}
	if end > len(out) {
		end = len(out)
	}
	reply(w, out[start:end], pages)
}

func decodeRecord(r *http.Request) Record {
	raw, _ := io.ReadAll(r.Body)
	var rec Record
	_ = json.Unmarshal(raw, &rec)
	return rec
}

func reply(w http.ResponseWriter, result any, pages int) {
	raw, _ := json.Marshal(map[string]any{
		"success": true, "errors": []any{}, "result": result,
		"result_info": map[string]int{"total_pages": pages},
	})
	_, _ = w.Write(raw)
}
