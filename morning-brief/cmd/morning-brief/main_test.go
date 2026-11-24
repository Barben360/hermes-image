package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestRunPrintsBriefAndExitsZero(t *testing.T) {
	var out, errb bytes.Buffer
	code := realMain([]string{"run", "--secrets-dir", t.TempDir(), "--now", "2026-11-25T07:15:00+01:00"}, &out, &errb)
	if code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if !strings.HasPrefix(out.String(), "# morning-brief 2026-11-25T07:15+01:00 Europe/Paris\n") ||
		!strings.Contains(out.String(), "## summary:") {
		t.Errorf("%s", out.String())
	}
}

func TestSectionsPrintsYAML(t *testing.T) {
	var out, errb bytes.Buffer
	if code := realMain([]string{"sections"}, &out, &errb); code != 0 {
		t.Fatalf("exit %d: %s", code, errb.String())
	}
	if !strings.HasPrefix(out.String(), "sections:") {
		t.Errorf("%s", out.String())
	}
}

func TestBadFormatAndCommand(t *testing.T) {
	var out, errb bytes.Buffer
	if realMain([]string{"run", "--format", "xml"}, &out, &errb) == 0 || realMain([]string{"nope"}, &out, &errb) == 0 {
		t.Error("want non-zero")
	}
}
