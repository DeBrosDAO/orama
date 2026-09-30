package ns

import (
	"context"
	"fmt"
	"runtime"
	"strings"
	"testing"
)

type fatalTB struct {
	testing.TB
	msg string
}

func (f *fatalTB) Helper()                  {}
func (f *fatalTB) Name() string             { return "TestFake" }
func (f *fatalTB) Context() context.Context { return context.Background() }
func (f *fatalTB) TempDir() string          { return f.TB.TempDir() }
func (f *fatalTB) Cleanup(func())           {}
func (f *fatalTB) Fatal(args ...any)        { f.msg = fmt.Sprint(args...); runtime.Goexit() }

// TestCreateViaOperator_nothingCreatedWhenSetupFails: a failure preparing the
// namespace's CLI must stop the test before `namespace create`, or the
// namespace would exist with no cleanup registered to delete it.
func TestCreateViaOperator_nothingCreatedWhenSetupFails(t *testing.T) {
	cli, log := fakeCLI(t, "open") // its HOME has no environment list
	tb := &fatalTB{TB: t}
	done := make(chan struct{})
	go func() { defer close(done); createViaOperator(tb, cli, nil, "e2e-x", "https://ns-e2e-x.example") }()
	<-done
	if tb.msg == "" {
		t.Fatal("a missing environment list was accepted")
	}
	if strings.Contains(readFile(t, log), "namespace create") {
		t.Fatalf("the namespace was created before its setup could fail: %s", readFile(t, log))
	}
}
