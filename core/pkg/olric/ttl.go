package olric

import "time"

// MaxEntryTTL is the longest expiry a cache write may ask for.
//
// Olric stores expiry as an absolute UnixNano (now + ttl). A ttl of roughly
// 235 years overflows that sum, and the entry is stored already expired while
// the write reports success. Refusing anything past this bound turns that
// silent loss into an error. A caller that wants no expiry omits the ttl.
const MaxEntryTTL = 10 * 365 * 24 * time.Hour
