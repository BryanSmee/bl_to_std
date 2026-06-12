package paint

import (
	"bufio"
	"os"
	"testing"
)

// Known simple encodings: an unsplit triangle fully painted with filament N
// (state N). Derived from BambuStudio's TriangleSelector/FacetsAnnotation
// serialization.
var simpleStates = map[string]int{
	"4":  1,
	"8":  2,
	"0C": 3,
	"1C": 4,
	"2C": 5,
	"3C": 6,
	"DC": 16,
}

func TestDecodeSimpleStates(t *testing.T) {
	for s, want := range simpleStates {
		n, err := Decode(s)
		if err != nil {
			t.Fatalf("Decode(%q): %v", s, err)
		}
		if n.SplitSides != 0 || n.State != want {
			t.Errorf("Decode(%q) = %+v, want leaf state %d", s, n, want)
		}
		if got := n.Encode(); got != s {
			t.Errorf("Encode(Decode(%q)) = %q", s, got)
		}
	}
}

func TestDecodeSplit(t *testing.T) {
	// "4882": 2-side split, special side 0, children: states 2, 2, 1.
	n, err := Decode("4882")
	if err != nil {
		t.Fatal(err)
	}
	if n.SplitSides != 2 || len(n.Children) != 3 {
		t.Fatalf("unexpected tree: %+v", n)
	}
	states := []int{n.Children[0].State, n.Children[1].State, n.Children[2].State}
	if states[0] != 2 || states[1] != 2 || states[2] != 1 {
		t.Errorf("child states = %v, want [2 2 1]", states)
	}
}

func TestExtendedStateRoundTrip(t *testing.T) {
	for state := 0; state <= 40; state++ {
		n := Node{State: state}
		enc := n.Encode()
		dec, err := Decode(enc)
		if err != nil {
			t.Fatalf("state %d (%q): %v", state, enc, err)
		}
		if dec.State != state {
			t.Errorf("state %d: round-trip gave %d (%q)", state, dec.State, enc)
		}
	}
}

func TestRemap(t *testing.T) {
	m := map[int]int{1: 2, 2: 1, 3: 1, 4: 3, 5: 4}
	fn := func(f int) int { return m[f] }

	got, err := Remap("4", fn) // filament 1 -> 2
	if err != nil {
		t.Fatal(err)
	}
	if got != "8" {
		t.Errorf("Remap(4) = %q, want 8", got)
	}

	// Split node: children states 2,2,1 -> 1,1,2
	got, err = Remap("4882", fn)
	if err != nil {
		t.Fatal(err)
	}
	n, err := Decode(got)
	if err != nil {
		t.Fatal(err)
	}
	states := []int{n.Children[0].State, n.Children[1].State, n.Children[2].State}
	if states[0] != 1 || states[1] != 1 || states[2] != 2 {
		t.Errorf("remapped child states = %v, want [1 1 2]", states)
	}
}

// TestCorpusRoundTrip decodes and re-encodes every unique paint_color value
// extracted from a real painted multi-color model and requires exact
// byte-for-byte reproduction.
func TestCorpusRoundTrip(t *testing.T) {
	f, err := os.Open("testdata/paint_samples.txt")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	count := 0
	sc := bufio.NewScanner(f)
	sc.Buffer(make([]byte, 0, 64*1024), 64*1024)
	for sc.Scan() {
		s := sc.Text()
		if s == "" {
			continue
		}
		n, err := Decode(s)
		if err != nil {
			t.Fatalf("Decode(%q): %v", s, err)
		}
		if got := n.Encode(); got != s {
			t.Fatalf("round-trip mismatch: %q -> %q", s, got)
		}
		count++
	}
	if err := sc.Err(); err != nil {
		t.Fatal(err)
	}
	if count < 5000 {
		t.Fatalf("corpus too small: %d samples", count)
	}
	t.Logf("round-tripped %d paint strings", count)
}
