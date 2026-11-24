package brief

import "testing"

func TestUseSectionsForTestSwapsAndRestores(t *testing.T) {
	Register(Section{Name: "base-registry-test"})
	before := Sections()
	restore := UseSectionsForTest(Section{Name: "only"})
	if got := Sections(); len(got) != 1 || got[0].Name != "only" {
		t.Fatalf("swap: %v", got)
	}
	Register(Section{Name: "added-while-swapped"})
	restore()
	after := Sections()
	if len(after) != len(before) || after[len(after)-1].Name != "base-registry-test" {
		t.Fatalf("restore: before %d after %v", len(before), after)
	}
}
