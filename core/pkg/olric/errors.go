package olric

import (
	"errors"

	olriclib "github.com/olric-data/olric"
)

// A server answers a key-level outcome as a RESP error "<PREFIX> <message>", for
// example "KEYFOUND key found". The cluster client decodes it with
// protocol.ConvertError (olric v0.7.4 internal/protocol/errors.go:81), which
// returns the sentinel only if the prefix was registered with
// protocol.SetError, and otherwise drops the prefix and returns
// fmt.Errorf("%s", message). The KEYFOUND and KEYNOTFOUND registrations run in
// internal/dmap/service.go:61 (registerErrors, called from NewService when a
// member starts); protocol is an internal package and olric exports no way to
// register codes, so a gateway, which holds a cluster client and no member,
// never has them. cluster_client.go:71 (processProtocolError) then yields a
// plain error whose only content is the sentinel's own message. That message,
// olric.go:70 and :73, is all that identifies the outcome, so the checks below
// match it exactly as well as through errors.Is.

// IsKeyFound reports whether err says the key already exists (a Put with NX
// that lost the race).
func IsKeyFound(err error) bool {
	return isOlricError(err, olriclib.ErrKeyFound)
}

// IsKeyNotFound reports whether err says the key, or the DMap holding it, does
// not exist.
func IsKeyNotFound(err error) bool {
	return isOlricError(err, olriclib.ErrKeyNotFound)
}

func isOlricError(err, sentinel error) bool {
	if err == nil {
		return false
	}
	return errors.Is(err, sentinel) || err.Error() == sentinel.Error()
}
