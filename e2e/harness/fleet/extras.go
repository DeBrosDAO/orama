package fleet

import (
	"fmt"
	"strconv"
	"strings"
)

// ExtraNamePrefix starts the default name of an on-demand server: extra-1, extra-2, ...
const ExtraNamePrefix = "extra-"

// AddExtra records an extra server created during this package, so Lookup,
// Node and AllNodes find it. Its name must be new to the run.
func (f *Fleet) AddExtra(n Node) error {
	if n.Name == "" {
		return fmt.Errorf("an extra server needs a name")
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	for _, list := range [][]Node{f.State.Nodes, f.State.Extras, f.extras, f.State.Probes} {
		for _, m := range list {
			if m.Name == n.Name {
				return fmt.Errorf("%s is already a member of run %s", n.Name, f.State.RunID)
			}
		}
	}
	f.extras = append(f.extras, n)
	return nil
}

// RemoveExtra forgets an extra AddExtra recorded; it reports whether it did.
func (f *Fleet) RemoveExtra(name string) bool {
	f.mu.Lock()
	defer f.mu.Unlock()
	for i, n := range f.extras {
		if n.Name == name {
			f.extras = append(f.extras[:i:i], f.extras[i+1:]...)
			return true
		}
	}
	return false
}

// NextExtraName is extra-<N> with N one above the highest extra-<n> in the
// run, so a name is never reused after a removal while a later extra lives on.
func NextExtraName(st *State) string {
	highest := 0
	for _, list := range [][]Node{st.Nodes, st.Extras, st.Probes} {
		for _, n := range list {
			num, ok := strings.CutPrefix(n.Name, ExtraNamePrefix)
			if !ok {
				continue
			}
			if v, err := strconv.Atoi(num); err == nil && v > highest {
				highest = v
			}
		}
	}
	return ExtraNamePrefix + strconv.Itoa(highest+1)
}
