package httputil

import "testing"

func TestValidateCID(t *testing.T) {
	tests := []struct {
		name  string
		cid   string
		valid bool
	}{
		{
			name:  "valid CIDv0",
			cid:   "QmYwAPJzv5CZsnA625s3Xf2nemtYgPpHdWEz79ojWnPbdG",
			valid: true,
		},
		{
			name:  "valid CIDv1 base32",
			cid:   "bafybeigdyrzt5sfp7udm7hu76uh7y26nf3efuylqabf3oclgtqy55fbzdi",
			valid: true,
		},
		{
			name:  "invalid CID",
			cid:   "not-a-cid",
			valid: false,
		},
		{
			name:  "empty string",
			cid:   "",
			valid: false,
		},
		{
			name:  "whitespace only",
			cid:   "   ",
			valid: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ValidateCID(tt.cid); got != tt.valid {
				t.Errorf("ValidateCID(%q) = %v, want %v", tt.cid, got, tt.valid)
			}
		})
	}
}

func TestValidateNamespace(t *testing.T) {
	tests := []struct {
		name      string
		namespace string
		valid     bool
	}{
		{
			name:      "valid simple",
			namespace: "default",
			valid:     true,
		},
		{
			name:      "valid with hyphen",
			namespace: "my-namespace",
			valid:     true,
		},
		{
			name:      "valid with underscore",
			namespace: "my_namespace",
			valid:     true,
		},
		{
			name:      "valid alphanumeric",
			namespace: "namespace123",
			valid:     true,
		},
		{
			name:      "invalid - starts with hyphen",
			namespace: "-namespace",
			valid:     false,
		},
		{
			name:      "invalid - special chars",
			namespace: "namespace!",
			valid:     false,
		},
		{
			name:      "invalid - empty",
			namespace: "",
			valid:     false,
		},
		{
			name:      "invalid - too long",
			namespace: "a123456789012345678901234567890123456789012345678901234567890123456789",
			valid:     false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ValidateNamespace(tt.namespace); got != tt.valid {
				t.Errorf("ValidateNamespace(%q) = %v, want %v", tt.namespace, got, tt.valid)
			}
		})
	}
}

func TestValidateTopicName(t *testing.T) {
	tests := []struct {
		name  string
		topic string
		valid bool
	}{
		{
			name:  "valid simple",
			topic: "mytopic",
			valid: true,
		},
		{
			name:  "valid with path",
			topic: "events/user/created",
			valid: true,
		},
		{
			name:  "valid with dots",
			topic: "com.example.events",
			valid: true,
		},
		{
			name:  "invalid - special chars",
			topic: "topic!@#",
			valid: false,
		},
		{
			name:  "invalid - empty",
			topic: "",
			valid: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ValidateTopicName(tt.topic); got != tt.valid {
				t.Errorf("ValidateTopicName(%q) = %v, want %v", tt.topic, got, tt.valid)
			}
		})
	}
}

func TestValidateWalletAddress(t *testing.T) {
	tests := []struct {
		name   string
		wallet string
		valid  bool
	}{
		{
			name:   "valid with 0x prefix",
			wallet: "0x742d35Cc6634C0532925a3b844Bc9e7595f0bEbC",
			valid:  true,
		},
		{
			name:   "valid without 0x prefix",
			wallet: "742d35Cc6634C0532925a3b844Bc9e7595f0bEbC",
			valid:  true,
		},
		{
			name:   "invalid - too short",
			wallet: "0x123",
			valid:  false,
		},
		{
			name:   "invalid - non-hex chars",
			wallet: "0xZZZd35Cc6634C0532925a3b844Bc9e7595f0bEbC",
			valid:  false,
		},
		{
			name:   "invalid - empty",
			wallet: "",
			valid:  false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ValidateWalletAddress(tt.wallet); got != tt.valid {
				t.Errorf("ValidateWalletAddress(%q) = %v, want %v", tt.wallet, got, tt.valid)
			}
		})
	}
}

func TestNormalizeWalletAddress(t *testing.T) {
	tests := []struct {
		name   string
		wallet string
		want   string
	}{
		{
			name:   "with 0x prefix",
			wallet: "0xABCdef123456789",
			want:   "abcdef123456789",
		},
		{
			name:   "without prefix",
			wallet: "ABCdef123456789",
			want:   "abcdef123456789",
		},
		{
			name:   "with whitespace",
			wallet: "  0xABCdef  ",
			want:   "abcdef",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := NormalizeWalletAddress(tt.wallet); got != tt.want {
				t.Errorf("NormalizeWalletAddress(%q) = %v, want %v", tt.wallet, got, tt.want)
			}
		})
	}
}

func TestIsEmpty(t *testing.T) {
	tests := []struct {
		name string
		s    string
		want bool
	}{
		{"empty string", "", true},
		{"whitespace only", "   ", true},
		{"non-empty", "hello", false},
		{"tabs and spaces", "\t  \n", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsEmpty(tt.s); got != tt.want {
				t.Errorf("IsEmpty(%q) = %v, want %v", tt.s, got, tt.want)
			}
		})
	}
}

func TestIsNotEmpty(t *testing.T) {
	tests := []struct {
		name string
		s    string
		want bool
	}{
		{"empty string", "", false},
		{"whitespace only", "   ", false},
		{"non-empty", "hello", true},
		{"tabs and spaces", "\t  \n", false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := IsNotEmpty(tt.s); got != tt.want {
				t.Errorf("IsNotEmpty(%q) = %v, want %v", tt.s, got, tt.want)
			}
		})
	}
}

func TestValidateDMapName(t *testing.T) {
	tests := []struct {
		name  string
		dmap  string
		valid bool
	}{
		{
			name:  "valid simple",
			dmap:  "mymap",
			valid: true,
		},
		{
			name:  "valid with hyphen",
			dmap:  "my-map",
			valid: true,
		},
		{
			name:  "valid with underscore",
			dmap:  "my_map",
			valid: true,
		},
		{
			name:  "valid with dots",
			dmap:  "my.map.v1",
			valid: true,
		},
		{
			name:  "invalid - special chars",
			dmap:  "map!@#",
			valid: false,
		},
		{
			name:  "invalid - empty",
			dmap:  "",
			valid: false,
		},
		{
			name:  "invalid - slash",
			dmap:  "my/map",
			valid: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := ValidateDMapName(tt.dmap); got != tt.valid {
				t.Errorf("ValidateDMapName(%q) = %v, want %v", tt.dmap, got, tt.valid)
			}
		})
	}
}
