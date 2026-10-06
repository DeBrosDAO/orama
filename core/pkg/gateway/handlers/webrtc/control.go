package webrtc

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/DeBrosOfficial/network/pkg/sfu/ctrlauth"
	"github.com/DeBrosOfficial/network/pkg/sfu/roomid"
)

// What the namespace's functions do to its rooms: admit a user, kick one, mute
// one (the webrtc_admit, webrtc_kick and webrtc_mute host functions). The
// admission and the mute are recorded in the namespace's database, which is
// what the next join is judged by; the kick and the mute also go to the SFU that
// owns the room, because the peer is connected there now.

const (
	// MaxAdmissionTTL is the longest an admission may be issued for. An admission
	// is a grant; one that outlives a day is a grant nobody is watching.
	MaxAdmissionTTL = 24 * time.Hour

	// maxIdentityLen bounds a user or device id a function names.
	maxIdentityLen = 256

	// controlTimeout bounds one control call to an SFU.
	controlTimeout = 5 * time.Second

	// maxControlResponse bounds what is read back from an SFU.
	maxControlResponse = 4096
)

// Admit records that user may join room for ttl, from device (any device when
// "") and returns when the admission ends.
func (h *WebRTCHandlers) Admit(ctx context.Context, ns, room, user, device string, ttl time.Duration) (time.Time, error) {
	if err := h.checkControl(ns, room, user); err != nil {
		return time.Time{}, err
	}
	if len(device) > maxIdentityLen {
		return time.Time{}, fmt.Errorf("device id is %d bytes, the limit is %d", len(device), maxIdentityLen)
	}
	if ttl <= 0 || ttl > MaxAdmissionTTL {
		return time.Time{}, fmt.Errorf("admission ttl must be between 1 second and %s, got %s", MaxAdmissionTTL, ttl)
	}
	return h.admissions.Admit(ctx, ns, room, user, device, ttl)
}

// Kick revokes user's admissions to room and removes their connection from the
// namespace's SFUs. The revocation comes first and is what keeps them out: a
// failure to reach an SFU leaves them revoked, and says so.
func (h *WebRTCHandlers) Kick(ctx context.Context, ns, room, user string) error {
	if err := h.checkControl(ns, room, user); err != nil {
		return err
	}
	if err := h.admissions.Revoke(ctx, ns, room, user); err != nil {
		return err
	}
	req := ctrlauth.KickRequest{Room: room, UserID: user, AtMs: h.now().UnixMilli()}
	if err := h.callAll(ctx, ns, ctrlauth.KickPath, req); err != nil {
		return fmt.Errorf("admissions of %q to room %q are revoked, but their connection could not be closed: %w", user, room, err)
	}
	return nil
}

// Mute stops (or resumes) the forwarding of user's audio in room, on the
// namespace's SFUs, and records it so it holds when they rejoin.
func (h *WebRTCHandlers) Mute(ctx context.Context, ns, room, user string, muted bool) error {
	if err := h.checkControl(ns, room, user); err != nil {
		return err
	}
	if err := h.admissions.SetMuted(ctx, ns, room, user, muted); err != nil {
		return err
	}
	req := ctrlauth.MuteRequest{Room: room, UserID: user, Muted: muted, AtMs: h.now().UnixMilli()}
	if err := h.callAll(ctx, ns, ctrlauth.MutePath, req); err != nil {
		return fmt.Errorf("the mute of %q in room %q is recorded, but the SFU could not be told: %w", user, room, err)
	}
	return nil
}

func (h *WebRTCHandlers) checkControl(ns, room, user string) error {
	if h.namespace == "" || ns != h.namespace {
		return fmt.Errorf("this gateway serves namespace %q and cannot act on namespace %q", h.namespace, ns)
	}
	if err := roomid.Validate(room); err != nil {
		return fmt.Errorf("invalid room: %w", err)
	}
	if user == "" || len(user) > maxIdentityLen {
		return fmt.Errorf("user must be 1 to %d bytes", maxIdentityLen)
	}
	if h.admissions == nil {
		return errors.New("this gateway has no WebRTC admission store")
	}
	if h.controlKey == nil {
		return errors.New("this gateway has no TURN secret to authenticate to the SFU with: is WebRTC enabled for the namespace?")
	}
	return nil
}

// callAll sends a signed control request to every SFU of the namespace. The
// room's owner is not asked alone: placement is decided by health probes, and an
// SFU that missed one while it hosted the room would be passed over, so the call
// would land on an SFU that has no such peer and the kick would do nothing, in
// silence. Every SFU keeps its own kick log, so every SFU is told. The error
// names each SFU that could not be reached or refused.
func (h *WebRTCHandlers) callAll(ctx context.Context, ns, path string, req any) error {
	if h.sfuDirectory == nil {
		return errors.New("SFU directory is not configured")
	}
	nodes, err := h.sfuDirectory.SFUNodes(ctx, ns)
	if err != nil {
		return fmt.Errorf("failed to list SFU nodes for namespace %q: %w", ns, err)
	}
	if len(nodes) == 0 {
		return ErrNoSFUNodes
	}
	body, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("failed to encode the %s request: %w", path, err)
	}
	errs := make([]error, len(nodes))
	var wg sync.WaitGroup
	for i, n := range nodes {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs[i] = h.callSFU(ctx, n, path, body)
		}()
	}
	wg.Wait()
	return errors.Join(errs...)
}

// callSFU sends one signed control request to one SFU.
func (h *WebRTCHandlers) callSFU(ctx context.Context, node SFUNode, path string, body []byte) error {
	ctx, cancel := context.WithTimeout(ctx, controlTimeout)
	defer cancel()
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, "http://"+node.Addr()+path, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("failed to build the %s request: %w", path, err)
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set(ctrlauth.MACHeader, ctrlauth.Sign(h.controlKey, node.Addr(), http.MethodPost, path, body, h.now()))
	resp, err := h.controlClient.Do(httpReq)
	if err != nil {
		return fmt.Errorf("failed to reach SFU %s on %s: %w", node.NodeID, node.Addr(), err)
	}
	defer resp.Body.Close()
	detail, _ := io.ReadAll(io.LimitReader(resp.Body, maxControlResponse))
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("SFU %s answered %d to %s: %s", node.NodeID, resp.StatusCode, path, strings.TrimSpace(string(detail)))
	}
	return nil
}
