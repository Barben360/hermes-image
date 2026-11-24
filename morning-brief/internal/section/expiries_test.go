package section

import (
	"bytes"
	"context"
	"strings"
	"testing"

	"github.com/Barben360/hermes-image/morning-brief/internal/brief"
)

func expCfg(files ...string) *ExpiriesConfig {
	c := brief.Sections()[indexOf("expiries")].Default().(*ExpiriesConfig)
	c.Files = files
	return c
}

// Test env: now = 2026-11-25 07:15 Paris.
func TestExpiriesWithin30Days(t *testing.T) {
	env := testEnv(t, "", map[string]string{
		"near":  "2026-12-24\n", // 29 days, trailing newline
		"far":   "2026-12-26",   // 31 days
		"today": "2026-11-25",
		"past":  "2026-11-20",
	})
	r := collectExpiries(context.Background(), env, expCfg("near", "far", "today", "past"))
	if r.Status != brief.StatusReport || len(r.Items) != 3 {
		t.Fatalf("status %s items %d err %q", r.Status, len(r.Items), r.Err)
	}
	got := map[string]string{}
	for _, it := range r.Items {
		got[it.Facts[0].Key] = it.Facts[0].Value
	}
	if got["near"] != "in 29 days" || got["today"] != "expires today" ||
		got["past"] != "expired 5 days ago" {
		t.Fatalf("%v", got)
	}
	if _, ok := got["far"]; ok {
		t.Fatal("31 days must not be reported")
	}
}

func TestExpiriesBoundaryIs30Days(t *testing.T) {
	env := testEnv(t, "", map[string]string{"edge": "2026-12-25"})
	if r := collectExpiries(context.Background(), env, expCfg("edge")); r.Status != brief.StatusReport {
		t.Fatalf("30 days must be reported, got %s", r.Status)
	}
}

func TestExpiriesAllFarIsOK(t *testing.T) {
	env := testEnv(t, "", map[string]string{"far": "2026-12-26"})
	if r := collectExpiries(context.Background(), env, expCfg("far")); r.Status != brief.StatusOK {
		t.Fatalf("%s", r.Status)
	}
}

func TestExpiriesBadDateNamesFile(t *testing.T) {
	env := testEnv(t, "", map[string]string{"gh": "2026-13-01"})
	r := collectExpiries(context.Background(), env, expCfg("gh"))
	if r.Status != brief.StatusError || !strings.Contains(r.Err, "gh") {
		t.Fatalf("%s %q", r.Status, r.Err)
	}
}

func TestExpiriesMissingFileIsError(t *testing.T) {
	r := collectExpiries(context.Background(), testEnv(t, "", nil), expCfg("gone"))
	if r.Status != brief.StatusError || !strings.Contains(r.Err, "gone") {
		t.Fatalf("%s %q", r.Status, r.Err)
	}
}

func TestExpiriesSurvivesScrubbedRender(t *testing.T) {
	env := testEnv(t, "", map[string]string{"near": "2026-12-24\n"})
	r := collectExpiries(context.Background(), env, expCfg("near", "near"))
	if len(r.Items) != 1 {
		t.Fatalf("duplicate file names must collapse, items %d", len(r.Items))
	}
	var out bytes.Buffer
	if err := brief.RenderText(&out, brief.Doc{Now: env.Now, Results: []brief.Result{r}}, env.Secrets); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "near: in 29 days") || strings.Contains(out.String(), "[redacted]") {
		t.Fatalf("%s", out.String())
	}
}
