package secrets

import "sort"

// MissingEnv returns the names in required that lookup does not have or has
// empty, sorted. It returns names only: a value is never read into a message.
func MissingEnv(required []string, lookup func(string) (string, bool)) []string {
	var missing []string
	for _, name := range required {
		if v, ok := lookup(name); !ok || v == "" {
			missing = append(missing, name)
		}
	}
	sort.Strings(missing)
	return missing
}
