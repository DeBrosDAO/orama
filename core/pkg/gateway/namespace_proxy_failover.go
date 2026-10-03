package gateway

import (
	"errors"
	"io"
	"net"
)

// undialedBody is a proxied request's body that can be offered to another
// namespace gateway when the first could not be dialed. The transport reads a
// body only once its connection is up, so a body nothing has read from yet was
// never sent anywhere; Close is left to the server, which owns the inbound
// body, so a failed attempt's transport cannot close it under the next one.
type undialedBody struct {
	io.ReadCloser
	read bool
}

func (b *undialedBody) Read(p []byte) (int, error) {
	n, err := b.ReadCloser.Read(p)
	if n > 0 {
		b.read = true
	}
	return n, err
}

func (b *undialedBody) Close() error { return nil }

// isDialFailure reports whether err is a failure to open the connection: the
// request never reached the upstream, so sending it to another one is safe for
// any method.
func isDialFailure(err error) bool {
	var opErr *net.OpError
	return errors.As(err, &opErr) && opErr.Op == "dial"
}

// errNoClusterSecret is returned by namespaceProxyRequest when the hop cannot
// be signed.
var errNoClusterSecret = errors.New("this gateway has no cluster secret, so it cannot authenticate itself to the namespace gateway")
