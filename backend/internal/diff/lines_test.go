package diff

import (
	"math/rand/v2"
	"strings"
	"testing"
)

// Replaying the edit script on the old text must give the new one, whatever the input.
func TestEditScriptRebuildsNewText(t *testing.T) {
	r := rand.New(rand.NewPCG(1, 2))
	words := []string{"a", "b", "c", "d", "e"}
	gen := func() []string {
		out := make([]string, r.IntN(40))
		for i := range out {
			out[i] = words[r.IntN(len(words))]
		}
		return out
	}
	for range 500 {
		a, b := gen(), gen()
		ops, ok := editScript(a, b)
		if !ok {
			t.Fatal("edit distance should be small")
		}
		var gotOld, gotNew []string
		for _, o := range ops {
			if o.Op != "+" {
				gotOld = append(gotOld, o.Text)
			}
			if o.Op != "-" {
				gotNew = append(gotNew, o.Text)
			}
		}
		if strings.Join(gotOld, ",") != strings.Join(a, ",") || strings.Join(gotNew, ",") != strings.Join(b, ",") {
			t.Fatalf("a=%v b=%v ops=%+v", a, b, ops)
		}
	}
}

func TestLineDiffHunks(t *testing.T) {
	var old []string
	for i := range 100 {
		old = append(old, "line "+string(rune('A'+i%26)))
	}
	cur := append([]string{}, old...)
	cur[10] = "changed"
	cur = append(cur[:60], append([]string{"inserted"}, cur[60:]...)...)
	td := LineDiff(old, cur, 3)
	if td.Added != 2 || td.Removed != 1 || len(td.Hunks) != 2 {
		t.Fatalf("diff = %+v", td)
	}
	h := td.Hunks[0]
	if h.OldStart != 8 || h.NewStart != 8 || len(h.Lines) != 8 {
		t.Errorf("hunk 1 = %+v", h)
	}
	if h.Lines[3].Op != "-" || h.Lines[3].Old != 11 || h.Lines[4].Op != "+" || h.Lines[4].New != 11 {
		t.Errorf("lines = %+v", h.Lines)
	}
	if td := LineDiff(old, old, 3); td.Added+td.Removed != 0 || len(td.Hunks) != 0 {
		t.Errorf("identical texts: %+v", td)
	}
	// completely different large texts: bounded, reported as too large
	var x, y []string
	for i := range 3000 {
		x = append(x, "x"+string(rune(i)))
		y = append(y, "y"+string(rune(i)))
	}
	if td := LineDiff(x, y, 3); !td.TooLarge {
		t.Errorf("expected too_large, got %d hunks", len(td.Hunks))
	}
}
