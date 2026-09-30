// Package roomid holds the one rule for what a WebRTC room id may be, shared by
// the namespace gateway (which routes on it) and the SFU (which joins on it), so
// the two can never disagree about a room's name.
package roomid

import "fmt"

// MaxLen is the longest room id accepted.
const MaxLen = 128

// Validate reports why id is not a usable room id: 1 to MaxLen printable ASCII
// characters, no whitespace or control characters.
func Validate(id string) error {
	if id == "" {
		return fmt.Errorf("room id must not be empty")
	}
	if len(id) > MaxLen {
		return fmt.Errorf("room id is %d bytes, the limit is %d", len(id), MaxLen)
	}
	for i := 0; i < len(id); i++ {
		if c := id[i]; c <= 0x20 || c >= 0x7f {
			return fmt.Errorf("room id has a character that is not printable ASCII at byte %d", i)
		}
	}
	return nil
}
