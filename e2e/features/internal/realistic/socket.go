//go:build e2e_fleet

package realistic

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"net/url"
	"sync"

	"github.com/gorilla/websocket"

	"github.com/DeBrosOfficial/network/e2e/harness/gw"
)

// pathPubsubWS is the subscription socket (docs/API_SURFACE.md "Pub/sub").
const pathPubsubWS = "/v1/pubsub/ws"

// frameBuffer is how many frames a socket keeps that nobody has read yet.
const frameBuffer = 4096

// Frame is one pub/sub delivery: the envelope {data (base64), topic,
// timestamp} the socket carries (core/pkg/gateway/handlers/pubsub/ws_client.go).
type Frame struct {
	Topic string
	Data  []byte
}

// Socket is a held-open subscription, the way a client app keeps one for
// hours. Frames arrive on Frames until the socket closes; Err then says why.
type Socket struct {
	conn   *websocket.Conn
	Frames chan Frame
	mu     sync.Mutex
	err    error
	done   chan struct{}
}

// Subscribe opens a subscription to topic on c (pinned or not) as token,
// with extra query parameters (presence, member_id, ...).
func Subscribe(ctx context.Context, c *gw.Client, topic, token string, extra url.Values) (*Socket, error) {
	q := url.Values{}
	for k, v := range extra {
		q[k] = v
	}
	q.Set("topic", topic)
	conn, resp, err := c.DialWS(ctx, pathPubsubWS+"?"+q.Encode(), token, nil)
	if err != nil {
		status := 0
		if resp != nil {
			status = resp.StatusCode
		}
		return nil, fmt.Errorf("subscribing to %s (HTTP %d): %w", topic, status, err)
	}
	s := &Socket{conn: conn, Frames: make(chan Frame, frameBuffer), done: make(chan struct{})}
	go s.read()
	return s, nil
}

func (s *Socket) read() {
	defer close(s.done)
	defer close(s.Frames)
	for {
		_, raw, err := s.conn.ReadMessage()
		if err != nil {
			s.mu.Lock()
			s.err = err
			s.mu.Unlock()
			return
		}
		var env struct {
			Data  string `json:"data"`
			Topic string `json:"topic"`
		}
		if json.Unmarshal(raw, &env) != nil {
			continue
		}
		data, err := base64.StdEncoding.DecodeString(env.Data)
		if err != nil {
			continue
		}
		select {
		case s.Frames <- Frame{Topic: env.Topic, Data: data}:
		default:
			// A reader that stopped reading must not stall the socket; the
			// frame is dropped and a count mismatch shows it.
		}
	}
}

// Err is why the socket closed, nil while it is open.
func (s *Socket) Err() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.err
}

// Closed reports whether the socket has ended.
func (s *Socket) Closed() bool {
	select {
	case <-s.done:
		return true
	default:
		return false
	}
}

// Close closes the socket and waits for its reader.
func (s *Socket) Close() {
	_ = s.conn.Close()
	<-s.done
}

// Drain returns every frame received and not yet read, without waiting;
// callers poll it with eventually.Poll.
func (s *Socket) Drain() []Frame {
	var out []Frame
	for {
		select {
		case f, ok := <-s.Frames:
			if !ok {
				return out
			}
			out = append(out, f)
		default:
			return out
		}
	}
}
