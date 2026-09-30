package broker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/cloudflare"
	"github.com/DeBrosOfficial/network/e2e/harness/fleet"
)

// DNS is the run's Cloudflare zone as the broker uses it (*cloudflare.Client).
type DNS interface {
	RunIDOf(name string) (string, bool)
	SetTXT(ctx context.Context, name, value string) error
	DeleteTXT(ctx context.Context, name, value string) ([]cloudflare.Record, error)
	RunRecords(ctx context.Context) ([]cloudflare.Record, error)
}

// Cloud creates and removes the run's servers and eval clusters with the
// runner's credentials (the provision package's direct implementation).
// Each call gets its own copy of the state to change.
type Cloud interface {
	AddExtra(ctx context.Context, st *fleet.State, name, location string) (fleet.Node, error)
	RemoveExtra(ctx context.Context, st *fleet.State, name string) error
	AddCluster(ctx context.Context, st *fleet.State, name string) (fleet.Cluster, error)
	RemoveCluster(ctx context.Context, st *fleet.State, name string) error
}

// Server serves one run's operations. State is the run's state as loaded;
// it is never written.
type Server struct {
	State *fleet.State
	DNS   DNS
	Cloud Cloud
	// Redact masks secrets in an error before it leaves the runner.
	Redact func(string) string
	// Logf receives one line per operation (no secret is ever passed).
	Logf func(format string, args ...any)
	// MaxServers caps the extras and eval clusters live or being created
	// at once (0: DefaultMaxServers).
	MaxServers int

	mu sync.Mutex
	// txt holds the TXT names set through the broker and not deleted.
	txtNames map[string]bool
	extras   map[string]fleet.Node
	clusters map[string]bool
	// pending holds names an add is creating, so two adds cannot race.
	pending map[string]bool
}

// Handle runs one request. It never panics on bad input: every refusal is
// an error in the response.
func (s *Server) Handle(ctx context.Context, req Request) Response {
	ctx, cancel := context.WithTimeout(ctx, budgetOf(req.Op))
	defer cancel()
	resp, err := s.dispatch(ctx, req)
	if err != nil {
		resp = Response{Error: s.redact(err.Error())}
	}
	s.logf("broker: %s %q: %s", req.Op, req.Name, outcome(resp))
	return resp
}

func (s *Server) dispatch(ctx context.Context, req Request) (Response, error) {
	switch req.Op {
	case OpTXTSet, OpTXTDelete:
		return Response{}, s.txt(ctx, req)
	case OpRecordsList:
		recs, err := s.records(ctx, req.Name)
		return Response{Records: recs}, err
	case OpExtraAdd:
		n, err := s.addExtra(ctx, req.Name, req.Location)
		return Response{Node: &n}, err
	case OpExtraRemove:
		return Response{}, s.removeExtra(ctx, req.Name)
	case OpClusterAdd:
		c, err := s.addCluster(ctx, req.Name)
		return Response{Cluster: &c}, err
	case OpClusterRemove:
		return Response{}, s.removeCluster(ctx, req.Name)
	}
	return Response{}, fmt.Errorf("unknown operation %q", req.Op)
}

// ownName normalises name and refuses one outside this run.
func (s *Server) ownName(name string) (string, error) {
	n := strings.TrimSuffix(strings.ToLower(strings.TrimSpace(name)), ".")
	if id, ok := s.DNS.RunIDOf(n); !ok || id != s.State.RunID {
		return "", fmt.Errorf("refusing %q: only names inside e2e-%s[-<label>].<zone>, this run's, are served", name, s.State.RunID)
	}
	return n, nil
}

func (s *Server) txt(ctx context.Context, req Request) error {
	name, err := s.ownName(req.Name)
	if err != nil {
		return err
	}
	if req.Op == OpTXTSet {
		if err := s.countTXT(name, true); err != nil {
			return err
		}
		return s.DNS.SetTXT(ctx, name, req.Value)
	}
	if _, err = s.DNS.DeleteTXT(ctx, name, req.Value); err != nil {
		return err
	}
	return s.countTXT(name, false)
}

// records lists the run's records named under or below it (all when empty).
func (s *Server) records(ctx context.Context, under string) ([]Record, error) {
	if under != "" {
		var err error
		if under, err = s.ownName(under); err != nil {
			return nil, err
		}
	}
	all, err := s.DNS.RunRecords(ctx)
	if err != nil {
		return nil, err
	}
	out := []Record{}
	for _, r := range all {
		name := strings.TrimSuffix(strings.ToLower(r.Name), ".")
		if id, ok := s.DNS.RunIDOf(name); !ok || id != s.State.RunID {
			continue
		}
		if under == "" || name == under || strings.HasSuffix(name, "."+under) {
			out = append(out, Record{Type: r.Type, Name: name, Content: r.Content})
		}
	}
	return out, nil
}

func (s *Server) redact(msg string) string {
	if s.Redact == nil {
		return msg
	}
	return s.Redact(msg)
}

func (s *Server) logf(format string, args ...any) {
	if s.Logf != nil {
		s.Logf(format, args...)
	}
}

func outcome(r Response) string {
	if r.Error != "" {
		return "refused or failed: " + r.Error
	}
	return "ok"
}

// serveConn answers one connection: one request, one response. The
// operation is cancelled when the client hangs up first.
func (s *Server) serveConn(ctx context.Context, conn net.Conn) error {
	defer conn.Close()
	var req Request
	if err := conn.SetReadDeadline(time.Now().Add(requestReadTimeout)); err != nil {
		return fmt.Errorf("broker: failed to bound the request read: %w", err)
	}
	if err := readLine(conn, &req); err != nil {
		return writeResponse(conn, Response{Error: "bad request: " + err.Error()})
	}
	if err := conn.SetReadDeadline(time.Time{}); err != nil {
		return fmt.Errorf("broker: failed to clear the request read deadline: %w", err)
	}
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	readDone := make(chan error, 1)
	go func() {
		// The client sends nothing more: a read returns only when it hangs up.
		_, err := conn.Read(make([]byte, 1))
		readDone <- err
		cancel()
	}()
	werr := writeResponse(conn, s.Handle(ctx, req))
	if werr == nil {
		return nil
	}
	// A failed write is the client's hang-up when the read has ended too;
	// the deadline tells a live client (the read times out) from a gone one.
	if err := conn.SetReadDeadline(time.Now()); err != nil {
		return errors.Join(werr, err)
	}
	var ne net.Error
	if rerr := <-readDone; errors.As(rerr, &ne) && ne.Timeout() {
		return werr
	}
	return nil
}

func writeResponse(conn net.Conn, resp Response) error {
	line, err := json.Marshal(resp)
	if err != nil {
		return fmt.Errorf("failed to encode a broker response: %w", err)
	}
	if _, err := conn.Write(append(line, '\n')); err != nil && !errors.Is(err, net.ErrClosed) {
		return fmt.Errorf("failed to send a broker response: %w", err)
	}
	return nil
}
