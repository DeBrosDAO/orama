package client

import "testing"

func TestDatabaseClient_openURL_readLevel(t *testing.T) {
	tests := []struct {
		name  string
		level string
		url   string
		want  string
	}{
		{"default is the local replica", "", "http://h:4001", "http://h:4001?disableClusterDiscovery=true&level=none"},
		{"weak routes to the leader", ReadLevelWeak, "http://h:4001", "http://h:4001?disableClusterDiscovery=true&level=weak"},
		{"existing query string is extended", ReadLevelWeak, "http://h:4001?x=1", "http://h:4001?x=1&disableClusterDiscovery=true&level=weak"},
		{"explicit none", ReadLevelNone, "http://h:4001", "http://h:4001?disableClusterDiscovery=true&level=none"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			d := &DatabaseClientImpl{client: &Client{config: &ClientConfig{DatabaseReadLevel: tt.level}}}
			if got := d.openURL(tt.url); got != tt.want {
				t.Errorf("openURL = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestDatabaseClient_openURL_noConfig(t *testing.T) {
	d := &DatabaseClientImpl{}
	if got, want := d.openURL("http://h:4001"), "http://h:4001?disableClusterDiscovery=true&level=none"; got != want {
		t.Errorf("openURL = %q, want %q", got, want)
	}
}
