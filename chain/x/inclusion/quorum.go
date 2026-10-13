package inclusion

import "math/bits"

// HasQuorum reports whether power is at least 2/3 of total.
// The comparison is exact, including when power*3 does not fit in int64:
// power*3 >= total*2. Non-positive power or total is not a quorum.
func HasQuorum(power, total int64) bool {
	if power <= 0 || total <= 0 {
		return false
	}
	pHi, pLo := bits.Mul64(uint64(power), 3)
	tHi, tLo := bits.Mul64(uint64(total), 2)
	if pHi != tHi {
		return pHi > tHi
	}
	return pLo >= tLo
}
