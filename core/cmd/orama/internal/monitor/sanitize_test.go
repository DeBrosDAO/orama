package monitor

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/clierr"

	"github.com/DeBrosOfficial/network/pkg/telemetry/cluster"
	"github.com/DeBrosOfficial/network/pkg/telemetry/report"
)

const hostileText = "ok\x1b]0;pwned\x07\x1b[2J\nnext"

func TestCleanText_replacesControlChars(t *testing.T) {
	if got := CleanText(hostileText); strings.ContainsAny(got, "\x1b\x07\n") || !strings.Contains(got, "pwned") {
		t.Fatalf("got %q", got)
	}
	if got := CleanText("plain · ✓ text"); got != "plain · ✓ text" {
		t.Fatalf("clean text was changed: %q", got)
	}
	if CleanText("") != "" {
		t.Fatal("empty string changed")
	}
}

func TestCleanText_dropsFormatChars(t *testing.T) {
	for name, tc := range map[string]struct{ in, want string }{
		"bidi override":     {"node \u202egnp.exe\u202c ok", "node gnp.exe ok"},
		"bidi isolate":      {"a\u2066b\u2067c\u2068d\u2069e", "abcde"},
		"zero-width space":  {"10.0.0\u200b.1", "10.0.0.1"},
		"zero-width joiner": {"a\u200cb\u200dc\u200ed\u200fe", "abcde"},
		"byte-order mark":   {"\ufeffhost", "host"},
		"with controls":     {"x\u202e\x1b[2Jy", "x [2Jy"},
		"only format chars": {"\u200b\u202e", ""},
	} {
		if got := CleanText(tc.in); got != tc.want {
			t.Errorf("%s: CleanText(%q) = %q, want %q", name, tc.in, got, tc.want)
		}
	}
}

func TestSanitizeSnapshot_reachesEveryString(t *testing.T) {
	snap := &cluster.ClusterSnapshot{
		Nodes: []cluster.CollectionStatus{
			{Node: cluster.NodeRef{Host: hostileText}, Err: hostileText},
			{Node: cluster.NodeRef{Host: "1.1.1.1"}, Report: &report.NodeReport{
				Hostname: hostileText,
				Errors:   []string{hostileText},
				RQLite: &report.RQLiteReport{Nodes: map[string]report.RQLiteNodeInfo{
					hostileText: {Error: hostileText},
				}},
				Services: &report.ServicesReport{FailedUnits: []string{hostileText}},
			}},
			{Node: cluster.NodeRef{Host: "2.2.2.2"}},
		},
		Alerts: []cluster.Alert{{Message: hostileText, Node: hostileText}},
	}
	sanitizeSnapshot(snap)

	r := snap.Nodes[1].Report
	var all []string
	all = append(all, snap.Nodes[0].Node.Host, snap.Nodes[0].Err, r.Hostname, r.Errors[0],
		r.Services.FailedUnits[0], snap.Alerts[0].Message, snap.Alerts[0].Node)
	for k, v := range r.RQLite.Nodes {
		all = append(all, k, v.Error)
	}
	if len(r.RQLite.Nodes) != 1 {
		t.Fatalf("map entries = %d, want 1", len(r.RQLite.Nodes))
	}
	for _, s := range all {
		if strings.ContainsAny(s, "\x1b\x07\n") {
			t.Errorf("control characters survived: %q", s)
		}
	}
	if snap.Nodes[2].Report != nil {
		t.Fatal("a nil report was filled in")
	}
}

// Errors from a source are cleaned too, and keep their exit code.
func TestScopedSource_cleansErrorsAndKeepsTheirCode(t *testing.T) {
	inner := &failingSource{err: clierr.Unavailable("gateway said %s", hostileText)}
	s := &scopedSource{inner: inner, env: "devnet"}
	_, err := s.Snapshot(context.Background())
	if strings.ContainsAny(err.Error(), "\x1b\x07\n") || clierr.CodeOf(err) != clierr.CodeUnavailable {
		t.Fatalf("got code %d: %q", clierr.CodeOf(err), err.Error())
	}
	for u := range s.Watch(context.Background(), time.Second) {
		if u.Err == nil || strings.ContainsAny(u.Err.Error(), "\x1b\x07\n") || clierr.CodeOf(u.Err) != clierr.CodeUnavailable {
			t.Fatalf("update error: %v", u.Err)
		}
	}
	if cleanErr(nil) != nil {
		t.Fatal("cleanErr(nil) is not nil")
	}
}

// failingSource fails every read with err.
type failingSource struct{ err error }

func (f *failingSource) Mode() Mode { return ModeAPI }
func (f *failingSource) Snapshot(context.Context) (*cluster.ClusterSnapshot, error) {
	return nil, f.err
}
func (f *failingSource) Watch(ctx context.Context, _ time.Duration) <-chan Update {
	out := make(chan Update, 1)
	out <- Update{State: LinkFailed, Err: f.err}
	close(out)
	return out
}
