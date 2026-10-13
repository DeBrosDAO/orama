package repair

import (
	"bytes"
	"context"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"net/http"
)

const (
	rootHeader = "X-Piece-Root"
	// MaxPieceBytes bounds one fetched replica.
	MaxPieceBytes = 1 << 30
)

// ErrNotReady is a provider that has not read the slot assignment yet and
// refuses the root. The next step uploads again.
var ErrNotReady = errors.New("provider has not accepted this root yet")

// HTTP is the provider piece API over plain HTTP(S).
type HTTP struct{ Client *http.Client }

// Fetch reads /pieces/<hex root> from base.
func (h HTTP) Fetch(ctx context.Context, base string, root []byte) ([]byte, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, base+"/pieces/"+hex.EncodeToString(root), nil)
	if err != nil {
		return nil, err
	}
	resp, err := h.Client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("fetch from %s: %w", base, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("fetch from %s: HTTP %d", base, resp.StatusCode)
	}
	body, err := io.ReadAll(io.LimitReader(resp.Body, MaxPieceBytes+1))
	if err != nil {
		return nil, fmt.Errorf("read piece from %s: %w", base, err)
	}
	if len(body) > MaxPieceBytes {
		return nil, fmt.Errorf("piece from %s is larger than %d bytes", base, MaxPieceBytes)
	}
	return body, nil
}

// Upload posts body to /pieces/<hex root> on base.
func (h HTTP) Upload(ctx context.Context, base string, root, body []byte) error {
	name := hex.EncodeToString(root)
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, base+"/pieces/"+name, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set(rootHeader, name)
	resp, err := h.Client.Do(req)
	if err != nil {
		return fmt.Errorf("upload to %s: %w", base, err)
	}
	defer resp.Body.Close()
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
	switch resp.StatusCode {
	case http.StatusNoContent, http.StatusOK:
		return nil
	case http.StatusForbidden:
		return ErrNotReady
	default:
		return fmt.Errorf("upload to %s: HTTP %d: %s", base, resp.StatusCode, bytes.TrimSpace(msg))
	}
}
