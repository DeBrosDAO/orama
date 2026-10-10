package sfu

import (
	"encoding/json"
	"io"
	"net/http"
	"time"

	"github.com/DeBrosOfficial/network/pkg/sfu/ctrlauth"
	"go.uber.org/zap"
)

// maxControlBody bounds a control request: a room id and a user id.
const maxControlBody = 4096

// authenticControl reads and authenticates a control request, answering the
// refusal itself. It returns the body, or nil when it has answered.
func (s *Server) authenticControl(w http.ResponseWriter, r *http.Request, path string) []byte {
	if r.Method != http.MethodPost {
		http.Error(w, "method not allowed", http.StatusMethodNotAllowed)
		return nil
	}
	body, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxControlBody))
	if err != nil {
		http.Error(w, "request body unreadable or too large", http.StatusBadRequest)
		return nil
	}
	if err := s.verifyControl(r, path, body); err != nil {
		s.logger.Warn("Control request refused", zap.String("path", path), zap.String("remote", r.RemoteAddr), zap.Error(err))
		http.Error(w, err.Error(), http.StatusUnauthorized)
		return nil
	}
	return body
}

// verifyControl checks the request's MAC, then that it is not a copy of one
// already served.
func (s *Server) verifyControl(r *http.Request, path string, body []byte) error {
	now := time.Now()
	header := r.Header.Get(ctrlauth.MACHeader)
	if err := ctrlauth.Verify(s.controlKey, s.config.ListenAddr, header, r.Method, path, body, now); err != nil {
		return err
	}
	return s.replays.Use(header, now)
}

func writeControlResult(w http.ResponseWriter, affected int) {
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(ctrlauth.ControlResult{Affected: affected})
}

// handleKick serves POST /admin/kick: remove a user from a room and refuse any
// join ticketed before now. A room this SFU does not host affects nobody; the
// kick is still logged, because the user may be joining it right now.
func (s *Server) handleKick(w http.ResponseWriter, r *http.Request) {
	body := s.authenticControl(w, r, ctrlauth.KickPath)
	if body == nil {
		return
	}
	var req ctrlauth.KickRequest
	if err := json.Unmarshal(body, &req); err != nil || req.Room == "" || req.UserID == "" || req.AtMs == 0 {
		http.Error(w, `kick needs {"room","user_id","at_ms"}`, http.StatusBadRequest)
		return
	}
	s.kicks.record(req.Room, req.UserID, req.AtMs, req.AdmitGen)
	affected := 0
	if room := s.roomManager.GetRoom(req.Room); room != nil {
		affected = room.KickUser(req.UserID)
	}
	s.logger.Info("User kicked from room", zap.String("room", req.Room), zap.String("user_id", req.UserID), zap.Int("peers", affected))
	writeControlResult(w, affected)
}

// handleMute serves POST /admin/mute: stop or resume forwarding a user's audio.
// The state is logged, because the user may be joining the room right now.
func (s *Server) handleMute(w http.ResponseWriter, r *http.Request) {
	body := s.authenticControl(w, r, ctrlauth.MutePath)
	if body == nil {
		return
	}
	var req ctrlauth.MuteRequest
	if err := json.Unmarshal(body, &req); err != nil || req.Room == "" || req.UserID == "" || req.AtMs == 0 {
		http.Error(w, `mute needs {"room","user_id","muted","at_ms"}`, http.StatusBadRequest)
		return
	}
	affected := 0
	// Logged before the room is looked at: a join that finds the log without the
	// mute has its peer in the room before the mute looks for it.
	if !s.mutes.record(req.Room, req.UserID, req.Muted, req.AtMs) {
		s.logger.Info("Mute ignored: older than the one on record", zap.String("room", req.Room),
			zap.String("user_id", req.UserID), zap.Bool("muted", req.Muted))
		writeControlResult(w, affected)
		return
	}
	if room := s.roomManager.GetRoom(req.Room); room != nil {
		affected = room.MuteUser(req.UserID, req.Muted)
	}
	s.logger.Info("User mute changed", zap.String("room", req.Room), zap.String("user_id", req.UserID),
		zap.Bool("muted", req.Muted), zap.Int("peers", affected))
	writeControlResult(w, affected)
}
