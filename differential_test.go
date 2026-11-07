package jsorder

import (
	"bufio"
	"encoding/json"
	"math"
	"os"
	"strconv"
	"testing"
	"unicode/utf16"
	"unicode/utf8"
)

// The goldens under testdata/ are produced by internal/gen/generate.mjs, which
// runs the real JavaScript. Regenerate with:
//
//	bun internal/gen/generate.mjs
//
// Never edit them by hand — an expected value nobody watched the reference
// produce is a guess with a test around it.

func readGoldens[T any](t *testing.T, path string) []T {
	t.Helper()
	f, err := os.Open(path)
	if err != nil {
		t.Fatalf("open %s: %v (run: bun internal/gen/generate.mjs)", path, err)
	}
	defer f.Close()

	var out []T
	scan := bufio.NewScanner(f)
	scan.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scan.Scan() {
		if len(scan.Bytes()) == 0 {
			continue
		}
		var c T
		if err := json.Unmarshal(scan.Bytes(), &c); err != nil {
			t.Fatalf("%s: %v", path, err)
		}
		out = append(out, c)
	}
	if err := scan.Err(); err != nil {
		t.Fatal(err)
	}
	return out
}

type numberGolden struct {
	Bits string `json:"bits"`
	Str  string `json:"str"`
	JSON string `json:"json"`
}

// TestNumberFormattingAgainstJS is the test this package most needs. Number
// formatting is where a port looks right and is wrong: the output parses, the
// value round-trips, and only the bytes differ — so only a signature notices.
func TestNumberFormattingAgainstJS(t *testing.T) {
	goldens := readGoldens[numberGolden](t, "testdata/numbers.jsonl")
	if len(goldens) < 1000 {
		t.Fatalf("thin corpus: %d cases", len(goldens))
	}

	var mismatches int
	for _, g := range goldens {
		bits, err := strconv.ParseUint(g.Bits, 16, 64)
		if err != nil {
			t.Fatalf("bad bits %q: %v", g.Bits, err)
		}
		x := math.Float64frombits(bits)

		if got := FormatNumber(x); got != g.Str {
			mismatches++
			if mismatches <= 20 {
				t.Errorf("Number::toString(0x%s): got %q, JS says %q", g.Bits, got, g.Str)
			}
		}
		if got := string(appendJSONNumber(nil, x)); got != g.JSON {
			mismatches++
			if mismatches <= 20 {
				t.Errorf("JSON.stringify(0x%s): got %q, JS says %q", g.Bits, got, g.JSON)
			}
		}
	}
	if mismatches > 20 {
		t.Errorf("%d total mismatches over %d cases", mismatches, len(goldens))
	}
	t.Logf("%d number cases", len(goldens))
}

type stringGolden struct {
	Units []uint16 `json:"units"`
	Out   string   `json:"out"`
}

// wtf8 encodes a UTF-16 code-unit sequence the way a WTF-8 encoder would,
// preserving unpaired surrogates. utf16.Decode replaces them with U+FFFD,
// which would make the lone-surrogate cases untestable.
func wtf8(units []uint16) string {
	var buf []byte
	for i := 0; i < len(units); i++ {
		u := units[i]
		if utf16.IsSurrogate(rune(u)) && i+1 < len(units) {
			if r := utf16.DecodeRune(rune(u), rune(units[i+1])); r != utf8.RuneError {
				buf = utf8.AppendRune(buf, r)
				i++
				continue
			}
		}
		if utf16.IsSurrogate(rune(u)) {
			// Three-byte WTF-8 form of the surrogate code point itself.
			buf = append(buf,
				byte(0xE0|u>>12), byte(0x80|(u>>6)&0x3F), byte(0x80|u&0x3F))
			continue
		}
		buf = utf8.AppendRune(buf, rune(u))
	}
	return string(buf)
}

func TestStringEscapingAgainstJS(t *testing.T) {
	goldens := readGoldens[stringGolden](t, "testdata/strings.jsonl")
	if len(goldens) < 500 {
		t.Fatalf("thin corpus: %d cases", len(goldens))
	}

	var mismatches int
	for _, g := range goldens {
		s := wtf8(g.Units)
		if got := string(AppendString(nil, s)); got != g.Out {
			mismatches++
			if mismatches <= 20 {
				t.Errorf("JSON.stringify(units %v): got %s, JS says %s", g.Units, got, g.Out)
			}
		}
	}
	if mismatches > 20 {
		t.Errorf("%d total mismatches over %d cases", mismatches, len(goldens))
	}
	t.Logf("%d string cases", len(goldens))
}

type documentGolden struct {
	In   string   `json:"in"`
	Out  string   `json:"out"`
	Keys []string `json:"keys"`
}

// TestStringifyAgainstJS is the end-to-end check: parse a document and
// re-serialise it, and demand the same bytes JavaScript produces.
//
// DecodeFloat64, not Decode, because JSON.parse produces float64 — comparing
// against Decode's preserved literals would be comparing against a different
// question. Decode's round-trip is covered by TestDecodePreservesBigIntegers.
func TestStringifyAgainstJS(t *testing.T) {
	goldens := readGoldens[documentGolden](t, "testdata/stringify.jsonl")
	if len(goldens) < 200 {
		t.Fatalf("thin corpus: %d cases", len(goldens))
	}

	for i, g := range goldens {
		value, err := DecodeFloat64([]byte(g.In))
		if err != nil {
			t.Errorf("case %d: decode %s: %v", i, g.In, err)
			continue
		}
		got, err := MarshalJS(value)
		if err != nil {
			t.Errorf("case %d: marshal: %v", i, err)
			continue
		}
		if string(got) != g.Out {
			t.Errorf("case %d\n in:  %s\n got: %s\n JS:  %s", i, g.In, got, g.Out)
		}
		if g.Keys != nil {
			keys := Keys(value)
			if len(keys) != len(g.Keys) {
				t.Errorf("case %d: %d keys, JS says %d", i, len(keys), len(g.Keys))
				continue
			}
			for j := range keys {
				if keys[j] != g.Keys[j] {
					t.Errorf("case %d: Object.keys mismatch at %d: got %q, JS says %q\n got %v\n JS  %v",
						i, j, keys[j], g.Keys[j], keys, g.Keys)
					break
				}
			}
		}
	}
	t.Logf("%d document cases", len(goldens))
}
