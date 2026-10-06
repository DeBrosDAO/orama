package monitor

import (
	"bufio"
	"fmt"
	"io"
	"strings"
)

// maxSSEEventBytes bounds one event's data across all its lines, so a
// stream that never ends an event cannot grow without limit. A snapshot
// travels as one data line, so it is also the line bound: the same size a
// one-shot snapshot may be.
const maxSSEEventBytes = maxSnapshotBytes

// sseLineOverhead is room for a line's field name beyond its data.
const sseLineOverhead = 64

// defaultSSEEvent is the event type of a block that names none.
const defaultSSEEvent = "message"

// sseEvent is one dispatched server-sent event.
type sseEvent struct {
	Name string
	Data string
}

// readSSE parses a text/event-stream body, calling onEvent for each complete
// event and onActivity for every line read, keepalive comments included (so a
// caller can tell a quiet stream from a dead one). It returns nil when the body
// ends, or the first error from reading or from onEvent. Per the SSE spec, data
// lines are joined with newlines, a block without data is not dispatched, and
// an event cut off by the end of the stream is dropped.
func readSSE(r io.Reader, onActivity func(), onEvent func(sseEvent) error) error {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 0, bufio.MaxScanTokenSize), maxSSEEventBytes+sseLineOverhead)
	var name string
	var data []string
	size := 0
	for sc.Scan() {
		onActivity()
		line := sc.Text()
		if line == "" {
			if len(data) > 0 {
				if err := onEvent(sseEvent{Name: eventName(name), Data: strings.Join(data, "\n")}); err != nil {
					return err
				}
			}
			name, data, size = "", nil, 0
			continue
		}
		if strings.HasPrefix(line, ":") {
			continue
		}
		field, value := splitSSEField(line)
		switch field {
		case "event":
			name = value
		case "data":
			size += len(value)
			if size > maxSSEEventBytes {
				return fmt.Errorf("an event in the stream exceeds %d bytes", maxSSEEventBytes)
			}
			data = append(data, value)
		}
	}
	if err := sc.Err(); err != nil {
		return fmt.Errorf("read event stream: %w", err)
	}
	return nil
}

// splitSSEField splits "field: value", dropping one space after the colon. A
// line with no colon is a field with an empty value.
func splitSSEField(line string) (string, string) {
	field, value, found := strings.Cut(line, ":")
	if !found {
		return line, ""
	}
	return field, strings.TrimPrefix(value, " ")
}

func eventName(name string) string {
	if name == "" {
		return defaultSSEEvent
	}
	return name
}
