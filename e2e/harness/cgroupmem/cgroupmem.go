// Package cgroupmem reads a cgroup v2 memory.stat.
package cgroupmem

import (
	"fmt"
	"strconv"
	"strings"
)

// Anon is the memory.stat field for a cgroup's anonymous memory: the
// heap and stacks its processes allocated, which is what a leak grows. The
// cgroup's memory.current also counts the page cache of the files it wrote,
// which the kernel reclaims under pressure: an IPFS node that stores uploads
// grows it by the size of the blocks it wrote, with a flat heap.
const Anon = "anon"

// Field is field's value in bytes in a cgroup v2 memory.stat.
func Field(stat, field string) (int64, error) {
	for _, line := range strings.Split(stat, "\n") {
		name, value, ok := strings.Cut(strings.TrimSpace(line), " ")
		if !ok || name != field {
			continue
		}
		v, err := strconv.ParseInt(strings.TrimSpace(value), 10, 64)
		if err != nil {
			return 0, fmt.Errorf("memory.stat %s is %q: %w", field, value, err)
		}
		return v, nil
	}
	return 0, fmt.Errorf("memory.stat has no %s field", field)
}
