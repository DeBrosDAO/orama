package authcmd

import (
	"strconv"

	"github.com/DeBrosOfficial/network/pkg/cli/clierr"
)

// parseSessionID reads the id 'orama auth sessions' printed.
//
// It refuses anything that is not one rather than passing a zero along: a
// command that ends "session 0" would report success against a session that
// was never named.
func parseSessionID(raw string) (int64, error) {
	id, err := strconv.ParseInt(raw, 10, 64)
	if err != nil || id <= 0 {
		return 0, clierr.Usage("%q is not a session id; 'orama auth sessions' lists them", raw)
	}
	return id, nil
}
