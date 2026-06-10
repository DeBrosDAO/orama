// Package push provides HTTP handlers for managing push-notification
// device registrations and sending pushes.
//
// Endpoints:
//
//	GET    /v1/push/devices         — list caller's registered devices (tokens omitted)
//	POST   /v1/push/devices         — register / update a device
//	DELETE /v1/push/devices/{id}    — unregister a device
//	POST   /v1/push/send            — send a push to a user (admin/internal scope)
//
// Device tokens are stored AES-256-GCM-encrypted in RQLite via the
// pkg/push.RqliteDeviceStore. Tokens are NEVER returned by any endpoint —
// the GET endpoint omits the token field for safety.
package push

import (
	"context"
	"encoding/json"
	"net/http"

	"github.com/DeBrosOfficial/network/pkg/gateway/auth"
	"github.com/DeBrosOfficial/network/pkg/gateway/ctxkeys"
	"github.com/DeBrosOfficial/network/pkg/logging"
	"github.com/DeBrosOfficial/network/pkg/push"
	"github.com/DeBrosOfficial/network/pkg/push/credentials"
)

// Handlers serves the /v1/push/* HTTP endpoints. Construct via NewHandlers;
// it's safe for concurrent use.
//
// dispatcher is the legacy single-tier dispatcher (kept for the device
// register/list/delete + send paths). manager is the per-namespace
// dispatcher built on top of ConfigStore (new in bug #220 follow-up);
// when both are present, send paths route through manager so per-namespace
// config wins.
//
// configStore + manager may be nil on gateways with push fully disabled —
// the corresponding endpoints return 503.
type Handlers struct {
	dispatcher         *push.PushDispatcher
	manager            *push.Manager
	store              push.PushDeviceStore
	configStore        push.ConfigStore
	credentialsManager *credentials.Manager // optional — feature #72 (set via SetCredentialsManager)
	logger             *logging.ColoredLogger
}

// NewHandlers constructs a Handlers with the legacy single-namespace
// dispatcher only. Use NewHandlersWithManager for per-namespace config
// support (bug #220 follow-up).
func NewHandlers(dispatcher *push.PushDispatcher, store push.PushDeviceStore, logger *logging.ColoredLogger) *Handlers {
	return &Handlers{
		dispatcher: dispatcher,
		store:      store,
		logger:     logger,
	}
}

// NewHandlersWithManager constructs Handlers wired to a Manager + ConfigStore
// for tenant-self-service per-namespace configuration. Send paths use the
// manager when present so per-namespace ntfy/expo settings take effect.
func NewHandlersWithManager(
	manager *push.Manager,
	configStore push.ConfigStore,
	deviceStore push.PushDeviceStore,
	logger *logging.ColoredLogger,
) *Handlers {
	return &Handlers{
		manager:     manager,
		configStore: configStore,
		store:       deviceStore,
		logger:      logger,
	}
}

// RegisterDeviceRequest is the body of POST /v1/push/devices.
//
// `device_id` is an app-supplied stable identifier (e.g. the OS-assigned
// device UUID). Combined with (namespace, user_id) it uniquely identifies
// the registration; re-posting with the same device_id updates the token.
//
// `token` is provider-specific:
//   - ntfy: the topic path the device subscribes to (e.g. "ns/myapp/user-1")
//   - expo: an ExponentPushToken[...]
//   - apns: a hex APNs device token (future)
type RegisterDeviceRequest struct {
	DeviceID   string `json:"device_id"`
	Provider   string `json:"provider"` // "ntfy" | "expo" | "apns"
	Token      string `json:"token"`
	Platform   string `json:"platform,omitempty"`    // "ios" | "android" | "web"
	AppVersion string `json:"app_version,omitempty"`
}

// RegisterDeviceResponse is the body of POST /v1/push/devices.
type RegisterDeviceResponse struct {
	Status string `json:"status"`
}

// PushDeviceView is the safe (token-omitting) representation returned
// by GET /v1/push/devices.
type PushDeviceView struct {
	ID         string `json:"id"`
	DeviceID   string `json:"device_id"`
	Provider   string `json:"provider"`
	Platform   string `json:"platform,omitempty"`
	AppVersion string `json:"app_version,omitempty"`
	CreatedAt  int64  `json:"created_at"`
	UpdatedAt  int64  `json:"updated_at"`
	LastSeen   int64  `json:"last_seen,omitempty"`
}

// SendRequest is the body of POST /v1/push/send.
//
// The dispatcher fans out to all of `user_id`'s registered devices in
// the caller's namespace. Auth scope: see SendHandler — currently
// requires the caller to act on behalf of their own namespace; finer
// per-user authorization is the app's responsibility.
type SendRequest struct {
	UserID   string                 `json:"user_id"`
	Title    string                 `json:"title"`
	Body     string                 `json:"body"`
	Channel  string                 `json:"channel,omitempty"`
	Priority string                 `json:"priority,omitempty"` // "high" | "normal" | "" (default)
	Badge    int                    `json:"badge,omitempty"`
	Sound    string                 `json:"sound,omitempty"`
	Data     map[string]interface{} `json:"data,omitempty"`
}

// SendResponse is the body of POST /v1/push/send.
type SendResponse struct {
	Status string `json:"status"`
}

// resolveNamespace pulls the namespace set by auth middleware out of context.
func resolveNamespace(r *http.Request) string {
	if v := r.Context().Value(ctxkeys.NamespaceOverride); v != nil {
		if s, ok := v.(string); ok {
			return s
		}
	}
	return ""
}

// accountIDClaim is the custom JWT claim an app may set to carry the stable
// account identity (e.g. anchat's users.user_id) that a device should be
// keyed on, independent of which wallet credential authenticated the
// session. Injected at mint time by the namespace's claims-provider hook.
// See bugboard #548 (name agreed in comment #906/#920).
const accountIDClaim = "account_id"

// resolveCallerUserID extracts the identity a push device should be keyed on.
//
// In a multi-credential app (anchat), the JWT subject is the *wallet* — a
// credential, not the identity. A single user (rootId) with N linked wallets
// would otherwise register N device rows and receive N duplicate pushes
// (bugboard #548). When the app includes a stable `account_id` custom claim, we
// key on that; otherwise we fall back to the subject (wallet) so single-
// credential apps and older tokens keep working unchanged.
//
// Returns empty if the request was authenticated by API key only (no JWT).
func resolveCallerUserID(r *http.Request) string {
	if v := r.Context().Value(ctxkeys.JWT); v != nil {
		if claims, ok := v.(*auth.JWTClaims); ok && claims != nil {
			if rootID, ok := claims.Custom[accountIDClaim]; ok && rootID != "" {
				return rootID
			}
			return claims.Sub
		}
	}
	return ""
}

func writeError(w http.ResponseWriter, code int, message string) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(map[string]string{"error": message})
}

func writeJSON(w http.ResponseWriter, code int, v interface{}) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(code)
	_ = json.NewEncoder(w).Encode(v)
}

// pickPriority maps the wire-format priority string to the typed enum.
func pickPriority(s string) push.PushPriority {
	switch s {
	case "high":
		return push.PriorityHigh
	case "normal":
		return push.PriorityNormal
	default:
		return push.PriorityNormal
	}
}

// boundCtx returns a request-scoped context with no extra wrapping;
// kept as a seam for future scope (rate-limit context etc.).
func boundCtx(r *http.Request) context.Context { return r.Context() }
