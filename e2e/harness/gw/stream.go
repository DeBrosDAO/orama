package gw

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/DeBrosOfficial/network/e2e/harness/evidence"
)

// Server-sent events (text/event-stream, the WHATWG HTML "server-sent
// events" section).
const (
	eventStreamType = "text/event-stream"
	// maxEventLineBytes bounds one line of the stream.
	maxEventLineBytes = 1 << 20
)

// Event is one server-sent event.
type Event struct {
	ID    string
	Event string
	Data  string
	// Retry is the reconnection time the server asked for, in ms (0: none).
	Retry int
}

// StreamResp is an open streaming response. Read events with Next; Close it
// (always) to end the stream and record the exchange, with every event read,
// as evidence.
type StreamResp struct {
	Status int
	Header http.Header

	c       *Client
	req     *http.Request
	reqBody []byte
	body    io.ReadCloser
	br      *bufio.Reader
	start   time.Time
	events  []Event
	once    sync.Once
	err     error
}

// Stream sends r and returns the response once its headers arrive, with the
// body left open (no RequestBudget: a stream lasts as long as ctx). Accept
// defaults to text/event-stream. A non-2xx answer is still a *StreamResp;
// its body is read by Next like any other.
func (c *Client) Stream(ctx context.Context, r Req) (*StreamResp, error) {
	if c.pinErr != nil {
		return nil, c.pinErr
	}
	req, err := c.newRequest(ctx, r)
	if err != nil {
		return nil, err
	}
	if req.Header.Get("Accept") == "" {
		req.Header.Set("Accept", eventStreamType)
	}
	body := snapshotBody(req)
	if _, err := c.paceRequest(ctx, req, body); err != nil {
		return nil, err
	}
	s := &StreamResp{c: c, req: req, reqBody: body, start: time.Now()}
	resp, err := c.HTTP.Do(req)
	if err != nil {
		err = fmt.Errorf("%s %s: %w", req.Method, req.URL.Path, err)
		return nil, errors.Join(err, s.record(err))
	}
	s.Status, s.Header, s.body = resp.StatusCode, resp.Header, resp.Body
	s.br = bufio.NewReaderSize(resp.Body, readBufferBytes)
	return s, nil
}

// readBufferBytes is the stream reader's buffer.
const readBufferBytes = 64 << 10

// Next returns the next event. It returns io.EOF when the server ends the
// stream, and ctx's error when the request's context ends.
func (s *StreamResp) Next() (Event, error) {
	var ev Event
	var data []string
	for {
		line, err := s.readLine()
		if err != nil {
			return Event{}, err
		}
		if line == "" {
			if len(data) == 0 && ev.Event == "" {
				continue
			}
			ev.Data = strings.Join(data, "\n")
			s.events = append(s.events, ev)
			return ev, nil
		}
		field, value := splitField(line)
		switch field {
		case "data":
			data = append(data, value)
		case "event":
			ev.Event = value
		case "id":
			ev.ID = value
		case "retry":
			if n, err := strconv.Atoi(value); err == nil {
				ev.Retry = n
			}
		}
	}
}

// readLine reads one line without its CR/LF ending, bounded.
func (s *StreamResp) readLine() (string, error) {
	var b strings.Builder
	for {
		chunk, isPrefix, err := s.br.ReadLine()
		if err != nil {
			return "", err
		}
		b.Write(chunk)
		if b.Len() > maxEventLineBytes {
			return "", fmt.Errorf("a stream line of %s is over %d bytes", s.req.URL.Path, maxEventLineBytes)
		}
		if !isPrefix {
			return b.String(), nil
		}
	}
}

// splitField parses "field: value" (one optional space after the colon); a
// line starting with ':' is a comment, with an empty field.
func splitField(line string) (string, string) {
	field, value, found := strings.Cut(line, ":")
	if !found {
		return line, ""
	}
	return field, strings.TrimPrefix(value, " ")
}

// Events is every event Next has returned so far.
func (s *StreamResp) Events() []Event { return append([]Event(nil), s.events...) }

// Close ends the stream and records the exchange once; later calls return
// the same error.
func (s *StreamResp) Close() error {
	s.once.Do(func() {
		var closeErr error
		if s.body != nil {
			closeErr = s.body.Close()
		}
		s.err = errors.Join(closeErr, s.record(nil))
	})
	return s.err
}

// record writes the request, the status, the headers and the events read.
func (s *StreamResp) record(opErr error) error {
	var out strings.Builder
	out.WriteString(dumpHeaders(s.Header) + "\n")
	for _, ev := range s.events {
		fmt.Fprintf(&out, "event=%s id=%s data=%s\n", ev.Event, ev.ID, ev.Data)
	}
	rec := evidence.Record{
		Kind: evidence.KindHTTP, Test: s.c.test, Summary: "STREAM " + s.req.Method + " " + s.req.URL.String() + s.c.pinNote(),
		Status: s.Status, DurationMS: time.Since(s.start).Milliseconds(),
		Input: dumpHeaders(s.req.Header) + "\n" + string(s.reqBody), Output: out.String(),
	}
	if opErr != nil {
		rec.Error = opErr.Error()
	}
	if err := s.c.rec.Add(rec); err != nil {
		return fmt.Errorf("failed to record the stream %s: %w", s.req.URL.Path, err)
	}
	return nil
}
