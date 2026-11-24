package main

import (
	"bytes"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const wantPrefix = "⚠️ Le brief du matin n'a pas été livré : "

// runWatch runs `watch` against testdata/cron/jobs-<fixture>.json at 07:45 on 2026-11-24.
func runWatch(t *testing.T, fixture string, extra ...string) (int, string, string) {
	t.Helper()
	dir := t.TempDir()
	cfg := filepath.Join(dir, "config.yaml")
	jobs, _ := filepath.Abs(filepath.Join("..", "..", "testdata", "cron", "jobs-"+fixture+".json"))
	if err := os.WriteFile(cfg, []byte("sources:\n  cron_jobs: "+jobs+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "some_token"), []byte("abc-SECRET-1234\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	args := append([]string{"watch", "--config", cfg, "--secrets-dir", dir, "--now", "2026-11-24T07:45:00+01:00"}, extra...)
	var out, errb bytes.Buffer
	code := realMain(args, &out, &errb)
	return code, out.String(), errb.String()
}

func wantLine(t *testing.T, fixture, contains string, extra ...string) {
	t.Helper()
	code, out, errb := runWatch(t, fixture, extra...)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb)
	}
	if !strings.HasPrefix(out, wantPrefix) || strings.Count(out, "\n") != 1 || !strings.Contains(out, contains) {
		t.Errorf("want one line with %q, got %q", contains, out)
	}
}

func TestWatchSilentOnDeliveredRun(t *testing.T) {
	code, out, errb := runWatch(t, "delivered")
	if code != 0 || out != "" {
		t.Errorf("exit %d stdout %q stderr %q", code, out, errb)
	}
}

func TestWatchFailsOnUndeliveredRun(t *testing.T) {
	wantLine(t, "undelivered", "send failed")
	_, out, _ := runWatch(t, "undelivered")
	if strings.Contains(out, "abc-SECRET-1234") {
		t.Errorf("secret printed: %q", out)
	}
}

func TestWatchFailsOnBlockedRun(t *testing.T) {
	wantLine(t, "blocked", "error")
	_, out, _ := runWatch(t, "blocked")
	if strings.Contains(strings.ToLower(out), "ignore all previous") {
		t.Errorf("injection phrase printed: %q", out)
	}
}

func TestWatchFailsOnYesterdaysRun(t *testing.T)  { wantLine(t, "yesterday", "aujourd'hui") }
func TestWatchFailsOnRunBeforeSeven(t *testing.T) { wantLine(t, "early", "07:00") }

// TestWatchAfterMovesTheMorning: a brief moved earlier needs --after; the early fixture ran at 06:30.
func TestWatchAfterMovesTheMorning(t *testing.T) {
	code, out, _ := runWatch(t, "early", "--after", "06:30")
	if code != 0 || out != "" {
		t.Errorf("06:30 run with --after 06:30: exit %d %q", code, out)
	}
	wantLine(t, "early", "avant 06:31", "--after", "06:31")
	wantLine(t, "early", "--after", "--after", "7h")
}

func TestWatchFailsOnNeverRun(t *testing.T)  { wantLine(t, "never", "jamais") }
func TestWatchFailsOnPausedJob(t *testing.T) { wantLine(t, "paused", "désactivé") }
func TestWatchFailsOnUnverifiedDelivery(t *testing.T) {
	wantLine(t, "unverified", "non vérifiée")
}

func TestWatchJobMissing(t *testing.T) {
	wantLine(t, "delivered", "introuvable", "--job", "nope")
}

func TestWatchFailsOnBadShapeOrMissingFile(t *testing.T) {
	wantLine(t, "badshape", "jobs.json")
	wantLine(t, "noshape", "jobs.json")
	wantLine(t, "does-not-exist", "jobs.json")
}

func TestWatchFailsOnRunningQueuedClaimedOrNoEnabled(t *testing.T) {
	wantLine(t, "running", "en cours")
	wantLine(t, "claimed", "en cours")
	wantLine(t, "queued", "livraison en attente")
	wantLine(t, "noenabled", "désactivé")
}

func TestWatchBadFlagOrNowStillExitsZeroWithLine(t *testing.T) {
	wantLine(t, "delivered", "--now", "--now", "garbage")
	wantLine(t, "delivered", "option", "--bogus")
}
