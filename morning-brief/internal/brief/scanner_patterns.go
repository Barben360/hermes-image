package brief

import (
	"regexp"
	"strings"
)

// ScannerPatterns mirrors the four injection patterns Hermes applies to the
// assembled cron prompt (a match blocks the whole run).
//
// Source: Hermes v0.23.1, tools/cronjob_prompt_scan.py,
// _CRON_SKILL_ASSEMBLED_PATTERNS = _CRON_THREAT_PATTERNS[:4], matched with
// re.IGNORECASE on a str (Unicode classes):
//
//	r'ignore\s+(?:\w+\s+)*(?:previous|all|above|prior)\s+(?:\w+\s+)*instructions'
//	r'do\s+not\s+tell\s+the\s+user'
//	r'system\s+prompt\s+override'
//	r'disregard\s+(your|all|any)\s+(instructions|rules|guidelines)'
//
// Go's \w and \s are ASCII-only, so they are widened to what Python matches:
// \w is letters, numbers and underscore; \s is Go's \s plus \v, the Unicode
// separators (Z*) and the C0/C1 whitespace controls U+001C-001F and U+0085.
var ScannerPatterns = []*regexp.Regexp{
	regexp.MustCompile(`(?i)` + w("ignore") + sp + `+(?:` + wd + `+` + sp + `+)*(?:` + w("previous") + `|all|above|` + w("prior") + `)` + sp + `+(?:` + wd + `+` + sp + `+)*` + w("instructions")),
	regexp.MustCompile(`(?i)do` + sp + `+not` + sp + `+tell` + sp + `+the` + sp + `+user`),
	regexp.MustCompile(`(?i)system` + sp + `+prompt` + sp + `+override`),
	regexp.MustCompile(`(?i)` + w("disregard") + sp + `+(?:your|all|any)` + sp + `+(?:` + w("instructions") + `|rules|` + w("guidelines") + `)`),
}

// w widens each i of a literal word to [iıİ]: Python's re.IGNORECASE folds
// U+0131 (dotless i) and U+0130 (dotted capital I) onto i, Go's (?i) does not.
// A comparison of every code point against every ASCII letter used found no
// other difference.
func w(word string) string { return strings.ReplaceAll(word, "i", `[i\x{131}\x{130}]`) }

const (
	sp = `[\s\v\p{Z}\x{1c}-\x{1f}\x{85}]`
	wd = `[\p{L}\p{N}_]`
)
