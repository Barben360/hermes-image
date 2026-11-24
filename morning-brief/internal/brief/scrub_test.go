package brief

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestScrubRemovesEveryReadSecret(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "x"), []byte("s3cr3t-value\n"), 0o600)
	os.WriteFile(filepath.Join(dir, "y"), []byte("other-secret \n"), 0o600)
	s := NewSecrets(dir)

	// nothing read yet: nothing scrubbed
	if got := s.Scrub("auth failed: s3cr3t-value"); got != "auth failed: s3cr3t-value" {
		t.Fatalf("scrubbed before read: %q", got)
	}
	v, err := s.Read("x")
	if err != nil || v != "s3cr3t-value" {
		t.Fatalf("Read = %q, %v", v, err)
	}
	if _, err := s.Read("y"); err != nil {
		t.Fatal(err)
	}
	if got := s.Scrub("auth failed: s3cr3t-value"); got != "auth failed: [redacted]" {
		t.Errorf("got %q", got)
	}
	got := s.Scrub(`Get "https://hc-ping.com/s3cr3t-value/fail": EOF`)
	if strings.Contains(got, "s3cr3t-value") {
		t.Errorf("leak: %q", got)
	}
	if got := s.Scrub("a other-secret b s3cr3t-value"); got != "a [redacted] b [redacted]" {
		t.Errorf("got %q", got)
	}
}

func TestScrubOverlappingSecretsLongestFirst(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "a"), []byte("abc"), 0o600)
	os.WriteFile(filepath.Join(dir, "b"), []byte("abcdef"), 0o600)
	s := NewSecrets(dir)
	s.Read("a")
	s.Read("b")
	if got := s.Scrub("x abcdef y"); got != "x [redacted] y" {
		t.Errorf("got %q", got)
	}
}

func TestReadMissingFileErrorsWithPathNoValue(t *testing.T) {
	dir := t.TempDir()
	s := NewSecrets(dir)
	_, err := s.Read("nope")
	if err == nil || !strings.Contains(err.Error(), "nope") {
		t.Fatalf("err = %v", err)
	}
}

func TestReadErrorNeverHoldsValue(t *testing.T) {
	dir := t.TempDir()
	os.WriteFile(filepath.Join(dir, "x"), []byte("s3cr3t-value"), 0o600)
	s := NewSecrets(dir)
	if _, err := s.Read("../x"); err == nil || strings.Contains(err.Error(), "s3cr3t-value") {
		t.Errorf("path escape must fail without value: %v", err)
	}
	os.WriteFile(filepath.Join(dir, "empty"), []byte("\n"), 0o600)
	s.Scrub("")
	if got := s.Scrub("plain text"); got != "plain text" {
		t.Errorf("empty secret must not redact everything: %q", got)
	}
	s.Read("empty")
	if got := s.Scrub("plain text"); got != "plain text" {
		t.Errorf("empty secret must not redact everything: %q", got)
	}
}

func TestNilSecretsScrubIsIdentity(t *testing.T) {
	var s *Secrets
	if s.Scrub("abc") != "abc" {
		t.Fail()
	}
}
