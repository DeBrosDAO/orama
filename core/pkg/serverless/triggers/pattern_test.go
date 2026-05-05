package triggers

import (
	"strings"
	"testing"
)

func TestValidatePattern_empty_returns_error(t *testing.T) {
	if err := ValidatePattern(""); err == nil {
		t.Error("expected error for empty pattern")
	}
}

func TestValidatePattern_too_long_returns_error(t *testing.T) {
	long := strings.Repeat("a", MaxPatternLength+1)
	if err := ValidatePattern(long); err == nil {
		t.Error("expected error for over-long pattern")
	}
}

func TestValidatePattern_unbalanced_brackets_returns_error(t *testing.T) {
	cases := []string{"a[b", "a]b", "[a[b]", "a]"}
	for _, c := range cases {
		if err := ValidatePattern(c); err == nil {
			t.Errorf("expected error for %q", c)
		}
	}
}

func TestValidatePattern_valid_patterns_no_error(t *testing.T) {
	cases := []string{"foo", "foo:*", "foo:**", "*.bar", "[abc]xyz", "[!a]b", "?abc"}
	for _, c := range cases {
		if err := ValidatePattern(c); err != nil {
			t.Errorf("expected no error for %q, got: %v", c, err)
		}
	}
}

func TestIsWildcard(t *testing.T) {
	cases := map[string]bool{
		"foo":          false,
		"foo:bar":      false,
		"foo:*":        true,
		"foo?bar":      true,
		"[abc]xyz":     true,
		"foo:**":       true,
		"a:b:c:d:e:f":  false,
	}
	for in, want := range cases {
		if got := IsWildcard(in); got != want {
			t.Errorf("IsWildcard(%q) = %v, want %v", in, got, want)
		}
	}
}

func TestPatternMatches_exact(t *testing.T) {
	cases := []struct {
		pattern, topic string
		want           bool
	}{
		{"foo", "foo", true},
		{"foo", "bar", false},
		{"foo:bar", "foo:bar", true},
		{"foo:bar", "foo:baz", false},
	}
	for _, c := range cases {
		if got := PatternMatches(c.pattern, c.topic); got != c.want {
			t.Errorf("PatternMatches(%q, %q) = %v, want %v", c.pattern, c.topic, got, c.want)
		}
	}
}

func TestPatternMatches_single_star_segment_bounded(t *testing.T) {
	cases := []struct {
		pattern, topic string
		want           bool
	}{
		// '*' matches within a single segment
		{"presence:*", "presence:user-1", true},
		{"presence:*", "presence:user-2", true},
		{"presence:*", "presence:", true},
		// '*' does NOT cross ':'
		{"presence:*", "presence:user:device", false},
		{"a:*:b", "a:x:b", true},
		{"a:*:b", "a:x:y:b", false},
		// Different prefix
		{"presence:*", "calls:invite", false},
	}
	for _, c := range cases {
		if got := PatternMatches(c.pattern, c.topic); got != c.want {
			t.Errorf("PatternMatches(%q, %q) = %v, want %v", c.pattern, c.topic, got, c.want)
		}
	}
}

func TestPatternMatches_double_star_crosses_segments(t *testing.T) {
	cases := []struct {
		pattern, topic string
		want           bool
	}{
		{"notify:**", "notify:user-1", true},
		{"notify:**", "notify:user:device:1", true},
		{"**", "anything:goes:here", true},
	}
	for _, c := range cases {
		if got := PatternMatches(c.pattern, c.topic); got != c.want {
			t.Errorf("PatternMatches(%q, %q) = %v, want %v", c.pattern, c.topic, got, c.want)
		}
	}
}

func TestPatternMatches_question_mark(t *testing.T) {
	cases := []struct {
		pattern, topic string
		want           bool
	}{
		{"a?c", "abc", true},
		{"a?c", "axc", true},
		{"a?c", "ac", false},
		{"a?c", "abbc", false},
	}
	for _, c := range cases {
		if got := PatternMatches(c.pattern, c.topic); got != c.want {
			t.Errorf("PatternMatches(%q, %q) = %v, want %v", c.pattern, c.topic, got, c.want)
		}
	}
}

func TestPatternMatches_character_class(t *testing.T) {
	cases := []struct {
		pattern, topic string
		want           bool
	}{
		{"[abc]xyz", "axyz", true},
		{"[abc]xyz", "bxyz", true},
		{"[abc]xyz", "dxyz", false},
		{"[!a]bc", "xbc", true},
		{"[!a]bc", "abc", false},
	}
	for _, c := range cases {
		if got := PatternMatches(c.pattern, c.topic); got != c.want {
			t.Errorf("PatternMatches(%q, %q) = %v, want %v", c.pattern, c.topic, got, c.want)
		}
	}
}

func TestPatternMatches_trailing_star_with_remaining_chars(t *testing.T) {
	// '*' can match zero characters at end.
	cases := []struct {
		pattern, topic string
		want           bool
	}{
		{"foo*", "foo", true},
		{"foo*", "foobar", true},
		{"foo*", "foobar:baz", false}, // ':' breaks single '*'
	}
	for _, c := range cases {
		if got := PatternMatches(c.pattern, c.topic); got != c.want {
			t.Errorf("PatternMatches(%q, %q) = %v, want %v", c.pattern, c.topic, got, c.want)
		}
	}
}
