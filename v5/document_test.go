package jsonpatch

import (
	"fmt"
	"strconv"
	"testing"
)

// TestApplyToDocumentEquivalence asserts that for every happy-path Case,
// the new ParseDocument+ApplyToDocument+Marshal path produces the same
// (JSON-equivalent) result as the legacy Apply path.
func TestApplyToDocumentEquivalence(t *testing.T) {
	defer configureGlobals(int64(100))()

	for i, c := range Cases {
		t.Run(fmt.Sprintf("Case %d", i), func(t *testing.T) {
			if c.doc == "" {
				// ParseDocument requires non-empty input; the legacy
				// Apply path also short-circuits on empty doc.
				return
			}
			opts := NewApplyOptions()
			opts.AllowMissingPathOnRemove = c.allowMissingPathOnRemove
			opts.EnsurePathExistsOnAdd = c.ensurePathExistsOnAdd

			patch, err := DecodePatch([]byte(c.patch))
			if err != nil {
				t.Fatalf("DecodePatch: %v", err)
			}

			doc, err := ParseDocument([]byte(c.doc), opts)
			if err != nil {
				t.Fatalf("ParseDocument: %v", err)
			}
			if err := patch.ApplyToDocument(doc, opts); err != nil {
				t.Fatalf("ApplyToDocument: %v", err)
			}
			out, err := doc.Marshal()
			if err != nil {
				t.Fatalf("Marshal: %v", err)
			}
			if !compareJSON(string(out), c.result) {
				t.Errorf("Mismatch.\nExpected:\n%s\n\nGot:\n%s",
					reformatJSON(c.result), reformatJSON(string(out)))
			}
		})
	}
}

// TestApplyToDocumentChained verifies that applying N patches to a single
// Document yields the same result as Apply'ing them one-by-one with
// intermediate marshalling.
func TestApplyToDocumentChained(t *testing.T) {
	doc := []byte(`{"items":[]}`)

	patches := make([]Patch, 0, 20)
	for i := 0; i < 20; i++ {
		raw := []byte(`[{"op":"add","path":"/items/-","value":` + strconv.Itoa(i) + `}]`)
		p, err := DecodePatch(raw)
		if err != nil {
			t.Fatalf("DecodePatch %d: %v", i, err)
		}
		patches = append(patches, p)
	}

	legacy := append([]byte(nil), doc...)
	for i, p := range patches {
		out, err := p.Apply(legacy)
		if err != nil {
			t.Fatalf("legacy Apply %d: %v", i, err)
		}
		legacy = out
	}

	d, err := ParseDocument(doc, nil)
	if err != nil {
		t.Fatalf("ParseDocument: %v", err)
	}
	for i, p := range patches {
		if err := p.ApplyToDocument(d, nil); err != nil {
			t.Fatalf("ApplyToDocument %d: %v", i, err)
		}
	}
	out, err := d.Marshal()
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	if !compareJSON(string(out), string(legacy)) {
		t.Errorf("Chained result diverges from legacy.\nLegacy:\n%s\n\nNew:\n%s",
			reformatJSON(string(legacy)), reformatJSON(string(out)))
	}
}

// TestApplyToDocumentGenesisReset verifies that callers can discard a
// Document and create a new one mid-replay (matching how a snapshot or
// mid-chain Genesis resets state in a history replay).
func TestApplyToDocumentGenesisReset(t *testing.T) {
	doc1 := []byte(`{"a":1}`)
	doc2 := []byte(`{"b":2}`)

	p, err := DecodePatch([]byte(`[{"op":"add","path":"/x","value":42}]`))
	if err != nil {
		t.Fatal(err)
	}

	d, err := ParseDocument(doc1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.ApplyToDocument(d, nil); err != nil {
		t.Fatal(err)
	}

	d, err = ParseDocument(doc2, nil)
	if err != nil {
		t.Fatal(err)
	}
	if err := p.ApplyToDocument(d, nil); err != nil {
		t.Fatal(err)
	}
	out, err := d.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if !compareJSON(string(out), `{"b":2,"x":42}`) {
		t.Errorf("got %s", string(out))
	}
}

// TestApplyToDocumentNilOptsInheritsParse confirms that passing nil opts to
// ApplyToDocument inherits the options the Document was created with
// (specifically EnsurePathExistsOnAdd, which would fail on a missing
// intermediate path otherwise).
func TestApplyToDocumentNilOptsInheritsParse(t *testing.T) {
	opts := NewApplyOptions()
	opts.EnsurePathExistsOnAdd = true

	d, err := ParseDocument([]byte(`{}`), opts)
	if err != nil {
		t.Fatal(err)
	}
	p, err := DecodePatch([]byte(`[{"op":"add","path":"/a/b/c","value":1}]`))
	if err != nil {
		t.Fatal(err)
	}
	if err := p.ApplyToDocument(d, nil); err != nil {
		t.Fatalf("ApplyToDocument with inherited opts: %v", err)
	}
	out, err := d.Marshal()
	if err != nil {
		t.Fatal(err)
	}
	if !compareJSON(string(out), `{"a":{"b":{"c":1}}}`) {
		t.Errorf("got %s", string(out))
	}
}

// benchPatches builds n small "append to /items" patches plus the matching
// starting document, for use by replay benchmarks.
func benchPatches(n int) (doc []byte, patches []Patch) {
	doc = []byte(`{"items":[]}`)
	patches = make([]Patch, n)
	for i := 0; i < n; i++ {
		raw := []byte(`[{"op":"add","path":"/items/-","value":{"i":` + strconv.Itoa(i) + `,"s":"item-` + strconv.Itoa(i) + `"}}]`)
		p, err := DecodePatch(raw)
		if err != nil {
			panic(err)
		}
		patches[i] = p
	}
	return doc, patches
}

// BenchmarkReplayLegacy: parse+marshal per patch (the existing pattern).
func BenchmarkReplayLegacy(b *testing.B) {
	for _, n := range []int{50, 200, 800} {
		b.Run(fmt.Sprintf("N=%d", n), func(b *testing.B) {
			doc, patches := benchPatches(n)
			b.ResetTimer()
			for iter := 0; iter < b.N; iter++ {
				cur := append([]byte(nil), doc...)
				for _, p := range patches {
					out, err := p.Apply(cur)
					if err != nil {
						b.Fatal(err)
					}
					cur = out
				}
			}
		})
	}
}

// BenchmarkReplayDocument: parse once, apply n times, marshal once.
func BenchmarkReplayDocument(b *testing.B) {
	for _, n := range []int{50, 200, 800} {
		b.Run(fmt.Sprintf("N=%d", n), func(b *testing.B) {
			doc, patches := benchPatches(n)
			b.ResetTimer()
			for iter := 0; iter < b.N; iter++ {
				d, err := ParseDocument(doc, nil)
				if err != nil {
					b.Fatal(err)
				}
				for _, p := range patches {
					if err := p.ApplyToDocument(d, nil); err != nil {
						b.Fatal(err)
					}
				}
				if _, err := d.Marshal(); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
