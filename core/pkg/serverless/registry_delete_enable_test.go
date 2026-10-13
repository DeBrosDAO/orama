package serverless

import (
	"context"
	"errors"
	"testing"
)

// A delete is soft, and enabling a function used to flip every row of the name
// back to active, so `function enable` revived a deleted function.
func TestSetEnabled_doesNotReviveADeletedFunction(t *testing.T) {
	r, _ := newVersionedRegistry(t)
	ctx := context.Background()
	deployN(t, r, "fn", 2)
	if err := r.Delete(ctx, "ns", "fn", 0); err != nil {
		t.Fatalf("Delete: %v", err)
	}
	if err := r.SetEnabled(ctx, "ns", "fn", true); !errors.Is(err, ErrFunctionNotFound) {
		t.Fatalf("SetEnabled after delete = %v; want ErrFunctionNotFound", err)
	}
	if _, err := r.Get(ctx, "ns", "fn", 0); !errors.Is(err, ErrFunctionNotFound) {
		t.Fatalf("Get after delete and enable = %v; want ErrFunctionNotFound", err)
	}
}

func TestSetEnabled_disableThenEnableStillWorks(t *testing.T) {
	r, _ := newVersionedRegistry(t)
	ctx := context.Background()
	deployN(t, r, "fn", 1)
	if err := r.SetEnabled(ctx, "ns", "fn", false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if _, err := r.Get(ctx, "ns", "fn", 0); err == nil {
		t.Fatal("a disabled function must not resolve")
	}
	if err := r.SetEnabled(ctx, "ns", "fn", true); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if _, err := r.Get(ctx, "ns", "fn", 0); err != nil {
		t.Fatalf("Get after enable: %v", err)
	}
}

func TestSetEnabled_deletedVersionStaysDeleted(t *testing.T) {
	r, _ := newVersionedRegistry(t)
	ctx := context.Background()
	deployN(t, r, "fn", 2)
	if err := r.Delete(ctx, "ns", "fn", 1); err != nil {
		t.Fatalf("Delete v1: %v", err)
	}
	if err := r.SetEnabled(ctx, "ns", "fn", false); err != nil {
		t.Fatalf("disable: %v", err)
	}
	if err := r.SetEnabled(ctx, "ns", "fn", true); err != nil {
		t.Fatalf("enable: %v", err)
	}
	if _, err := r.Get(ctx, "ns", "fn", 1); err == nil {
		t.Error("enable revived the deleted version 1")
	}
	if _, err := r.Get(ctx, "ns", "fn", 2); err != nil {
		t.Errorf("version 2 should be active: %v", err)
	}
}
