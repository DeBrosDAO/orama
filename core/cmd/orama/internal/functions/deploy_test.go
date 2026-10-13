package functions

import "testing"

// A deploy cannot know what a function takes, so the command it suggests sends
// an empty object, whatever the function is called.
func TestInvokeHint_sendsNoInputTheFunctionDidNotAskFor(t *testing.T) {
	for _, name := range []string{"hello", "image-resize", "a"} {
		want := "orama function invoke " + name + " --data '{}'"
		if got := invokeHint(name); got != want {
			t.Errorf("invokeHint(%q) = %q, want %q", name, got, want)
		}
	}
}
