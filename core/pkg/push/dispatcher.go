package push

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"sync"

	"go.uber.org/zap"
)

// PushDispatcher routes push messages to the matching provider for each
// of a user's registered devices.
type PushDispatcher struct {
	mu        sync.RWMutex
	providers map[string]PushProvider
	devices   PushDeviceStore
	logger    *zap.Logger
}

// New creates a dispatcher with the given device store. Register
// providers before sending.
func New(devices PushDeviceStore, logger *zap.Logger) *PushDispatcher {
	if logger == nil {
		logger = zap.NewNop()
	}
	return &PushDispatcher{
		providers: map[string]PushProvider{},
		devices:   devices,
		logger:    logger.Named("push"),
	}
}

// Register makes a provider available to dispatch. Calling Register with
// the same name twice replaces the previous provider — useful in tests.
func (d *PushDispatcher) Register(p PushProvider) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.providers[p.Name()] = p
}

// Provider returns the registered provider by name, or nil.
func (d *PushDispatcher) Provider(name string) PushProvider {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.providers[name]
}

// SendToUser fans out the message to every registered device for the
// user. Each provider failure is logged but does not stop subsequent
// devices. Returns the first encountered error (if any) so callers can
// surface a partial-failure signal.
//
// SendToUser returns nil if the user has no registered devices — that
// is normal, not an error.
//
// Callers wanting per-device outcomes should use SendToUserDetailed
// (bugboard #348 — back-compat preserved on this method).
func (d *PushDispatcher) SendToUser(
	ctx context.Context,
	namespace, userID string,
	msg PushMessage,
) error {
	res, err := d.SendToUserDetailed(ctx, namespace, userID, msg)
	if err != nil {
		return err
	}
	// Preserve the legacy contract: return the first per-device error
	// with the full error chain intact (sentinels like ErrUnknownProvider
	// and ErrDeviceUnregistered are reachable via errors.Is on the result).
	for _, r := range res.Results {
		if !r.Success && r.err != nil {
			return r.err
		}
	}
	return nil
}

// SendToUserDetailed dispatches to every registered device for the user
// and returns a per-device outcome. Unlike SendToUser (which collapses
// to a single error), this surfaces every device's HTTP status / reason
// so the caller can react granularly (delete on Unregistered, retry on
// 5xx, log unknowns, etc.).
//
// Used by the `oh.PushSendV2` WASM host function so WASM callers can
// auto-clean stale tokens and surface real failures (bugboard #348).
//
// Returns (nil, err) only on setup failures (device-store query failed,
// etc.). A user with zero devices returns
// (&SendDetailedResult{Ok: true, DevicesAttempted: 0}, nil).
func (d *PushDispatcher) SendToUserDetailed(
	ctx context.Context,
	namespace, userID string,
	msg PushMessage,
) (*SendDetailedResult, error) {
	devs, err := d.devices.ListForUser(ctx, namespace, userID)
	if err != nil {
		return nil, fmt.Errorf("list devices: %w", err)
	}
	// Bugboard #408 — target_provider filter. When the caller sets
	// msg.TargetProvider, drop every device whose Provider doesn't match
	// BEFORE we attempt sends or count anything. This lets a chat-alert
	// path send only to "apns" devices while a call-push path sends only
	// to "apns_voip" devices, even though both are registered on the
	// same iPhone. Unset = fanout (back-compat for every existing
	// caller, including unmigrated functions in other namespaces).
	//
	// Bugboard feat-10 — exclude_provider filter. The inverse: drop
	// devices whose Provider EQUALS msg.ExcludeProvider. Useful for the
	// "fan out to everyone EXCEPT VoIP" pattern (chat handler that wants
	// ntfy+apns+expo but never apns_voip — cleaner than listing every
	// included provider). If both are set, TargetProvider wins —
	// combining them is ambiguous (e.g. target=apns + exclude=apns is
	// empty by construction), so we pick the safer positive filter and
	// ignore the exclusion. Unset = no exclusion.
	if msg.TargetProvider != "" {
		filtered := devs[:0]
		for _, dev := range devs {
			if dev.Provider == msg.TargetProvider {
				filtered = append(filtered, dev)
			}
		}
		devs = filtered
	} else if msg.ExcludeProvider != "" {
		filtered := devs[:0]
		for _, dev := range devs {
			if dev.Provider != msg.ExcludeProvider {
				filtered = append(filtered, dev)
			}
		}
		devs = filtered
	}
	out := &SendDetailedResult{
		Ok:               true, // flipped to false on the first failure
		DevicesAttempted: len(devs),
		Results:          make([]DeviceSendResult, 0, len(devs)),
	}
	if len(devs) == 0 {
		return out, nil
	}

	for _, dev := range devs {
		r := DeviceSendResult{DeviceID: dev.DeviceID, Provider: dev.Provider}
		d.mu.RLock()
		p, ok := d.providers[dev.Provider]
		d.mu.RUnlock()
		if !ok {
			r.Success = false
			r.Message = fmt.Sprintf("push: unknown provider %q (device not dispatched)", dev.Provider)
			// Preserve the sentinel error chain so legacy callers using
			// errors.Is(err, ErrUnknownProvider) on the SendToUser
			// return value keep working.
			r.err = fmt.Errorf("%w: %s", ErrUnknownProvider, dev.Provider)
			d.logger.Warn("push: dropping device with unregistered provider",
				zap.String("provider", dev.Provider),
				zap.String("device_id", dev.DeviceID),
			)
			out.Ok = false
			out.Results = append(out.Results, r)
			continue
		}
		m := msg
		m.DeviceToken = dev.Token
		if sendErr := p.Send(ctx, m); sendErr != nil {
			r.Success = false
			r.err = sendErr // preserve full chain for errors.Is/As
			// Extract structured info if the provider returned PushError.
			var perr *PushError
			if errors.As(sendErr, &perr) {
				r.HTTPStatus = perr.HTTPStatus
				r.Reason = perr.Reason
				r.Message = perr.Message
				r.Unregistered = perr.Unregistered
			} else {
				r.Message = sendErr.Error()
			}
			d.logger.Warn("push: provider send failed",
				zap.String("provider", dev.Provider),
				zap.String("device_id", dev.DeviceID),
				zap.Int("http_status", r.HTTPStatus),
				zap.String("reason", r.Reason),
				zap.Bool("unregistered", r.Unregistered),
				zap.Error(sendErr),
			)
			out.Ok = false
		} else {
			r.Success = true
			// Record the success status explicitly. A provider Send returns nil
			// only on a 2xx delivery, so surface 200 instead of leaving
			// HTTPStatus at its zero value — otherwise a successful push logs
			// "http=0", which reads like an opaque failure and masks real
			// false-success classes (bugboard #132).
			r.HTTPStatus = http.StatusOK
			out.DevicesSucceeded++
		}
		out.Results = append(out.Results, r)
	}
	return out, nil
}
