package brief

import (
	"strings"
	"unicode"
)

const (
	phraseRemoved = "[phrase removed]"
	maxRunes      = 120
)

// Sanitize makes third-party text safe to print: control and invisible
// characters are dropped, whitespace collapsed, anything Hermes's prompt
// scanner would block is replaced by a marker, and the result is capped.
func Sanitize(raw string) string {
	var b strings.Builder
	for _, r := range raw {
		switch {
		case unicode.IsSpace(r) || unicode.Is(unicode.Z, r):
			b.WriteRune(' ')
		case unicode.Is(unicode.Cc, r) || unicode.Is(unicode.Cf, r):
			// dropped
		default:
			b.WriteRune(r)
		}
	}
	s := strings.Join(strings.Fields(b.String()), " ")
	for changed := true; changed; {
		changed = false
		for _, re := range ScannerPatterns {
			if re.MatchString(s) {
				s = re.ReplaceAllLiteralString(s, phraseRemoved)
				changed = true
			}
		}
	}
	if rs := []rune(s); len(rs) > maxRunes {
		s = string(rs[:maxRunes-1]) + "…"
	}
	return s
}

// Untrusted builds a Fact whose value is third-party text, sanitised.
func Untrusted(key, raw string) Fact {
	return Fact{Key: key, Value: Sanitize(raw), Untrusted: true}
}
