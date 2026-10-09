package secrets

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// ErrGenerationGap marks a pushed root that is more than one generation ahead
// of the gateway's own: the gateway missed a fan-out. It is not a rotation the
// gateway can check by itself, but the registry, which every rotation writes
// first, can say what the cluster's root is (ResolveSuccessor).
var ErrGenerationGap = errors.New("the pushed encryption root is more than one generation ahead")

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
// longer has is a captured pre-forget push, and would restore a retired key. It
// must not lower the write level either: a level only goes up (an operator
// enables bound writes once every gateway can read them), so a captured push
// from before it was enabled would have this gateway write what older readers
// cannot open, or the reverse, depending on which way it is replayed.
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
		return fmt.Errorf("refusing the pushed encryption root: generation %d is more than one past this gateway's %d: %w", want, have, ErrGenerationGap)
	case want == have && next.CurrentIKM != current.CurrentIKM:
		return fmt.Errorf("refusing the pushed encryption root: generation %d is already held with a different key", have)
	case want == have && lowersWriteLevel(current, next):
		return fmt.Errorf("refusing the pushed encryption root: generation %d would lower the write level this gateway is at", have)
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

// lowersWriteLevel reports whether next writes in an older format than current
// does.
func lowersWriteLevel(current, next Root) bool {
	return (current.WriteBound && !next.WriteBound) || (current.WriteVersioned && !next.WriteVersioned)
}

// ResolveSuccessor is the root a gateway holding current adopts for a pushed
// next: next itself when CheckSuccessor accepts it. A gateway that missed a
// fan-out sees a push more than one generation ahead and would refuse every
// later one for ever (a push only ever carries the generation just made); for
// that case the registry, which is the source of truth and is written before
// any push, says what the root is. The registry's root is adopted when it is
// at least the pushed generation, and, at that generation, the pushed key.
func ResolveSuccessor(ctx context.Context, store Store, current, next Root) (Root, error) {
	err := CheckSuccessor(current, next)
	if !errors.Is(err, ErrGenerationGap) {
		return next, err
	}
	if store == nil {
		return Root{}, err
	}
	registry, rerr := loadFromRegistry(ctx, store)
	if rerr != nil {
		return Root{}, errors.Join(err, fmt.Errorf("read the cluster's encryption root from the registry: %w", rerr))
	}
	pushed, perr := next.Generation()
	if perr != nil {
		return Root{}, perr
	}
	held, rerr := registry.Generation()
	if rerr != nil {
		return Root{}, fmt.Errorf("the registry's encryption root is unusable: %w", rerr)
	}
	switch {
	case held < pushed:
		return Root{}, fmt.Errorf("the pushed encryption root is generation %d and the registry's is %d: %w", pushed, held, err)
	case held == pushed && registry.CurrentIKM != next.CurrentIKM:
		return Root{}, fmt.Errorf("the pushed encryption root and the registry's differ at generation %d: %w", held, err)
	}
	return registry, nil
}
