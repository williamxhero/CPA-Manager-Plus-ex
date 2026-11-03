package quotawire

import (
	"strings"
	"testing"
)

func TestSanitizeEncodedSecretsPreservesClassification(t *testing.T) {
	for name, encoded := range map[string]string{
		"raw":          "FixtureAlpha+opaque/value",
		"upper":        "FixtureAlpha%2Bopaque%2Fvalue",
		"lower":        "FixtureAlpha%2bopaque%2fvalue",
		"mixed":        "FixtureAlpha%2bopaque%2Fvalue",
		"double":       "FixtureAlpha%252Bopaque%252Fvalue",
		"double_mixed": "FixtureAlpha%252bopaque%252Fvalue",
	} {
		t.Run(name, func(t *testing.T) {
			got := Sanitize("ConsoleNeedLogin: lookup failed "+encoded, nil, []string{"FixtureAlpha+opaque/value"})
			if strings.Contains(got, "FixtureAlpha") || !strings.Contains(got, "ConsoleNeedLogin") || !strings.Contains(got, "[redacted]") {
				t.Fatalf("unsafe or misclassified diagnostic: %s", got)
			}
		})
	}
}

func TestSanitizeDoesNotRewriteItsOwnReplacement(t *testing.T) {
	if got := Sanitize("lookup e", nil, []string{"e", "e"}); got != "lookup [redacted]" {
		t.Fatalf("replacement was processed as another diagnostic: %s", got)
	}
	// Invalid in-process metadata must not turn the sanitization boundary into
	// a regexp panic. This value cannot occur in the valid UTF-8 diagnostic.
	if got := Sanitize("ConsoleNeedLogin", nil, []string{string([]byte{0xff})}); got != "ConsoleNeedLogin" {
		t.Fatalf("unrelated invalid metadata corrupted classification: %s", got)
	}
	invalid := string([]byte{0xff})
	if got := Sanitize(invalid, nil, []string{invalid, "e"}); got != "[redacted]" {
		t.Fatalf("invalid-byte replacement was processed twice: %s", got)
	}
}
