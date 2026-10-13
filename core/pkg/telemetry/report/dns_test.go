package report

import (
	"context"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/miekg/dns"
)

func TestDomainFromCorefile_zoneBlock(t *testing.T) {
	cases := map[string]string{
		"stagenet.dbrsteting.bid {\n  file db\n}":    "stagenet.dbrsteting.bid",
		"*.orama-devnet.network:53 {\n}":             "orama-devnet.network",
		". {\n  forward . 8.8.8.8\n}\nexample.com {": "example.com",
		". {\n  forward . 8.8.8.8\n}":                "",
		"":                                           "",
	}
	for in, want := range cases {
		if got := domainFromCorefile(in); got != want {
			t.Errorf("domainFromCorefile(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestCountErrorLines_matchesErrorsOnly(t *testing.T) {
	out := "[INFO] plugin/reload\n[ERROR] plugin/errors: 2 x.\nall good\nsome error here\n"
	if n := countErrorLines(out); n != 2 {
		t.Fatalf("countErrorLines = %d, want 2", n)
	}
	if n := countErrorLines(""); n != 0 {
		t.Fatalf("empty journal = %d errors", n)
	}
}

// startDNS answers A, NS and SOA for zone and NXDOMAIN for anything else.
func startDNS(t *testing.T, zone string) string {
	t.Helper()
	pc, err := net.ListenPacket("udp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	mux := dns.NewServeMux()
	mux.HandleFunc(dns.Fqdn(zone), func(w dns.ResponseWriter, req *dns.Msg) {
		m := new(dns.Msg)
		m.SetReply(req)
		q := req.Question[0]
		switch q.Qtype {
		case dns.TypeA:
			rr, _ := dns.NewRR(q.Name + " 60 IN A 10.0.0.1")
			m.Answer = append(m.Answer, rr)
		case dns.TypeNS:
			for _, ns := range []string{"ns1", "ns2"} {
				rr, _ := dns.NewRR(q.Name + " 60 IN NS " + ns + "." + zone + ".")
				m.Answer = append(m.Answer, rr)
			}
		case dns.TypeSOA:
			rr, _ := dns.NewRR(q.Name + " 60 IN SOA ns1." + zone + ". admin." + zone + ". 1 7200 3600 1209600 60")
			m.Answer = append(m.Answer, rr)
		}
		_ = w.WriteMsg(m)
	})
	srv := &dns.Server{PacketConn: pc, Handler: mux}
	go func() { _ = srv.ActivateAndServe() }()
	t.Cleanup(func() { _ = srv.Shutdown() })
	return pc.LocalAddr().String()
}

func TestProbeZone_readsLocalNameserver(t *testing.T) {
	old := localDNSAddr
	localDNSAddr = startDNS(t, "example.test")
	t.Cleanup(func() { localDNSAddr = old })

	r := &DNSReport{}
	probeZone(context.Background(), r, "example.test")
	if !r.SOAResolves || !r.NSResolves || r.NSRecordCount != 2 || !r.BaseAResolves || !r.WildcardResolves {
		t.Fatalf("report = %+v, want every check to resolve", r)
	}
}

func TestProbeZone_nameserverDown(t *testing.T) {
	old := localDNSAddr
	localDNSAddr = "127.0.0.1:1"
	t.Cleanup(func() { localDNSAddr = old })

	r := &DNSReport{}
	probeZone(context.Background(), r, "example.test")
	if r.SOAResolves || r.NSResolves || r.BaseAResolves || r.WildcardResolves {
		t.Fatalf("report = %+v, want nothing to resolve", r)
	}
}

func TestTLSDaysLeft_readsServedCertificate(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}))
	defer srv.Close()
	old := localTLSAddr
	localTLSAddr = strings.TrimPrefix(srv.URL, "https://")
	t.Cleanup(func() { localTLSAddr = old })

	days, expired := tlsDaysLeft(context.Background(), "example.test")
	if days < 1 || expired {
		t.Fatalf("days left = %d, want the test certificate's remaining lifetime", days)
	}
}

func TestTLSDaysLeft_nothingListening(t *testing.T) {
	old := localTLSAddr
	localTLSAddr = "127.0.0.1:1"
	t.Cleanup(func() { localTLSAddr = old })
	if days, expired := tlsDaysLeft(context.Background(), "example.test"); days != -1 || expired {
		t.Fatalf("days = %d, want -1", days)
	}
}
