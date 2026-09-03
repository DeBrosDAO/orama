package credentials

import (
	"strings"
	"testing"
)

// fakeValidator is a no-op Validator for registry tests.
type fakeValidator struct{ name string }

func (v fakeValidator) Provider() string                     { return v.name }
func (v fakeValidator) Validate(_ []byte) error              { return nil }
func (v fakeValidator) Redact(b []byte) (interface{}, error) { return string(b), nil }

func TestRegistry_RegisterLookup(t *testing.T) {
	defer resetRegistry()
	resetRegistry()

	Register(fakeValidator{name: "apns"})
	Register(fakeValidator{name: "ntfy"})

	if _, ok := LookupValidator("apns"); !ok {
		t.Error("apns not found after Register")
	}
	if _, ok := LookupValidator("ntfy"); !ok {
		t.Error("ntfy not found after Register")
	}
	if _, ok := LookupValidator("nonexistent"); ok {
		t.Error("LookupValidator returned true for unregistered provider")
	}
}

func TestRegistry_ReregisterReplaces(t *testing.T) {
	defer resetRegistry()
	resetRegistry()

	Register(fakeValidator{name: "apns"})
	v, _ := LookupValidator("apns")
	if v.(fakeValidator).name != "apns" {
		t.Fatal("setup: wrong validator returned")
	}

	type replacement struct{ fakeValidator }
	r := replacement{fakeValidator{name: "apns"}}
	Register(r)
	got, _ := LookupValidator("apns")
	if _, ok := got.(replacement); !ok {
		t.Errorf("Re-register did not replace; got %T", got)
	}
}

func TestRegistry_RegisteredProviders(t *testing.T) {
	defer resetRegistry()
	resetRegistry()

	Register(fakeValidator{name: "apns"})
	Register(fakeValidator{name: "ntfy"})
	Register(fakeValidator{name: "fcm"})

	names := RegisteredProviders()
	if len(names) != 3 {
		t.Errorf("expected 3 registered; got %d (%v)", len(names), names)
	}
	for _, want := range []string{"apns", "ntfy", "fcm"} {
		found := false
		for _, n := range names {
			if n == want {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("expected %q in RegisteredProviders, got %v", want, names)
		}
	}
}

func TestRegistry_PanicsOnNilOrEmpty(t *testing.T) {
	defer resetRegistry()
	resetRegistry()

	defer func() {
		r := recover()
		if r == nil {
			t.Error("expected panic on nil Validator; got none")
		}
		if !strings.Contains(toString(r), "nil") {
			t.Errorf("panic message should mention nil; got %v", r)
		}
	}()
	Register(nil)
}

func TestRegistry_PanicsOnEmptyName(t *testing.T) {
	defer resetRegistry()
	resetRegistry()

	defer func() {
		r := recover()
		if r == nil {
			t.Error("expected panic on empty Provider() name; got none")
		}
	}()
	Register(fakeValidator{name: ""})
}

func toString(v interface{}) string {
	switch s := v.(type) {
	case string:
		return s
	case error:
		return s.Error()
	default:
		return ""
	}
}
