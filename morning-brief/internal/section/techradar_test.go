package section

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

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

func TestTechRadarUsesLocalDate(t *testing.T) {
	vault := t.TempDir()
	// 2026-11-25 23:30 UTC is already 2026-11-26 in Paris
	rel := "Tech/Tech Radar/Dailies/2026/11/2026-11-26.md"
	p := filepath.Join(vault, rel)
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p, []byte("x"), 0o644); err != nil {
		t.Fatal(err)
	}
	loc, err := time.LoadLocation("Europe/Paris")
	if err != nil {
		t.Skip("no tzdata")
	}
	env := b11Env(t, nil, brief.Sources{VaultDir: vault})
	env.Now = time.Date(2026, 11, 25, 23, 30, 0, 0, time.UTC)
	env.Loc = loc
	r := b11Section(t, "tech_radar").Collect(t.Context(), env, techRadarDefault())
	if v, _ := factVal(r.Facts, "today's note"); v != rel {
		t.Fatalf("got %+v", r)
	}
	env.Loc = time.UTC
	if r := b11Section(t, "tech_radar").Collect(t.Context(), env, techRadarDefault()); r.Status != brief.StatusOK {
		t.Fatalf("UTC date has no note, got %+v", r)
	}
}

func TestTechRadarStatErrorIsError(t *testing.T) {
	vault := t.TempDir()
	if err := os.WriteFile(filepath.Join(vault, "Tech"), []byte("a file where a directory should be"), 0o644); err != nil {
		t.Fatal(err)
	}
	env := b11Env(t, nil, brief.Sources{VaultDir: vault})
	if r := b11Section(t, "tech_radar").Collect(t.Context(), env, techRadarDefault()); r.Status != brief.StatusError {
		t.Fatalf("status %s", r.Status)
	}
}

// A vault that is not there (volume not mounted) is an error, never "no note yet".
func TestTechRadarVaultMissingIsError(t *testing.T) {
	for _, vault := range []string{filepath.Join(t.TempDir(), "nope"), writeTemp(t)} {
		env := b11Env(t, nil, brief.Sources{VaultDir: vault})
		r := b11Section(t, "tech_radar").Collect(t.Context(), env, techRadarDefault())
		if r.Status != brief.StatusError || !strings.Contains(r.Err, "vault") {
			t.Errorf("%s: status %s err %q", vault, r.Status, r.Err)
		}
	}
}

// writeTemp returns a regular file, standing for a vault path that is not a directory.
func writeTemp(t *testing.T) string {
	p := filepath.Join(t.TempDir(), "file")
	if err := os.WriteFile(p, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	return p
}
