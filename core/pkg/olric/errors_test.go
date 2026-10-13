package olric

import (
	"errors"
	"fmt"
	"testing"

	olriclib "github.com/olric-data/olric"
)

func TestIsKeyFound(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"sentinel", olriclib.ErrKeyFound, true},
		{"wrapped sentinel", fmt.Errorf("put: %w", olriclib.ErrKeyFound), true},
		{"cluster client message only", errors.New("key found"), true},
		{"key not found is not key found", olriclib.ErrKeyNotFound, false},
		{"unrelated", errors.New("connection refused"), false},
		{"message that merely contains it", errors.New("the key found nothing"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsKeyFound(tt.err); got != tt.want {
				t.Errorf("IsKeyFound(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}

func TestIsKeyNotFound(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{"nil", nil, false},
		{"sentinel", olriclib.ErrKeyNotFound, true},
		{"cluster client message only", errors.New("key not found"), true},
		{"key found is not key not found", olriclib.ErrKeyFound, false},
		{"unrelated", errors.New("timeout"), false},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsKeyNotFound(tt.err); got != tt.want {
				t.Errorf("IsKeyNotFound(%v) = %v, want %v", tt.err, got, tt.want)
			}
		})
	}
}
