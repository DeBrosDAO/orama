package gateway

import "testing"

// `enforced` used to be a constant false, so `orama members list` said every
// narrowed grant was doing nothing while the data path applied it.
func TestSelectorEnforced_saysWhatTheDataPathApplies(t *testing.T) {
	for resource, want := range map[string]bool{
		"pubsub:topic=chat.*":  true,
		"fn:name=checkout":     true,
		"storage:avatars/*":    true,
		"cache:key=sessions/*": true,
		"db:table=posts":       false,
		"push:topic=x":         false,
		"nonsense":             false,
		"":                     false,
	} {
		if got := selectorEnforced(resource); got != want {
			t.Errorf("selectorEnforced(%q) = %v, want %v", resource, got, want)
		}
	}
}
