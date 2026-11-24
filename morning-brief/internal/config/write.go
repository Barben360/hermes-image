package config

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"

	"github.com/Barben360/hermes-image/morning-brief/internal/brief"
)

// readRaw returns the owner's file as a generic map; a missing file is empty, an unparsable one an error.
func readRaw(path string) (map[string]any, []byte, error) {
	b, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return map[string]any{}, nil, nil
	}
	if err != nil {
		return nil, nil, err
	}
	m := map[string]any{}
	if err := yaml.Unmarshal(b, &m); err != nil {
		return nil, nil, fmt.Errorf("%s is not valid YAML, fix or remove it first: %s", path, oneLine(err.Error()))
	}
	if m == nil {
		m = map[string]any{}
	}
	for k := range m {
		if !identRe.MatchString(k) {
			return nil, nil, fmt.Errorf("%s is not a morning-brief config (key %q is not a name), fix or remove it first", path, k)
		}
	}
	return m, b, nil
}

func splitDotted(dotted string) (section, key string, err error) {
	section, key, ok := strings.Cut(dotted, ".")
	if !ok || section == "" || key == "" {
		return "", "", fmt.Errorf("%q: want <section>.<key>", dotted)
	}
	return section, key, nil
}

func checkTarget(section string, sections []brief.Section) error {
	if section == "writer" || section == "sources" {
		return nil
	}
	for _, s := range sections {
		if s.Name == section {
			return nil
		}
	}
	return fmt.Errorf("unknown section %q", section)
}

// problem returns the error of the part of the configuration a change touches.
func problem(r result, section string) string {
	switch section {
	case "writer":
		return r.writerErr
	case "sources":
		return r.sourcesErr
	}
	return r.Plan.ConfigErrors[section]
}

func tryApply(path string, raw map[string]any, old []byte, sections []brief.Section, touched ...string) error {
	nb, err := yaml.Marshal(raw)
	if err != nil {
		return err
	}
	r := loadBytes(nb, path, sections)
	for _, section := range touched {
		if p := problem(r, section); p != "" {
			return errors.New(p)
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return writeAtomic(path, old, nb)
}

// Pair is one dotted key and its value, read as YAML.
type Pair struct{ Key, Value string }

// Set changes one key of the owner's file after validating the result; the previous file is kept as path+".prev".
func Set(path string, sections []brief.Section, dotted, value string) error {
	return SetMany(path, sections, []Pair{{dotted, value}})
}

// SetMany applies every pair together and validates once: all or nothing, the error names the bad key.
func SetMany(path string, sections []brief.Section, pairs []Pair) error {
	if len(pairs) == 0 {
		return errors.New("nothing to set")
	}
	type target struct{ section, key string }
	targets := make([]target, len(pairs))
	var touched []string
	seen := map[string]bool{}
	for i, p := range pairs {
		section, key, err := splitDotted(p.Key)
		if err != nil {
			return err
		}
		if err := checkTarget(section, sections); err != nil {
			return fmt.Errorf("%s: %w", p.Key, err)
		}
		targets[i] = target{section, key}
		if !seen[section] {
			seen[section] = true
			touched = append(touched, section)
		}
	}
	// First every value read as YAML, then (for a value that is not valid YAML or
	// is refused as typed) every value as a plain string.
	asYAML := func(v string) any {
		var parsed any
		if yaml.Unmarshal([]byte(v), &parsed) == nil {
			return parsed
		}
		return v
	}
	var firstErr error
	for attempt := 0; attempt < 2; attempt++ {
		raw, old, err := readRaw(path)
		if err != nil {
			return err
		}
		for i, p := range pairs {
			t := targets[i]
			sec, _ := raw[t.section].(map[string]any)
			if sec == nil {
				if raw[t.section] != nil {
					return fmt.Errorf("%s in %s is not a mapping, fix it first", t.section, path)
				}
				sec = map[string]any{}
			}
			if attempt == 0 {
				sec[t.key] = asYAML(p.Value)
			} else {
				sec[t.key] = p.Value
			}
			raw[t.section] = sec
		}
		err = tryApply(path, raw, old, sections, touched...)
		if err == nil {
			return nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}
	return firstErr
}

// Unset removes one key of the owner's file, so the default applies again.
func Unset(path string, sections []brief.Section, dotted string) error {
	section, key, err := splitDotted(dotted)
	if err != nil {
		return err
	}
	if err := checkTarget(section, sections); err != nil {
		return err
	}
	raw, old, err := readRaw(path)
	if err != nil {
		return err
	}
	sec, _ := raw[section].(map[string]any)
	if _, set := sec[key]; !set {
		return fmt.Errorf("%s is not set in %s", dotted, path)
	}
	delete(sec, key)
	if len(sec) == 0 {
		delete(raw, section)
	}
	return tryApply(path, raw, old, sections, section)
}

// writeAtomic keeps the current content as path+".prev", then replaces path through a rename.
func writeAtomic(path string, old, content []byte) error {
	if old != nil {
		if err := atomicFile(path+".prev", old); err != nil {
			return err
		}
	}
	return atomicFile(path, content)
}

func atomicFile(path string, content []byte) error {
	tmp, err := os.CreateTemp(filepath.Dir(path), ".morning-brief-*")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name) // no-op after a successful rename
	if _, err := tmp.Write(content); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return os.Rename(name, path)
}
