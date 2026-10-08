package secrets

import (
	"fmt"
	"strconv"
	"strings"
)

// Generation is the root's position in its rotation history. Rotate gives each
// new root the next positive integer as its CurrentID, so the id is the
// generation: a root materialised from the cluster secret is generation 1 and
// every existing root already carries one, with nothing to migrate.
func (r Root) Generation() (int, error) {
	n, err := strconv.Atoi(strings.TrimSpace(r.CurrentID))
	if err != nil || n < 1 {
		return 0, fmt.Errorf("encryption-root id %q is not a positive integer", r.CurrentID)
	}
	return n, nil
}

// CheckSuccessor reports whether a gateway holding current may adopt next. A
// rotate fan-out is signed, but a captured one replayed inside the signature's
// window would otherwise put a gateway back on a root that has since been
// rotated away, so the generation must not go backwards, and it must not skip:
// a rotation advances by exactly one, so a pushed generation further ahead is
// not a rotation this cluster made.
//
// The same generation is accepted, because the fan-out is also how a format
// rewrite and the forgetting of the previous root reach a gateway and a failed
// fan-out is retried, but only with the same IKM (one generation has one key)
// and with a previous root that is either gone or the one this gateway holds.
// A push at the same generation that carries a previous root this gateway no
// longer has is a captured pre-forget push, and would restore a retired key.
func CheckSuccessor(current, next Root) error {
	if current.CurrentIKM == "" {
		return nil
	}
	have, err := current.Generation()
	if err != nil {
		return fmt.Errorf("this gateway's own encryption root is unusable: %w", err)
	}
	want, err := next.Generation()
	if err != nil {
		return fmt.Errorf("refusing the pushed encryption root: %w", err)
	}
	switch {
	case want < have:
		return fmt.Errorf("refusing the pushed encryption root: generation %d is older than this gateway's %d", want, have)
	case want > have+1:
		return fmt.Errorf("refusing the pushed encryption root: generation %d is more than one past this gateway's %d", want, have)
	case want == have && next.CurrentIKM != current.CurrentIKM:
		return fmt.Errorf("refusing the pushed encryption root: generation %d is already held with a different key", have)
	case want == have && !samePreviousOrForgotten(current, next):
		return fmt.Errorf("refusing the pushed encryption root: generation %d carries a previous root this gateway does not hold", have)
	}
	return nil
}

// samePreviousOrForgotten reports whether next's previous root is absent (the
// previous root was forgotten) or is the one current holds.
func samePreviousOrForgotten(current, next Root) bool {
	if next.PreviousIKM == "" && next.PreviousID == "" {
		return true
	}
	return next.PreviousIKM == current.PreviousIKM && next.PreviousID == current.PreviousID
}
