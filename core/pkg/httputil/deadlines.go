package httputil

import (
	"errors"
	"fmt"
	"net/http"
	"time"
)

// TransferBudget is how long a whole-database transfer (a namespace backup or
// restore, an RQLite export or import) may take to be read and written, on
// every gateway it passes through. The gateways' http.Server read and write
// timeouts (60s and 120s) would otherwise cut a database of a few hundred MiB
// off mid-transfer whatever any proxy allowed it.
const TransferBudget = 5 * time.Minute

// ExtendIO moves this request's read and write deadlines to budget from now,
// replacing the server's own. It is for the routes that move a whole database;
// every other route keeps the server's timeouts.
//
// A ResponseWriter that cannot move its deadlines (a test recorder) is not an
// error: it has none to move. Any other failure is returned, because carrying
// on would cut the transfer off at the server's timeout.
func ExtendIO(w http.ResponseWriter, budget time.Duration) error {
	rc := http.NewResponseController(w)
	deadline := time.Now().Add(budget)
	if err := rc.SetReadDeadline(deadline); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return fmt.Errorf("extend the read deadline: %w", err)
	}
	if err := rc.SetWriteDeadline(deadline); err != nil && !errors.Is(err, http.ErrNotSupported) {
		return fmt.Errorf("extend the write deadline: %w", err)
	}
	return nil
}
