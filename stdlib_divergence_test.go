package jsorder

import (
	"bytes"
	"encoding/json"
	"fmt"
	"math"
	"strconv"
	"testing"
)

// TestEncodingJSONWouldDiverge does two jobs.
//
// It documents, with a number rather than an assertion, how far
// encoding/json — even with SetEscapeHTML(false), the fix everyone reaches for
// first — is from JSON.stringify on this corpus.
//
// And it proves the corpus DISCRIMINATES. A differential suite that passes
// against both the right implementation and the wrong one is measuring nothing,
// and that failure mode is invisible: the tests are green either way.
func TestEncodingJSONWouldDiverge(t *testing.T) {
	stdlib := func(v any) string {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false) // necessary, and the point is that it is not sufficient
		if err := enc.Encode(v); err != nil {
			return "<error>"
		}
		return string(bytes.TrimRight(buf.Bytes(), "\n"))
	}

	docs := readGoldens[documentGolden](t, "testdata/stringify.jsonl")
	var docDiff int
	for _, g := range docs {
		// Decode into stdlib's own value model, which is what a naive port does.
		var v any
		if err := json.Unmarshal([]byte(g.In), &v); err != nil {
			continue
		}
		if stdlib(v) != g.Out {
			docDiff++
		}
	}

	nums := readGoldens[numberGolden](t, "testdata/numbers.jsonl")
	var numDiff, gDiff, vDiff int
	var numExamples []string
	for _, g := range nums {
		bits, _ := strconv.ParseUint(g.Bits, 16, 64)
		x := math.Float64frombits(bits)
		if math.IsNaN(x) || math.IsInf(x, 0) {
			continue // stdlib errors; not a formatting difference
		}
		if got := stdlib(x); got != g.JSON {
			numDiff++
			if len(numExamples) < 5 {
				numExamples = append(numExamples, got+" vs JS "+g.JSON)
			}
		}
		// The two formatters a hand-rolled marshaller actually reaches for.
		if strconv.FormatFloat(x, 'g', -1, 64) != g.JSON {
			gDiff++
		}
		if fmtV(x) != g.JSON {
			vDiff++
		}
	}

	strs := readGoldens[stringGolden](t, "testdata/strings.jsonl")
	var strDiff int
	for _, g := range strs {
		if stdlib(wtf8(g.Units)) != g.Out {
			strDiff++
		}
	}

	t.Logf("encoding/json (SetEscapeHTML=false) disagrees with JSON.stringify on:")
	t.Logf("  documents: %d of %d", docDiff, len(docs))
	t.Logf("  numbers:   %d of %d  %v", numDiff, len(nums), numExamples)
	t.Logf("  strings:   %d of %d", strDiff, len(strs))
	t.Logf("the float formatters a hand-rolled marshaller reaches for instead:")
	t.Logf("  strconv.FormatFloat(x,'g',-1,64): %d of %d", gDiff, len(nums))
	t.Logf("  fmt.Sprintf(\"%%v\", x):             %d of %d", vDiff, len(nums))

	if docDiff == 0 || numDiff == 0 || strDiff == 0 {
		t.Fatal("corpus does not discriminate: stdlib passes a category it should fail, " +
			"so that category's differential test proves nothing")
	}
}

// fmtV is what a port writes when it does not think about this at all.
func fmtV(x float64) string { return fmt.Sprintf("%v", x) }
