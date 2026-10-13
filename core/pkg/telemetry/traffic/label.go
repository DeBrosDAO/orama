package traffic

// Namespace labels come from request data an attacker controls (the Host of
// an ns-<name> request, a path segment of a function invocation), so they are
// validated before they become map keys. The reserved labels below contain
// parentheses, which no namespace name can, so they never collide with one.
const (
	// OtherLabel collects the namespaces seen in a second after
	// MaxNamespacesPerSecond distinct ones were already tracked.
	OtherLabel = "(other)"
	// InvalidLabel collects requests whose namespace is empty, too long, or
	// contains a character no namespace name can.
	InvalidLabel = "(invalid)"

	// MaxLabelLen is the longest namespace name accepted as its own label,
	// matching the 64-character cap of the namespace name validators.
	MaxLabelLen = 64
)

// sanitizeLabel returns namespace when it is a well-formed namespace name
// ([A-Za-z0-9][A-Za-z0-9_-]*, at most MaxLabelLen bytes) and InvalidLabel
// otherwise. It does not allocate: this runs on every request.
func sanitizeLabel(namespace string) string {
	if namespace == "" || len(namespace) > MaxLabelLen {
		return InvalidLabel
	}
	for i := 0; i < len(namespace); i++ {
		c := namespace[i]
		alnum := c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9'
		if alnum || (i > 0 && (c == '-' || c == '_')) {
			continue
		}
		return InvalidLabel
	}
	return namespace
}
