package tornet

import (
	"fmt"
	"os"
)

// AddValidatorOnionsToFile adds validator onion services to the network file at
// path and returns the network as written and how many onions were new. The
// file is replaced atomically and keeps its mode; a file that does not pass
// Load is left untouched and the error says why. This is how a validator's
// onion address reaches the file: the address exists only once the onion role
// has started (`orama global tor info`), which is after the ceremony wrote the
// file.
func AddValidatorOnionsToFile(path string, onions ...string) (Network, int, error) {
	n, err := Load(path)
	if err != nil {
		return Network{}, 0, err
	}
	info, err := os.Stat(path)
	if err != nil {
		return Network{}, 0, fmt.Errorf("stat the Tor network file: %w", err)
	}
	updated, err := n.WithValidatorOnions(onions...)
	if err != nil {
		return Network{}, 0, err
	}
	added := len(updated.ValidatorOnions) - len(n.ValidatorOnions)
	if added == 0 {
		return updated, 0, nil
	}
	body, err := updated.Marshal()
	if err != nil {
		return Network{}, 0, err
	}
	if err := writeAtomic(path, body, info.Mode().Perm()); err != nil {
		return Network{}, 0, err
	}
	return updated, added, nil
}
