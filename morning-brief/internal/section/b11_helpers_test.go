package section

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/Barben360/hermes-image/morning-brief/internal/brief"
)

func b11Env(t *testing.T, secrets map[string]string, src brief.Sources) *brief.Env {
	t.Helper()
	dir := t.TempDir()
	for n, v := range secrets {
		if err := os.WriteFile(filepath.Join(dir, n), []byte(v+"\n"), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	return &brief.Env{
		Now:     time.Date(2026, 11, 25, 7, 15, 0, 0, time.UTC),
		Loc:     time.UTC,
		HTTP:    http.DefaultClient,
		Secrets: brief.NewSecrets(dir),
		Sources: src,
	}
}

func b11Section(t *testing.T, name string) brief.Section {
	t.Helper()
	for _, s := range brief.Sections() {
		if s.Name == name {
			return s
		}
	}
	t.Fatalf("section %q not registered", name)
	return brief.Section{}
}

func factVal(fs []brief.Fact, key string) (string, bool) {
	for _, f := range fs {
		if f.Key == key {
			return f.Value, true
		}
	}
	return "", false
}

func itemsText(r brief.Result) string {
	var b strings.Builder
	for _, it := range r.Items {
		for _, f := range it.Facts {
			b.WriteString(f.Key + "=" + f.Value + " ")
		}
		b.WriteString("\n")
	}
	return b.String()
}
