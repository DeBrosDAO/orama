package main

import (
	"fmt"
	"strconv"
	"strings"
)

// count is a number of things given on the command line: 100000, 100_000, 100k, 10M, 1G.
type count uint64

var countSuffixes = map[byte]uint64{'k': 1_000, 'm': 1_000_000, 'g': 1_000_000_000}

// Set parses a count; it implements flag.Value.
func (c *count) Set(s string) error {
	text := strings.ToLower(strings.NewReplacer("_", "", ",", "").Replace(strings.TrimSpace(s)))
	mult := uint64(1)
	if text != "" {
		if m, ok := countSuffixes[text[len(text)-1]]; ok {
			mult, text = m, text[:len(text)-1]
		}
	}
	n, err := strconv.ParseUint(text, 10, 64)
	if err != nil {
		return fmt.Errorf("%q is not a count (digits, optionally _ separators and a k, m or g suffix)", s)
	}
	if n > ^uint64(0)/mult {
		return fmt.Errorf("%q is too large", s)
	}
	*c = count(n * mult)
	return nil
}

func (c *count) String() string { return strconv.FormatUint(uint64(*c), 10) }
