// Package sdk carries the source of the function SDK (package fn) for
// 'orama function init', which copies it into a new function's project.
package sdk

import _ "embed"

// FnSource is fn/fn.go, the source of package fn.
//
//go:embed fn/fn.go
var FnSource string
