// Package rqlitelog reads what rqlited writes to the journal.
package rqlitelog

import (
	"net"
	"strconv"
	"strings"
)

// LeaderLine is the text of the line every member's store logs when the leader
// changes: "node <id> at <host>:<port> is now Leader". hashicorp raft's own
// "entering leader state" is an INFO line, and rqlite runs raft at WARN, so it
// is never logged.
const LeaderLine = "is now Leader"

// ParseLeaderLine reads a short-unix journal line "<unix> … node <id> at
// <host>:<port> is now Leader" into its time and the leader's host. It reads
// from the end, so an id with spaces in it does not matter, and reports false
// for anything else, including "Leader is now unknown".
func ParseLeaderLine(line string) (at float64, host string, ok bool) {
	fields := strings.Fields(line)
	if len(fields) < 5 || strings.Join(fields[len(fields)-3:], " ") != LeaderLine {
		return 0, "", false
	}
	at, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		return 0, "", false
	}
	host, _, err = net.SplitHostPort(fields[len(fields)-4])
	if err != nil {
		return 0, "", false
	}
	return at, host, true
}
