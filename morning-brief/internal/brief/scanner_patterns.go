package brief

import "regexp"

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
	regexp.MustCompile(`(?i)ignore` + sp + `+(?:` + wd + `+` + sp + `+)*(?:previous|all|above|prior)` + sp + `+(?:` + wd + `+` + sp + `+)*instructions`),
	regexp.MustCompile(`(?i)do` + sp + `+not` + sp + `+tell` + sp + `+the` + sp + `+user`),
	regexp.MustCompile(`(?i)system` + sp + `+prompt` + sp + `+override`),
	regexp.MustCompile(`(?i)disregard` + sp + `+(?:your|all|any)` + sp + `+(?:instructions|rules|guidelines)`),
}

const (
	sp = `[\s\v\p{Z}\x{1c}-\x{1f}\x{85}]`
	wd = `[\p{L}\p{N}_]`
)
