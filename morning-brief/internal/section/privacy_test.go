// Fixtures privacy scan: Go ignores testdata/ in ./..., so the scan lives here and walks ../../testdata.
package section

import (
	"encoding/json"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
)

const (
	fakeLatF = 45.0
	fakeLonF = 5.0
)

var privateText = regexp.MustCompile(`@proton\.me|@bbarrois\.tech|Barrois`)

// walkCoords reports every number held under a latitude or longitude key.
func walkCoords(v any, key string, bad func(key string, n float64)) {
	switch x := v.(type) {
	case map[string]any:
		for k, c := range x {
			walkCoords(c, strings.ToLower(k), bad)
		}
	case []any:
		for _, c := range x {
			walkCoords(c, key, bad)
		}
	case float64:
		if key == "latitude" && x != fakeLatF || key == "longitude" && x != fakeLonF {
			bad(key, x)
		}
	}
}

func TestFixturesHoldNoPrivateData(t *testing.T) {
	n := 0
	err := filepath.WalkDir("../../testdata", func(p string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || strings.HasSuffix(p, ".go") {
			return err
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return err
		}
		n++
		if m := privateText.Find(b); m != nil {
			t.Errorf("%s: private text %q", p, m)
		}
		var v any
		if json.Unmarshal(b, &v) == nil {
			walkCoords(v, "", func(k string, f float64) { t.Errorf("%s: %s = %v is not the fake constant", p, k, f) })
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	if n == 0 {
		t.Fatal("no fixture scanned")
	}
}

func TestScannerCatchesPrivateData(t *testing.T) {
	var got []string
	var v any
	_ = json.Unmarshal([]byte(`{"a":[{"latitude":48.85,"Longitude":2.3},{"latitude":45.0,"longitude":5.0}]}`), &v)
	walkCoords(v, "", func(k string, f float64) { got = append(got, k) })
	if len(got) != 2 {
		t.Errorf("got %v, want latitude and longitude flagged once each", got)
	}
	if !privateText.MatchString("x@bbarrois.tech") || !privateText.MatchString("Jean Barrois") || privateText.MatchString("alex@example.com") {
		t.Error("text pattern wrong")
	}
}
