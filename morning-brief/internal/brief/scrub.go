package brief

import (
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
)

// Secrets reads credential files and remembers every value to scrub it from any printed string.
type Secrets struct {
	dir  string
	mu   sync.Mutex
	vals []string
}

// NewSecrets reads files from dir.
func NewSecrets(dir string) *Secrets { return &Secrets{dir: dir} }

// Read returns the file's content without trailing whitespace. Errors name the file, never a value.
func (s *Secrets) Read(name string) (string, error) {
	if s == nil {
		return "", fmt.Errorf("secret file %q: no secrets directory", name)
	}
	if name == "" || name != filepath.Base(name) || name == "." || name == ".." {
		return "", fmt.Errorf("secret file %q: invalid name", name)
	}
	path := filepath.Join(s.dir, name)
	b, err := os.ReadFile(path)
	if err != nil {
		reason := "not readable"
		if os.IsNotExist(err) {
			reason = "not found"
		}
		return "", fmt.Errorf("secret file %s: %s", path, reason)
	}
	v := strings.TrimRight(string(b), " \t\r\n")
	if v != "" {
		s.mu.Lock()
		s.vals = append(s.vals, v)
		s.mu.Unlock()
	}
	return v, nil
}

// Scrub replaces every value read so far with [redacted].
func (s *Secrets) Scrub(in string) string {
	if s == nil {
		return in
	}
	s.mu.Lock()
	vals := append([]string(nil), s.vals...)
	s.mu.Unlock()
	sort.Slice(vals, func(i, j int) bool { return len(vals[i]) > len(vals[j]) })
	for _, v := range vals {
		in = strings.ReplaceAll(in, v, "[redacted]")
	}
	return in
}
