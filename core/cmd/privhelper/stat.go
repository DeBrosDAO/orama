package main

import (
	"fmt"
	"strconv"
	"strings"
)

// statStartTimeField is starttime's position in /proc/<pid>/stat counting
// from the state field, the first after the parenthesised command name
// (proc(5): field 22, state is field 3).
const statStartTimeField = 22 - 3

// parseStartTime reads starttime — clock ticks after boot — from the content
// of /proc/<pid>/stat. The command name is in parentheses and may itself hold
// spaces and parentheses, so the fields are counted from the last ')'.
func parseStartTime(stat string) (uint64, error) {
	end := strings.LastIndexByte(stat, ')')
	if end < 0 {
		return 0, fmt.Errorf("not a /proc/<pid>/stat line: no command name")
	}
	fields := strings.Fields(stat[end+1:])
	if len(fields) <= statStartTimeField {
		return 0, fmt.Errorf("not a /proc/<pid>/stat line: %d fields after the command name", len(fields))
	}
	start, err := strconv.ParseUint(fields[statStartTimeField], 10, 64)
	if err != nil {
		return 0, fmt.Errorf("starttime %q: %w", fields[statStartTimeField], err)
	}
	return start, nil
}
