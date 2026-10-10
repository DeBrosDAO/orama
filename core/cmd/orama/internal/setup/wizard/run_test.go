package wizard

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/DeBrosOfficial/network/cmd/orama/internal/setup"
)

func TestPump_feedsEveryLine(t *testing.T) {
	var got []string
	pump(strings.NewReader("one\ntwo\n\nthree"), func(l string) { got = append(got, l) })
	if strings.Join(got, "|") != "one|two||three" {
		t.Fatalf("got %q", got)
	}
}

func TestCaptureOutput_sendsLegacyPrintsToTheScreenAndRestores(t *testing.T) {
	m := New(context.Background(), newFake().services(), setup.Options{})
	before := os.Stdout
	restore, err := captureOutput(m)
	if err != nil {
		t.Fatal(err)
	}
	if os.Stdout == before {
		t.Fatal("standard output was not redirected")
	}
	fmt.Println("Scanning SSH host key for 203.0.113.11...")
	fmt.Fprintln(os.Stderr, "a warning")
	restore()
	if os.Stdout != before {
		t.Fatal("standard output was not restored")
	}
	var lines []string
	for len(m.feed) > 0 {
		lines = append(lines, string((<-m.feed).(lineMsg)))
	}
	if strings.Join(lines, "|") != "Scanning SSH host key for 203.0.113.11...|a warning" {
		t.Fatalf("lines %q", lines)
	}
}

func TestFeed_dropsALineWhenTheScreenIsGone(t *testing.T) {
	m := New(context.Background(), newFake().services(), setup.Options{})
	for range feedBuffer + 10 {
		m.Feed("x") // must not block
	}
	if len(m.feed) != feedBuffer {
		t.Fatalf("%d queued", len(m.feed))
	}
}

func TestFinish_waitsForAnInterruptedRunToEnd(t *testing.T) {
	m := New(context.Background(), newFake().services(), setup.Options{})
	m.running = true
	go func() {
		m.feed <- lineMsg("closing a connection")
		m.feed <- doneMsg{res: &setup.Result{Env: "e"}, err: context.Canceled}
	}()
	m.finish()
	if m.running || m.result == nil || m.result.Env != "e" || m.ctx.Err() == nil {
		t.Fatalf("running=%v result=%+v ctxErr=%v: the run is cancelled and awaited", m.running, m.result, m.ctx.Err())
	}
}

func TestFinish_nothingToWaitForWhenNoRunStarted(t *testing.T) {
	m := New(context.Background(), newFake().services(), setup.Options{})
	m.finish() // must return at once
}
