package webrtc

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"time"

	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/sfu/roomid"
	"github.com/gorilla/websocket"
	"go.uber.org/zap"
)

// A client that does not put ?room= on the URL names its room only in the first
// frame it sends. The gateway then terminates the upgrade itself, reads that
// frame, places the room, dials the owning SFU and replays the frame, and pipes
// frames both ways from there.
const (
	// joinFrameTimeout bounds the wait for the first frame. The SFU gives a
	// direct client 10 s; a socket that has not said which room it wants within
	// this is holding a connection for nothing.
	joinFrameTimeout = 5 * time.Second

	// joinFrameMaxBytes caps the first frame: a join is a room id and a user id.
	joinFrameMaxBytes = 4096

	// pipeFrameMaxBytes caps one frame after the join (SDP offers are the largest).
	pipeFrameMaxBytes = 1 << 20

	// sfuDialTimeout bounds the handshake with the owning SFU.
	sfuDialTimeout = 10 * time.Second

	joinMessageType = "join"
)

var signalUpgrader = websocket.Upgrader{
	ReadBufferSize:  4096,
	WriteBufferSize: 4096,
	CheckOrigin:     func(*http.Request) bool { return true }, // authenticated before this handler
}

// joinFrame is the client's first frame: {"type":"join","data":{"roomId","userId"}}
// (core/pkg/sfu/signaling.go).
type joinFrame struct {
	Type string `json:"type"`
	Data struct {
		RoomID string `json:"roomId"`
		UserID string `json:"userId"`
	} `json:"data"`
}

// parseJoinFrame checks the first frame is a join for a valid room and returns its room id.
func parseJoinFrame(raw []byte) (string, error) {
	var f joinFrame
	if err := json.Unmarshal(raw, &f); err != nil {
		return "", errors.New("the first frame must be JSON")
	}
	if f.Type != joinMessageType {
		return "", errors.New("the first frame must be a join")
	}
	if f.Data.UserID == "" {
		return "", errors.New("roomId and userId are required")
	}
	if err := roomid.Validate(f.Data.RoomID); err != nil {
		return "", err
	}
	return f.Data.RoomID, nil
}

// signalByJoinFrame serves a socket that named no room in the URL.
func (h *WebRTCHandlers) signalByJoinFrame(w http.ResponseWriter, r *http.Request, ns string) {
	client, err := signalUpgrader.Upgrade(w, r, nil)
	if err != nil {
		h.logger.ComponentWarn(logging.ComponentGeneral, "WebRTC signal upgrade failed",
			zap.String("namespace", ns), zap.Error(err))
		return
	}
	defer client.Close()

	first, room, err := readJoinFrame(client, h.joinTimeout)
	if err != nil {
		refuse(client, "invalid_join", err.Error())
		return
	}

	owner, err := h.ownerOf(r.Context(), ns, room)
	if err != nil {
		h.logger.ComponentWarn(logging.ComponentGeneral, "No SFU available for room",
			zap.String("namespace", ns), zap.String("room", room), zap.Error(err))
		refuse(client, "no_sfu", ownerErrorMessage(err))
		return
	}

	backend, err := dialSFU(r, owner, room)
	if err != nil {
		h.logger.ComponentWarn(logging.ComponentGeneral, "SFU dial failed",
			zap.String("namespace", ns), zap.String("target", owner.Addr()), zap.Error(err))
		refuse(client, "sfu_unreachable", "the SFU for this room did not accept the connection; retry")
		return
	}
	defer backend.Close()

	if err := backend.WriteMessage(websocket.TextMessage, first); err != nil {
		refuse(client, "sfu_unreachable", "the SFU for this room dropped the connection; retry")
		return
	}
	pipeSockets(client, backend)
}

// readJoinFrame reads the first frame under a deadline and a size limit.
func readJoinFrame(c *websocket.Conn, timeout time.Duration) (frame []byte, room string, err error) {
	c.SetReadLimit(joinFrameMaxBytes)
	if err := c.SetReadDeadline(time.Now().Add(timeout)); err != nil {
		return nil, "", fmt.Errorf("failed to set the join deadline: %w", err)
	}
	typ, frame, err := c.ReadMessage()
	if err != nil {
		return nil, "", fmt.Errorf("no join frame arrived within %s, or it was over %d bytes", timeout, joinFrameMaxBytes)
	}
	if typ != websocket.TextMessage {
		return nil, "", errors.New("the first frame must be a text frame")
	}
	room, err = parseJoinFrame(frame)
	if err != nil {
		return nil, "", err
	}
	_ = c.SetReadDeadline(time.Time{})
	c.SetReadLimit(pipeFrameMaxBytes)
	return frame, room, nil
}

// dialSFU opens the signalling socket to the owning SFU, naming the room in the
// URL so the SFU's own room check applies.
func dialSFU(r *http.Request, owner SFUNode, room string) (*websocket.Conn, error) {
	target := "ws://" + owner.Addr() + "/ws/signal?" + url.Values{roomQueryParam: {room}}.Encode()
	d := websocket.Dialer{HandshakeTimeout: sfuDialTimeout, Proxy: nil}
	conn, _, err := d.DialContext(r.Context(), target, nil)
	if err != nil {
		return nil, fmt.Errorf("failed to dial SFU %s: %w", owner.Addr(), err)
	}
	conn.SetReadLimit(pipeFrameMaxBytes)
	return conn, nil
}

// refuse tells the client why in the SFU's own error-frame shape, then closes.
func refuse(c *websocket.Conn, code, message string) {
	frame, _ := json.Marshal(map[string]any{"type": "error", "data": map[string]string{"code": code, "message": message}})
	_ = c.WriteMessage(websocket.TextMessage, frame)
	_ = c.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(websocket.ClosePolicyViolation, code))
}

// pipeSockets copies frames both ways until either side ends, then closes both.
func pipeSockets(a, b *websocket.Conn) {
	done := make(chan struct{}, 2)
	go func() { copyFrames(a, b); done <- struct{}{} }()
	go func() { copyFrames(b, a); done <- struct{}{} }()
	<-done
	a.Close()
	b.Close()
	<-done
}

func copyFrames(dst, src *websocket.Conn) {
	for {
		typ, data, err := src.ReadMessage()
		if err != nil {
			var ce *websocket.CloseError
			if errors.As(err, &ce) {
				_ = dst.WriteMessage(websocket.CloseMessage, websocket.FormatCloseMessage(ce.Code, ce.Text))
			}
			return
		}
		if err := dst.WriteMessage(typ, data); err != nil {
			return
		}
	}
}
