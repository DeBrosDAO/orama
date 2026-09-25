package wssession

import "errors"

var (
	// ErrNotRefreshable is a refresh on a socket that was not opened with a
	// token — an API key, or no credential at all. Such a socket has no token
	// to extend, and taking one on would change who it belongs to.
	ErrNotRefreshable = errors.New("this socket was not opened with a token, so there is none to refresh; reconnect with one")

	// ErrNoClaims is a refresh with nothing to refresh to.
	ErrNoClaims = errors.New("no token claims to refresh to")

	// ErrSubjectChanged is a refresh to a token for somebody else.
	ErrSubjectChanged = errors.New("the token belongs to a different subject than the one this socket was opened for; reconnect instead")

	// ErrDeviceChanged is a refresh to a token bound to another device, or to
	// none where the socket had one. The device is who the function was told
	// is calling, and revoking it must reach this socket.
	ErrDeviceChanged = errors.New("the token is bound to a different device than the one this socket was opened with; reconnect instead")
)
