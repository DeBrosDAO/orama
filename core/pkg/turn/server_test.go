package turn

import (
	"fmt"
	"testing"
	"time"
)

func TestGenerateCredentials(t *testing.T) {
	secret := "test-secret-key-32bytes-long!!!!"
	namespace := "test-namespace"
	ttl := 10 * time.Minute

	username, password := GenerateCredentials(secret, namespace, ttl)

	if username == "" {
		t.Fatal("username should not be empty")
	}
	if password == "" {
		t.Fatal("password should not be empty")
	}

	// Username should be "{timestamp}:{namespace}"
	var ts int64
	var ns string
	n, err := fmt.Sscanf(username, "%d:%s", &ts, &ns)
	if err != nil || n != 2 {
		t.Fatalf("username format should be timestamp:namespace, got %q", username)
	}

	if ns != namespace {
		t.Fatalf("namespace in username should be %q, got %q", namespace, ns)
	}

	// Timestamp should be ~10 minutes in the future
	now := time.Now().Unix()
	expectedExpiry := now + int64(ttl.Seconds())
	if ts < expectedExpiry-2 || ts > expectedExpiry+2 {
		t.Fatalf("expiry timestamp should be ~%d, got %d", expectedExpiry, ts)
	}
}

func TestGeneratePassword(t *testing.T) {
	secret := "test-secret"
	username := "1234567890:test-ns"

	password1 := GeneratePassword(secret, username)
	password2 := GeneratePassword(secret, username)

	// Same inputs should produce same output
	if password1 != password2 {
		t.Fatal("GeneratePassword should be deterministic")
	}

	// Different secret should produce different output
	password3 := GeneratePassword("different-secret", username)
	if password1 == password3 {
		t.Fatal("different secrets should produce different passwords")
	}

	// Different username should produce different output
	password4 := GeneratePassword(secret, "9999999999:other-ns")
	if password1 == password4 {
		t.Fatal("different usernames should produce different passwords")
	}
}

func TestValidateCredentials(t *testing.T) {
	secret := "test-secret-key"
	namespace := "my-namespace"
	ttl := 10 * time.Minute

	// Generate valid credentials
	username, password := GenerateCredentials(secret, namespace, ttl)

	tests := []struct {
		name      string
		secret    string
		username  string
		password  string
		namespace string
		wantValid bool
	}{
		{
			name:      "valid credentials",
			secret:    secret,
			username:  username,
			password:  password,
			namespace: namespace,
			wantValid: true,
		},
		{
			name:      "wrong secret",
			secret:    "wrong-secret",
			username:  username,
			password:  password,
			namespace: namespace,
			wantValid: false,
		},
		{
			name:      "wrong password",
			secret:    secret,
			username:  username,
			password:  "wrongpassword",
			namespace: namespace,
			wantValid: false,
		},
		{
			name:      "wrong namespace",
			secret:    secret,
			username:  username,
			password:  password,
			namespace: "other-namespace",
			wantValid: false,
		},
		{
			name:      "expired credentials",
			secret:    secret,
			username:  fmt.Sprintf("%d:%s", time.Now().Unix()-60, namespace),
			password:  GeneratePassword(secret, fmt.Sprintf("%d:%s", time.Now().Unix()-60, namespace)),
			namespace: namespace,
			wantValid: false,
		},
		{
			name:      "malformed username - no colon",
			secret:    secret,
			username:  "badusername",
			password:  "whatever",
			namespace: namespace,
			wantValid: false,
		},
		{
			name:      "malformed username - non-numeric timestamp",
			secret:    secret,
			username:  "notanumber:my-namespace",
			password:  "whatever",
			namespace: namespace,
			wantValid: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ValidateCredentials(tt.secret, tt.username, tt.password, tt.namespace)
			if got != tt.wantValid {
				t.Errorf("ValidateCredentials() = %v, want %v", got, tt.wantValid)
			}
		})
	}
}

func TestConfigValidation(t *testing.T) {
	tests := []struct {
		name     string
		config   Config
		wantErrs int
	}{
		{
			name: "valid config",
			config: Config{
				ListenAddr:     "0.0.0.0:3478",
				PublicIP:       "1.2.3.4",
				Realm:          "dbrs.space",
				AuthSecret:     "secret123",
				RelayPortStart: 49152,
				RelayPortEnd:   50000,
				Namespace:      "test-ns",
			},
			wantErrs: 0,
		},
		{
			name:   "missing all fields",
			config: Config{},
			// bugboard #283: the separate auth_secret and namespace errors collapsed
			// into one "no tenants configured" — a config with neither the legacy
			// pair nor a tenants list serves nobody, which is a single fault.
			wantErrs: 5, // listen_addr, public_ip, realm, no-tenants, relay_port_range
		},
		{
			name: "invalid public IP",
			config: Config{
				ListenAddr:     "0.0.0.0:3478",
				PublicIP:       "not-an-ip",
				Realm:          "dbrs.space",
				AuthSecret:     "secret",
				RelayPortStart: 49152,
				RelayPortEnd:   50000,
				Namespace:      "test-ns",
			},
			wantErrs: 1,
		},
		{
			name: "relay range too small",
			config: Config{
				ListenAddr:     "0.0.0.0:3478",
				PublicIP:       "1.2.3.4",
				Realm:          "dbrs.space",
				AuthSecret:     "secret",
				RelayPortStart: 49152,
				RelayPortEnd:   49200,
				Namespace:      "test-ns",
			},
			wantErrs: 1,
		},
		{
			name: "relay range inverted",
			config: Config{
				ListenAddr:     "0.0.0.0:3478",
				PublicIP:       "1.2.3.4",
				Realm:          "dbrs.space",
				AuthSecret:     "secret",
				RelayPortStart: 50000,
				RelayPortEnd:   49152,
				Namespace:      "test-ns",
			},
			wantErrs: 1,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			errs := tt.config.Validate()
			if len(errs) != tt.wantErrs {
				t.Errorf("Validate() returned %d errors, want %d: %v", len(errs), tt.wantErrs, errs)
			}
		})
	}
}
