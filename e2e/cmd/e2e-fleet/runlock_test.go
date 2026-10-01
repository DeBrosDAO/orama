package main

import "testing"

func TestRunLock_heldWhileARunnerRunsFreeAfter(t *testing.T) {
	dir := t.TempDir()
	if held, err := runHeld(dir); err != nil || held {
		t.Fatalf("a directory no runner used reads held=%v err=%v", held, err)
	}
	release, err := holdRun(dir)
	if err != nil {
		t.Fatal(err)
	}
	if held, err := runHeld(dir); err != nil || !held {
		t.Fatalf("a running runner's directory reads held=%v err=%v", held, err)
	}
	second, err := holdRun(dir)
	if err != nil {
		t.Fatalf("two runners of one directory must both hold it: %v", err)
	}
	release()
	if held, _ := runHeld(dir); !held {
		t.Fatal("the lock was released while a runner still holds it")
	}
	second()
	if held, err := runHeld(dir); err != nil || held {
		t.Fatalf("after every runner exits held=%v err=%v", held, err)
	}
}

func TestIdleRoots_splitsByLock(t *testing.T) {
	busy, idle := t.TempDir(), t.TempDir()
	release, err := holdRun(busy)
	if err != nil {
		t.Fatal(err)
	}
	defer release()
	gotIdle, gotHeld, err := idleRoots([]string{busy, idle})
	if err != nil || len(gotIdle) != 1 || gotIdle[0] != idle || len(gotHeld) != 1 || gotHeld[0] != busy {
		t.Fatalf("idle %v held %v err %v", gotIdle, gotHeld, err)
	}
}
