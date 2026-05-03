package push

import (
	"context"
	"fmt"
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
func (d *PushDispatcher) SendToUser(
	ctx context.Context,
	namespace, userID string,
	msg PushMessage,
) error {
	devs, err := d.devices.ListForUser(ctx, namespace, userID)
	if err != nil {
		return fmt.Errorf("list devices: %w", err)
	}
	if len(devs) == 0 {
		return nil
	}

	var firstErr error
	for _, dev := range devs {
		d.mu.RLock()
		p, ok := d.providers[dev.Provider]
		d.mu.RUnlock()
		if !ok {
			d.logger.Warn("push: dropping device with unregistered provider",
				zap.String("provider", dev.Provider),
				zap.String("device_id", dev.DeviceID),
			)
			if firstErr == nil {
				firstErr = fmt.Errorf("%w: %s", ErrUnknownProvider, dev.Provider)
			}
			continue
		}
		m := msg
		m.DeviceToken = dev.Token
		if err := p.Send(ctx, m); err != nil {
			d.logger.Warn("push: provider send failed",
				zap.String("provider", dev.Provider),
				zap.String("device_id", dev.DeviceID),
				zap.Error(err),
			)
			if firstErr == nil {
				firstErr = err
			}
		}
	}
	return firstErr
}
