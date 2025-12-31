package rqlite

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestExponentialBackoff(t *testing.T) {
	r := &RQLiteManager{}
	baseDelay := 100 * time.Millisecond
	maxDelay := 1 * time.Second

	tests := []struct {
		attempt  int
		expected time.Duration
	}{
		{0, 100 * time.Millisecond},
		{1, 200 * time.Millisecond},
		{2, 400 * time.Millisecond},
		{3, 800 * time.Millisecond},
		{4, 1000 * time.Millisecond}, // Maxed out
		{10, 1000 * time.Millisecond}, // Maxed out
	}

	for _, tt := range tests {
		got := r.exponentialBackoff(tt.attempt, baseDelay, maxDelay)
		if got != tt.expected {
			t.Errorf("exponentialBackoff(%d) = %v; want %v", tt.attempt, got, tt.expected)
		}
	}
}

func TestRQLiteDataDirPath(t *testing.T) {
	// Test with explicit path
	r := &RQLiteManager{dataDir: "/tmp/data"}
	got, _ := r.rqliteDataDirPath()
	expected := filepath.Join("/tmp/data", "rqlite")
	if got != expected {
		t.Errorf("rqliteDataDirPath() = %s; want %s", got, expected)
	}

	// Test with environment variable expansion
	os.Setenv("TEST_DATA_DIR", "/tmp/env-data")
	defer os.Unsetenv("TEST_DATA_DIR")
	r = &RQLiteManager{dataDir: "$TEST_DATA_DIR"}
	got, _ = r.rqliteDataDirPath()
	expected = filepath.Join("/tmp/env-data", "rqlite")
	if got != expected {
		t.Errorf("rqliteDataDirPath() with env = %s; want %s", got, expected)
	}

	// Test with home directory expansion
	r = &RQLiteManager{dataDir: "~/data"}
	got, _ = r.rqliteDataDirPath()
	home, _ := os.UserHomeDir()
	expected = filepath.Join(home, "data", "rqlite")
	if got != expected {
		t.Errorf("rqliteDataDirPath() with ~ = %s; want %s", got, expected)
	}
}

func TestHasExistingState(t *testing.T) {
	r := &RQLiteManager{}
	
	// Create a temp directory for testing
	tmpDir, err := os.MkdirTemp("", "rqlite-test-*")
	if err != nil {
		t.Fatalf("failed to create temp dir: %v", err)
	}
	defer os.RemoveAll(tmpDir)

	// Test empty directory
	if r.hasExistingState(tmpDir) {
		t.Errorf("hasExistingState() = true; want false for empty dir")
	}

	// Test directory with a file
	testFile := filepath.Join(tmpDir, "test.txt")
	if err := os.WriteFile(testFile, []byte("data"), 0644); err != nil {
		t.Fatalf("failed to create test file: %v", err)
	}

	if !r.hasExistingState(tmpDir) {
		t.Errorf("hasExistingState() = false; want true for non-empty dir")
	}
}

