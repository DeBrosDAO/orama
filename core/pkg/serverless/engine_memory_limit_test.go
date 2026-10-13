package serverless

import "testing"

func TestEngine_memoryLimitMB(t *testing.T) {
	e := &Engine{config: &Config{DefaultMemoryLimitMB: 64, MaxMemoryLimitMB: 256}}
	tests := []struct {
		name string
		fnMB int
		want int
	}{
		{"function limit applies", 16, 16},
		{"zero falls back to the configured default", 0, 64},
		{"negative falls back to the configured default", -1, 64},
	}
	for _, tt := range tests {
		if got := e.memoryLimitMB(&Function{MemoryLimitMB: tt.fnMB}); got != tt.want {
			t.Errorf("%s: got %d, want %d", tt.name, got, tt.want)
		}
	}
}
