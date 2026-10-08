package updatepolicy

import "testing"

func TestValidate_acceptsTheDocumentedValuesAndRefusesTheRest(t *testing.T) {
	good := map[string][]string{
		KeyMode:    {"off", "notify", "auto"},
		KeyChannel: {"stable", "nightly", "beta-2"},
		KeyWindow:  {"", "1-5", "22-4", "0-23", "5-5"},
		KeyRepo:    {"", "https://releases.example.org/tuf", "http://127.0.0.1:8080"},
	}
	bad := map[string][]string{
		KeyMode:    {"", "yes", "AUTO", "auto "},
		KeyChannel: {"", "Stable", "a/b", "../x", "this-channel-name-is-far-longer-than-allowed"},
		KeyWindow:  {"night", "1-24", "-1-3", "1", "1-", "-5", "a-b", "1-5-7"},
		KeyRepo:    {"releases.example.org", "http://releases.example.org", "ftp://x", "https://u:p@x"},
	}
	for key, values := range good {
		for _, v := range values {
			if err := Validate(key, v); err != nil {
				t.Errorf("%s=%q: %v", key, v, err)
			}
		}
	}
	for key, values := range bad {
		for _, v := range values {
			if err := Validate(key, v); err == nil {
				t.Errorf("%s=%q was accepted", key, v)
			}
		}
	}
	if err := Validate("max_parallel", "2"); err == nil {
		t.Error("an unknown key was accepted")
	}
}

func TestParseWindow(t *testing.T) {
	w, err := ParseWindow("22-4")
	if err != nil || w != (Window{Start: 22, End: 4}) {
		t.Fatalf("22-4 -> %+v, %v", w, err)
	}
	if w, err := ParseWindow(""); err != nil || w != (Window{}) {
		t.Fatalf("empty -> %+v, %v", w, err)
	}
}
