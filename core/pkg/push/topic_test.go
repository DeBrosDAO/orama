package push

import (
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"strings"
	"testing"
)

// testTopicSecret is a 32-byte secret, hex-encoded.
const testTopicSecret = "00112233445566778899aabbccddeeff00112233445566778899aabbccddeeff"

func TestTopicIDFromSecret_isSHA256OfDecodedSecret(t *testing.T) {
	raw, _ := hex.DecodeString(testTopicSecret)
	sum := sha256.Sum256(raw)
	want := hex.EncodeToString(sum[:])

	got, err := TopicIDFromSecret(testTopicSecret)
	if err != nil {
		t.Fatalf("TopicIDFromSecret: %v", err)
	}
	if got != want {
		t.Errorf("topic id = %s, want sha256 of the decoded secret %s", got, want)
	}
	if got == testTopicSecret {
		t.Error("topic id equals the secret")
	}
}

// Case is an encoding detail: the same bytes are the same topic.
func TestTopicIDFromSecret_caseInsensitiveEncoding(t *testing.T) {
	lower, err := TopicIDFromSecret(testTopicSecret)
	if err != nil {
		t.Fatalf("lower: %v", err)
	}
	upper, err := TopicIDFromSecret(strings.ToUpper(testTopicSecret))
	if err != nil {
		t.Fatalf("upper: %v", err)
	}
	if lower != upper {
		t.Errorf("same bytes gave two topic ids: %s vs %s", lower, upper)
	}
}

func TestTopicIDFromSecret_boundaries(t *testing.T) {
	cases := []struct {
		name   string
		secret string
		ok     bool
	}{
		{"empty", "", false},
		{"15 bytes", strings.Repeat("ab", TopicSecretMinBytes-1), false},
		{"16 bytes (128 bits)", strings.Repeat("ab", TopicSecretMinBytes), true},
		{"64 bytes", strings.Repeat("ab", TopicSecretMaxBytes), true},
		{"65 bytes", strings.Repeat("ab", TopicSecretMaxBytes+1), false},
		{"odd length", strings.Repeat("ab", TopicSecretMinBytes) + "a", false},
		{"not hex", strings.Repeat("zz", TopicSecretMinBytes), false},
		{"base64", "AAECAwQFBgcICQoLDA0ODxAREhMUFRYXGBkaGxwdHh8=", false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			_, err := TopicIDFromSecret(c.secret)
			if c.ok && err != nil {
				t.Fatalf("rejected a valid secret: %v", err)
			}
			if !c.ok {
				if !errors.Is(err, ErrInvalidTopicSecret) {
					t.Fatalf("err = %v, want ErrInvalidTopicSecret", err)
				}
				if c.secret != "" && strings.Contains(err.Error(), c.secret) {
					t.Errorf("the error echoes the secret: %v", err)
				}
			}
		})
	}
}

func TestValidateTopicID_acceptsWhatTopicIDFromSecretProduces(t *testing.T) {
	id, err := TopicIDFromSecret(testTopicSecret)
	if err != nil {
		t.Fatalf("TopicIDFromSecret: %v", err)
	}
	if err := ValidateTopicID(id); err != nil {
		t.Errorf("ValidateTopicID(%s) = %v", id, err)
	}
}

func TestValidateTopicID_rejectsMalformed(t *testing.T) {
	id, _ := TopicIDFromSecret(testTopicSecret)
	for name, bad := range map[string]string{
		"empty":     "",
		"uppercase": strings.ToUpper(id),
		"short":     id[:63],
		"long":      id + "0",
		"non-hex":   strings.Repeat("g", 64),
		"sql":       "' OR 1=1 --" + strings.Repeat("0", 53),
	} {
		t.Run(name, func(t *testing.T) {
			if err := ValidateTopicID(bad); !errors.Is(err, ErrInvalidTopicID) {
				t.Errorf("ValidateTopicID(%q) = %v, want ErrInvalidTopicID", bad, err)
			}
		})
	}
}
