package monitor

import (
	"reflect"
	"strings"
	"unicode"

	"github.com/DeBrosOfficial/network/pkg/telemetry/cluster"
)

// CleanText replaces every control character in s with a space and drops
// every Unicode format character. Text from a gateway or a node (hosts, alert
// messages, error strings) is written to the operator's terminal; an escape
// sequence inside it could move the cursor, rewrite what is on screen or set
// the window title, and a bidi override (U+202A–U+202E, U+2066–U+2069) or a
// zero-width character (U+200B–U+200F) makes the text read differently from
// what it is. Nothing the monitor shows legitimately carries one.
func CleanText(s string) string {
	if strings.IndexFunc(s, unsafeRune) < 0 {
		return s
	}
	return strings.Map(func(r rune) rune {
		switch {
		case unicode.IsControl(r):
			return ' '
		case unicode.Is(unicode.Cf, r):
			return -1
		}
		return r
	}, s)
}

// unsafeRune reports whether CleanText must replace or drop r.
func unsafeRune(r rune) bool {
	return unicode.IsControl(r) || unicode.Is(unicode.Cf, r)
}

// sanitizeSnapshot cleans every string in the snapshot in place, map keys
// included. It is applied where snapshots enter the CLI, so no view has to
// remember to.
func sanitizeSnapshot(snap *cluster.ClusterSnapshot) {
	sanitizeValue(reflect.ValueOf(snap).Elem())
}

func sanitizeValue(v reflect.Value) {
	switch v.Kind() {
	case reflect.String:
		if v.CanSet() {
			v.SetString(CleanText(v.String()))
		}
	case reflect.Pointer, reflect.Interface:
		if !v.IsNil() {
			sanitizeValue(v.Elem())
		}
	case reflect.Struct:
		for i := 0; i < v.NumField(); i++ {
			if v.Type().Field(i).IsExported() {
				sanitizeValue(v.Field(i))
			}
		}
	case reflect.Slice, reflect.Array:
		for i := 0; i < v.Len(); i++ {
			sanitizeValue(v.Index(i))
		}
	case reflect.Map:
		sanitizeMap(v)
	}
}

// sanitizeMap rebuilds a map with cleaned keys and values: map entries are
// not addressable, so each value is copied, cleaned and stored back.
func sanitizeMap(v reflect.Value) {
	if v.IsNil() {
		return
	}
	keys := v.MapKeys()
	for _, k := range keys {
		val := reflect.New(v.Type().Elem()).Elem()
		val.Set(v.MapIndex(k))
		sanitizeValue(val)
		newKey := k
		if k.Kind() == reflect.String {
			newKey = reflect.ValueOf(CleanText(k.String())).Convert(k.Type())
		}
		v.SetMapIndex(k, reflect.Value{})
		v.SetMapIndex(newKey, val)
	}
}

// cleanError is an error whose message has its control characters replaced.
// It unwraps to the original, so the exit code clierr.CodeOf reads through it
// survives. Every error a source returns passes through one: a gateway's
// answer, a certificate's hostname, a resolver's or a wallet's message can all
// carry text from outside the CLI.
type cleanError struct{ err error }

func (e cleanError) Error() string { return CleanText(e.err.Error()) }
func (e cleanError) Unwrap() error { return e.err }

// cleanErr wraps err in a cleanError, and leaves nil alone.
func cleanErr(err error) error {
	if err == nil {
		return nil
	}
	return cleanError{err: err}
}
