package runctx

import (
	"context"
	"testing"
)

func TestWith_endsWithTheRunOrTheParent(t *testing.T) {
	Reset()
	t.Cleanup(Reset)
	parent, cancelParent := context.WithCancel(context.Background())
	ctx, release := With(parent)
	defer release()
	if ctx.Err() != nil {
		t.Fatal("ended before anything was cancelled")
	}
	cancelParent()
	<-ctx.Done()
	ctx2, release2 := With(context.Background())
	defer release2()
	Cancel()
	<-ctx2.Done()
	if ctx2.Err() == nil {
		t.Fatal("the run's cancellation did not reach the derived context")
	}
}

func TestWith_releaseDoesNotCancelTheRun(t *testing.T) {
	Reset()
	t.Cleanup(Reset)
	_, release := With(context.Background())
	release()
	if Context().Err() != nil {
		t.Fatal("releasing a derived context cancelled the run")
	}
}
