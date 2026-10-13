package orama

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"net/http"
	"time"

	"github.com/caddyserver/caddy/v2"
	"github.com/caddyserver/caddy/v2/caddyconfig/caddyfile"
	"github.com/caddyserver/certmagic"
	"go.uber.org/zap"
)

const (
	// lockLease is how long a lock is held without a renewal. The holder
	// renews it every lockLease/3, so a live holder never loses it and a dead
	// one releases it within lockLease.
	lockLease = 60 * time.Second
	// maxLease mirrors core/pkg/tlsstore.MaxLease: the store refuses longer.
	maxLease = 2 * time.Hour
	// lockPoll is how often a waiting Lock asks again.
	lockPoll = 2 * time.Second
	// callTimeout bounds one call to the gateway.
	callTimeout = 30 * time.Second
	// renewTimeout bounds one lease renewal, well under lockLease/3, so a hung
	// renewal leaves time for the next before the lease runs out.
	renewTimeout = 10 * time.Second
	// maxResponse bounds what a call reads back: a list of keys or one value.
	maxResponse = 4 << 20
	// storeWait is how long Provision waits for the store to open: the cluster
	// gateway starts with Caddy, and imports this node's old certificates
	// before it answers.
	storeWait = 2 * time.Minute
	// storePoll is how often it asks.
	storePoll = 2 * time.Second
)

// errLockNotHeld is the store's answer to a renewal or release of a lock the
// caller no longer holds.
var errLockNotHeld = errors.New("TLS store lock not held")

// errRefused is the gateway refusing the call outright (404): a wrong key, or
// not a cluster gateway. Waiting does not change it.
var errRefused = errors.New("refused")

func init() {
	caddy.RegisterModule(Storage{})
}

// Storage is caddy.storage.orama: CertMagic storage kept in the cluster's
// shared store through the local index gateway. Every node's Caddy uses the
// same store, so CertMagic treats them as one cluster: one node obtains or
// renews a certificate under the store's lock and the others load it.
type Storage struct {
	// Endpoint is the index gateway's /v1/internal/tls-store. Required.
	Endpoint string `json:"endpoint,omitempty"`
	// KeyFile holds the store's master key, hex. Required.
	KeyFile string `json:"key_file,omitempty"`

	macKey, sealKey []byte
	client          *http.Client
	logger          *zap.Logger

	locks *lockTable
}

// CaddyModule returns the Caddy module information.
func (Storage) CaddyModule() caddy.ModuleInfo {
	return caddy.ModuleInfo{ID: "caddy.storage.orama", New: func() caddy.Module { return new(Storage) }}
}

// Provision reads the key, checks the configuration and waits for the store to
// open.
//
// Caddy starts managing its certificates right after provisioning, and a name
// whose certificate could not be read then is left unmanaged — logged, never
// retried — so the node would serve no TLS for it until Caddy restarted. So the
// store must answer first: while the gateway is not up, or is still importing,
// this waits; if it does not open within storeWait, provisioning fails, Caddy
// exits, and systemd starts it again.
func (s *Storage) Provision(ctx caddy.Context) error {
	s.logger = ctx.Logger()
	if err := s.setup(); err != nil {
		return err
	}
	return s.waitForStore(ctx, storeWait, storePoll)
}

// waitForStore returns once the store answers a call, or an error when it
// refuses this node's key or does not open within wait.
func (s *Storage) waitForStore(ctx context.Context, wait, poll time.Duration) error {
	deadline := time.Now().Add(wait)
	for {
		resp, err := s.call(ctx, storeRequest{Op: "stat", Key: "certificates"})
		if err == nil && resp.Exists == nil {
			err = fmt.Errorf("orama storage: %s answered without saying whether the key exists: not the cluster's store", s.Endpoint)
		}
		switch {
		case err == nil:
			return nil
		case errors.Is(err, errRefused):
			return err
		case !time.Now().Before(deadline):
			return fmt.Errorf("orama storage: the cluster's certificate store did not open within %s: %w", wait, err)
		}
		s.logger.Info("waiting for the cluster's certificate store", zap.Error(err))
		select {
		case <-ctx.Done():
			return fmt.Errorf("orama storage: %w", ctx.Err())
		case <-time.After(poll):
		}
	}
}

func (s *Storage) setup() error {
	if s.Endpoint == "" {
		return fmt.Errorf("orama storage: endpoint is required; install writes the gateway's /v1/internal/tls-store into the Caddyfile")
	}
	if s.KeyFile == "" {
		return fmt.Errorf("orama storage: key_file is required; the gateway refuses unsigned calls")
	}
	master, err := readHexKey(s.KeyFile, "orama storage")
	if err != nil {
		return err
	}
	if s.macKey, s.sealKey, err = storeKeys(master); err != nil {
		return fmt.Errorf("orama storage: %w", err)
	}
	s.client = &http.Client{Timeout: callTimeout}
	s.locks = &lockTable{held: map[string]*heldLock{}}
	if s.logger == nil {
		s.logger = zap.NewNop()
	}
	return nil
}

// CertMagicStorage returns the storage CertMagic uses.
func (s *Storage) CertMagicStorage() (certmagic.Storage, error) { return s, nil }

// UnmarshalCaddyfile parses `storage orama { endpoint …; key_file … }`.
func (s *Storage) UnmarshalCaddyfile(d *caddyfile.Dispenser) error {
	for d.Next() {
		for d.NextBlock(0) {
			var target *string
			switch d.Val() {
			case "endpoint":
				target = &s.Endpoint
			case "key_file":
				target = &s.KeyFile
			default:
				return d.Errf("unrecognized option: %s", d.Val())
			}
			if !d.NextArg() {
				return d.ArgErr()
			}
			*target = d.Val()
		}
	}
	return nil
}

// storeRequest and storeResponse are core/pkg/gateway's tlsStoreRequest and
// tlsStoreResponse.
type storeRequest struct {
	Op        string `json:"op"`
	Key       string `json:"key,omitempty"`
	Value     string `json:"value,omitempty"`
	Recursive bool   `json:"recursive,omitempty"`
	Holder    string `json:"holder,omitempty"`
	LeaseMS   int64  `json:"lease_ms,omitempty"`
}

type storeResponse struct {
	Exists     *bool    `json:"exists"`
	Value      string   `json:"value"`
	Keys       []string `json:"keys"`
	Size       int64    `json:"size"`
	ModifiedMS int64    `json:"modified_ms"`
	Terminal   bool     `json:"terminal"`
	Acquired   bool     `json:"acquired"`
}

// call runs one operation. Any answer but 200 is an error — never "absent":
// a refused or failed call read as "no certificate" would have this node
// obtain one it already has.
func (s *Storage) call(ctx context.Context, req storeRequest) (storeResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return storeResponse{}, fmt.Errorf("orama storage %s: %w", req.Op, err)
	}
	hreq, err := http.NewRequestWithContext(ctx, http.MethodPost, s.Endpoint, bytes.NewReader(body))
	if err != nil {
		return storeResponse{}, fmt.Errorf("orama storage %s: %w", req.Op, err)
	}
	hreq.Header.Set("Content-Type", "application/json")
	if err := signV2(s.macKey, hreq, body, storeAudience, time.Now()); err != nil {
		return storeResponse{}, fmt.Errorf("orama storage %s: %w", req.Op, err)
	}
	resp, err := s.client.Do(hreq)
	if err != nil {
		return storeResponse{}, fmt.Errorf("orama storage %s %s: gateway unreachable: %w", req.Op, req.Key, err)
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(io.LimitReader(resp.Body, maxResponse))
	if err != nil {
		return storeResponse{}, fmt.Errorf("orama storage %s %s: read answer: %w", req.Op, req.Key, err)
	}
	switch resp.StatusCode {
	case http.StatusOK:
	case http.StatusConflict:
		return storeResponse{}, fmt.Errorf("orama storage %s %s: %w", req.Op, req.Key, errLockNotHeld)
	case http.StatusNotFound:
		return storeResponse{}, fmt.Errorf("orama storage %s %s: %w (404): the gateway does not accept "+
			"this node's TLS store key, or is not a cluster gateway", req.Op, req.Key, errRefused)
	default:
		return storeResponse{}, fmt.Errorf("orama storage %s %s: gateway answered %d: %s",
			req.Op, req.Key, resp.StatusCode, clip(bytes.TrimSpace(raw)))
	}
	var out storeResponse
	if err := json.Unmarshal(raw, &out); err != nil {
		return storeResponse{}, fmt.Errorf("orama storage %s %s: undecodable answer: %w", req.Op, req.Key, err)
	}
	return out, nil
}

// Store seals value and stores it at key.
func (s *Storage) Store(ctx context.Context, key string, value []byte) error {
	sealed, err := sealValue(s.sealKey, key, value)
	if err != nil {
		return err
	}
	_, err = s.call(ctx, storeRequest{Op: "store", Key: key, Value: sealed})
	return err
}

// loadAttempts and loadRetryGap: a load is tried again after a transient
// failure. CertMagic does not retry the first load of a name it starts
// managing, and leaves a name whose load failed unmanaged until Caddy restarts.
const (
	loadAttempts = 3
	loadRetryGap = time.Second
)

// Load returns the value at key, or fs.ErrNotExist. A failure is retried a few
// times and then returned — never read as "not there".
func (s *Storage) Load(ctx context.Context, key string) ([]byte, error) {
	var (
		resp storeResponse
		err  error
	)
	for attempt := 1; ; attempt++ {
		resp, err = s.call(ctx, storeRequest{Op: "load", Key: key})
		if err == nil || errors.Is(err, errRefused) || attempt == loadAttempts {
			break
		}
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("orama storage load %s: %w", key, ctx.Err())
		case <-time.After(loadRetryGap):
		}
	}
	if err != nil {
		return nil, err
	}
	if resp.Exists == nil {
		return nil, fmt.Errorf("orama storage load %s: answer says neither present nor absent", key)
	}
	if !*resp.Exists {
		return nil, fs.ErrNotExist
	}
	return openValue(s.sealKey, key, resp.Value)
}

// Delete removes key and everything under it.
func (s *Storage) Delete(ctx context.Context, key string) error {
	_, err := s.call(ctx, storeRequest{Op: "delete", Key: key})
	return err
}

// Exists reports whether key is stored, as a value or a directory. The
// interface has no error to return: a call that fails twice answers false and
// is logged.
func (s *Storage) Exists(ctx context.Context, key string) bool {
	_, err := s.Stat(ctx, key)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		// One more try: an answer of "false" after a lock is taken is what
		// would have CertMagic order a certificate the store already holds.
		_, err = s.Stat(ctx, key)
	}
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		s.logger.Error("could not ask the TLS store whether a key exists", zap.String("key", key), zap.Error(err))
	}
	return err == nil
}

// Stat describes key, or returns fs.ErrNotExist.
func (s *Storage) Stat(ctx context.Context, key string) (certmagic.KeyInfo, error) {
	resp, err := s.call(ctx, storeRequest{Op: "stat", Key: key})
	if err != nil {
		return certmagic.KeyInfo{}, err
	}
	if resp.Exists == nil {
		return certmagic.KeyInfo{}, fmt.Errorf("orama storage stat %s: answer says neither present nor absent", key)
	}
	if !*resp.Exists {
		return certmagic.KeyInfo{}, fs.ErrNotExist
	}
	return certmagic.KeyInfo{Key: key, Size: resp.Size, Modified: time.UnixMilli(resp.ModifiedMS), IsTerminal: resp.Terminal}, nil
}

// List returns the keys under path; fs.ErrNotExist when there are none.
func (s *Storage) List(ctx context.Context, path string, recursive bool) ([]string, error) {
	resp, err := s.call(ctx, storeRequest{Op: "list", Key: path, Recursive: recursive})
	if err != nil {
		return nil, err
	}
	if len(resp.Keys) == 0 {
		return nil, fs.ErrNotExist
	}
	return resp.Keys, nil
}

// Interface guards
var (
	_ caddy.Module               = (*Storage)(nil)
	_ caddy.Provisioner          = (*Storage)(nil)
	_ caddy.StorageConverter     = (*Storage)(nil)
	_ caddyfile.Unmarshaler      = (*Storage)(nil)
	_ certmagic.Storage          = (*Storage)(nil)
	_ certmagic.LockLeaseRenewer = (*Storage)(nil)
)

// maxErrorBody bounds how much of an unexpected answer an error quotes.
const maxErrorBody = 256

func clip(b []byte) []byte {
	if len(b) > maxErrorBody {
		return b[:maxErrorBody]
	}
	return b
}
