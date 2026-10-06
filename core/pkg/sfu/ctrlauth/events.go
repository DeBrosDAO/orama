package ctrlauth

import "time"

const (
	// EventsPath is the route on a namespace gateway where an SFU reports
	// membership. The body is a MembershipEvent and the request carries a MAC.
	EventsPath = "/v1/internal/webrtc/events"

	// EventJoin and EventLeave are the two membership events.
	EventJoin  = "join"
	EventLeave = "leave"

	// LeftReason, KickedReason, ExpiredReason and ClosedReason say why a peer
	// ended: it left (or its connection failed), the namespace kicked it, its
	// admission ran out, or the SFU shut the room down.
	LeftReason    = "left"
	KickedReason  = "kicked"
	ExpiredReason = "expired"
	ClosedReason  = "closed"

	// Control routes on the SFU, called by a namespace gateway with a MAC.
	KickPath = "/admin/kick"
	MutePath = "/admin/mute"
)

// MembershipEvent is one peer joining or leaving a room, as the SFU saw it.
type MembershipEvent struct {
	Type     string    `json:"type"`
	Room     string    `json:"room"`
	UserID   string    `json:"user_id"`
	DeviceID string    `json:"device_id,omitempty"`
	PeerID   string    `json:"peer_id"`
	Reason   string    `json:"reason,omitempty"` // leave only
	At       time.Time `json:"at"`
}

// KickRequest asks an SFU to remove a user from a room. AtMs is the gateway's
// clock when the admission was revoked: joins ticketed before it are refused.
// AdmitGen is the newest admission generation the kick revoked (0: none, or an
// older gateway): where it and a ticket's AdmitGen both exist, the two numbers
// decide and no clock does.
type KickRequest struct {
	Room     string `json:"room"`
	UserID   string `json:"user_id"`
	AtMs     int64  `json:"at_ms"`
	AdmitGen int64  `json:"admit_gen,omitempty"`
}

// MuteRequest asks an SFU to stop (or resume) forwarding a user's audio in a
// room. AtMs is the gateway's clock when the mute was recorded: a join ticketed
// before it, whose Muted is therefore a stale snapshot, takes this state.
type MuteRequest struct {
	Room   string `json:"room"`
	UserID string `json:"user_id"`
	Muted  bool   `json:"muted"`
	AtMs   int64  `json:"at_ms"`
}

// ControlResult is the SFU's answer to a control request.
type ControlResult struct {
	// Affected is how many peers of the user the room had.
	Affected int `json:"affected"`
}
