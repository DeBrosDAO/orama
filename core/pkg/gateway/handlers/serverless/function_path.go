package serverless

import "strings"

// FunctionsPathPrefix is where every per-function operation is mounted.
const FunctionsPathPrefix = "/v1/functions/"

// SecretsPathName is the first segment under FunctionsPathPrefix that is the
// secrets tree rather than a function.
const SecretsPathName = "secrets"

// SplitFunctionPath splits a path under FunctionsPathPrefix into the function
// (or "secrets") and the action after it — "" for none, "ws", "invoke",
// "triggers/{id}" — exactly as handleFunctionByName dispatches it. The route
// policy reads it too, so what a policy decides about and what the handler then
// runs are the same operation by construction.
func SplitFunctionPath(path string) (name, action string) {
	rest := strings.TrimPrefix(path, FunctionsPathPrefix)
	name, action, _ = strings.Cut(rest, "/")
	return name, action
}

// IsFunctionAction reports whether path is action on a function — not on the
// secrets tree, and not a sub-path that merely ends the same way.
func IsFunctionAction(path, action string) bool {
	name, got := SplitFunctionPath(path)
	return strings.HasPrefix(path, FunctionsPathPrefix) && name != "" && name != SecretsPathName && got == action
}
