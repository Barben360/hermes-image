package section

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/Barben360/hermes-image/morning-brief/internal/brief"
)

func TestTechRadarNote(t *testing.T) {
	vault := t.TempDir()
	env := b11Env(t, nil, brief.Sources{VaultDir: vault})
	s := b11Section(t, "tech_radar")

	r := s.Collect(t.Context(), env, techRadarDefault())
	if r.Status != brief.StatusOK {
		t.Fatalf("absent: status %s", r.Status)
	}
	if v, _ := factVal(r.Facts, "note"); v != "no note yet" {
		t.Fatalf("absent: %q", v)
	}

	rel := "Tech/Tech Radar/Dailies/2026/11/2026-11-25.md"
	p := filepath.Join(vault, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	r = s.Collect(t.Context(), env, techRadarDefault())
	v, _ := factVal(r.Facts, "today's note")
	if r.Status != brief.StatusReport || v != rel {
		t.Fatalf("present: %s %q", r.Status, v)
	}
	if strings.Contains(v, vault) {
		t.Fatal("absolute path leaked")
	}
}

func TestTechRadarNoteIsAFileNotADirectory(t *testing.T) {
	vault := t.TempDir()
	if err := os.MkdirAll(filepath.Join(vault, "Tech/Tech Radar/Dailies/2026/11/2026-11-25.md"), 0o755); err != nil {
		t.Fatal(err)
	}
	env := b11Env(t, nil, brief.Sources{VaultDir: vault})
	r := b11Section(t, "tech_radar").Collect(t.Context(), env, techRadarDefault())
	if r.Status != brief.StatusOK {
		t.Fatalf("directory counted as note: %s", r.Status)
	}
}

func TestB11ConfigsValidate(t *testing.T) {
	g := githubDefault()
	g.Owner = ""
	if g.Validate() == nil || !strings.Contains(g.Validate().Error(), "github.owner") {
		t.Fatal("empty owner must fail with a set hint")
	}
	if githubDefault().Validate() != nil || deepseekDefault().Validate() != nil ||
		bugsinkDefault().Validate() != nil || techRadarDefault().Validate() != nil {
		t.Fatal("defaults must validate")
	}
	h := githubDefault()
	h.Hours = 0
	if h.Validate() == nil {
		t.Fatal("hours 0 must fail")
	}
}
