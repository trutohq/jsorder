package jsorder

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
)

func mustMarshalJS(t *testing.T, v any) string {
	t.Helper()
	b, err := MarshalJS(v)
	if err != nil {
		t.Fatalf("MarshalJS: %v", err)
	}
	return string(b)
}

func TestIsArrayIndex(t *testing.T) {
	tests := []struct {
		key  string
		n    uint64
		want bool
	}{
		{"0", 0, true},
		{"1", 1, true},
		{"10", 10, true},
		{"4294967294", 4294967294, true}, // 2^32-2, the last index
		{"4294967295", 0, false},         // 2^32-1 is an ordinary key
		{"4294967296", 0, false},
		{"", 0, false},
		{"01", 0, false},          // not canonical
		{"1.0", 0, false},         // not canonical
		{"+1", 0, false},          // not canonical
		{"-1", 0, false},          // negative
		{" 1", 0, false},          // leading space
		{"1 ", 0, false},          // trailing space
		{"1e2", 0, false},         // not decimal digits
		{"0x1", 0, false},         // not decimal digits
		{"name", 0, false},        // ordinary
		{"99999999999", 0, false}, // 11 digits, over the length guard
	}
	for _, tc := range tests {
		n, ok := IsArrayIndex(tc.key)
		if ok != tc.want || (ok && n != tc.n) {
			t.Errorf("IsArrayIndex(%q) = (%d,%v), want (%d,%v)", tc.key, n, ok, tc.n, tc.want)
		}
	}
}

func TestKeyOrder(t *testing.T) {
	tests := []struct {
		name string
		in   []string
		want []string
	}{
		{"the canonical example", []string{"10", "2", "1", "name"}, []string{"1", "2", "10", "name"}},
		{"no index keys is a no-op", []string{"b", "a", "c"}, []string{"b", "a", "c"}},
		{"all index keys", []string{"3", "1", "2"}, []string{"1", "2", "3"}},
		{"string keys keep insertion order", []string{"z", "1", "a", "0"}, []string{"0", "1", "z", "a"}},
		{"near-indices are string keys", []string{"01", "1", "-1", "0"}, []string{"0", "1", "01", "-1"}},
		{"empty", nil, nil},
		{"single", []string{"x"}, []string{"x"}},
		{"boundary", []string{"4294967295", "4294967294"}, []string{"4294967294", "4294967295"}},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := KeyOrder(tc.in)
			if strings.Join(got, ",") != strings.Join(tc.want, ",") {
				t.Errorf("KeyOrder(%v) = %v, want %v", tc.in, got, tc.want)
			}
		})
	}
}

// TestKeyOrderDoesNotMutateInput guards the fast path: KeyOrder returns the
// caller's slice when there is nothing to do, and a Map's Keys() IS the map's
// internal slice, so sorting in place would corrupt the map it came from.
func TestKeyOrderDoesNotMutateInput(t *testing.T) {
	in := []string{"z", "10", "2", "a"}
	original := strings.Join(in, ",")
	KeyOrder(in)
	if strings.Join(in, ",") != original {
		t.Errorf("KeyOrder mutated its input: %v", in)
	}
}

func TestDecodeNullIsNotNil(t *testing.T) {
	v, err := Decode([]byte(`{"a":null}`))
	if err != nil {
		t.Fatal(err)
	}
	m := v.(*Map)
	got, present := m.Get("a")
	if !present {
		t.Fatal("key absent; a decoded null must be PRESENT")
	}
	if got == nil {
		t.Fatal("decoded null is Go nil; nil means undefined and $exists() would be false")
	}
	if !IsNull(got) {
		t.Fatalf("decoded null is %T, want the Null sentinel", got)
	}
}

func TestUndefinedVersusNull(t *testing.T) {
	tests := []struct {
		name string
		in   any
		want string
	}{
		{"undefined object key is omitted", Pairs("a", nil, "b", 1), `{"b":1}`},
		{"null object key is kept", Pairs("a", Null, "b", 1), `{"a":null,"b":1}`},
		{"undefined array element becomes null", []any{nil, 1}, `[null,1]`},
		{"null array element stays null", []any{Null, 1}, `[null,1]`},
		{"all-undefined object", Pairs("a", nil), `{}`},
		{"nil Map", (*Map)(nil), `null`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := mustMarshalJS(t, tc.in); got != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func TestTwoMarshallersDisagreeAsDesigned(t *testing.T) {
	// The whole point of D-0008: these produce different bytes for the same
	// value, and picking the wrong one is silent.
	m := Pairs("10", 1, "2", 2, "1", 3, "name", 4)

	js := mustMarshalJS(t, m)
	if want := `{"1":3,"2":2,"10":1,"name":4}`; js != want {
		t.Errorf("MarshalJS = %s, want %s", js, want)
	}

	stable, err := StableMarshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if want := `{"1":3,"10":1,"2":2,"name":4}`; string(stable) != want {
		t.Errorf("StableMarshal = %s, want %s", stable, want)
	}
	if js == string(stable) {
		t.Error("the two marshallers agree on a value where they must not; " +
			"if they never differ, nothing enforces the split")
	}
}

func TestStableMarshalIsDeterministicOverPlainMaps(t *testing.T) {
	// Go randomises map iteration, so this is the property that matters.
	m := map[string]any{"z": 1, "a": 2, "10": 3, "2": 4, "m": 5}
	first, err := StableMarshal(m)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 200; i++ {
		again, err := StableMarshal(m)
		if err != nil {
			t.Fatal(err)
		}
		if string(again) != string(first) {
			t.Fatalf("StableMarshal is not deterministic:\n %s\n %s", first, again)
		}
	}
	if want := `{"10":3,"2":4,"a":2,"m":5,"z":1}`; string(first) != want {
		t.Errorf("got %s, want %s", first, want)
	}
}

func TestMarshalJSOverPlainMapAppliesJSOrder(t *testing.T) {
	m := map[string]any{"z": 1, "a": 2, "10": 3, "2": 4}
	// Sorted first (no insertion order exists), then JS-ordered.
	if want, got := `{"2":4,"10":3,"a":2,"z":1}`, mustMarshalJS(t, m); got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

func TestDecodePreservesBigIntegers(t *testing.T) {
	const doc = `{"id":12345678901234567890,"small":42,"neg":-98765432109876543210}`

	v, err := Decode([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	got := mustMarshalJS(t, v)
	if got != doc {
		t.Errorf("Decode+MarshalJS lost the literal:\n got %s\n want %s", got, doc)
	}

	// DecodeFloat64 reproduces JavaScript's loss instead, which is the point of
	// having both. The reference produces 12345678901234567000.
	lossy, err := DecodeFloat64([]byte(doc))
	if err != nil {
		t.Fatal(err)
	}
	want := `{"id":12345678901234567000,"small":42,"neg":-98765432109876540000}`
	if got := mustMarshalJS(t, lossy); got != want {
		t.Errorf("DecodeFloat64:\n got %s\n want %s", got, want)
	}
}

func TestJSONNumberIsValidated(t *testing.T) {
	// json.Number is a string type, so a caller can build a nonsense one.
	// Emitting it unchecked would produce a document that is not JSON.
	bad := []string{"", "abc", "01", "1.", ".5", "1e", "+1", "0x10", "Infinity", "1 2", "--1"}
	for _, s := range bad {
		if _, err := MarshalJS(json.Number(s)); err == nil {
			t.Errorf("MarshalJS(json.Number(%q)) succeeded; want an error", s)
		}
	}
	good := []string{"0", "-0", "1", "-1.5", "1e10", "1E+10", "1.5e-7", "123456789012345678901234567890"}
	for _, s := range good {
		out, err := MarshalJS(json.Number(s))
		if err != nil {
			t.Errorf("MarshalJS(json.Number(%q)): %v", s, err)
			continue
		}
		if string(out) != s {
			t.Errorf("json.Number(%q) marshalled as %s; it must be verbatim", s, out)
		}
	}
}

// TestDuplicateKeyKeepsFirstPositionLastValue pins the rule the decoder gets
// from Map.Set rather than from a scan of its own: a repeated key keeps its
// first POSITION and its last VALUE. The array-index and long-object cases are
// the ones that could break if the staging or the sort ever changed, since a
// duplicate has to survive a stable sort and a map larger than one hash group.
func TestDuplicateKeyKeepsFirstPositionLastValue(t *testing.T) {
	tests := []struct {
		name string
		in   string
		want string
	}{
		{"plain", `{"a":1,"b":2,"a":3}`, `{"a":3,"b":2}`},
		{"three of the same key", `{"a":1,"b":2,"a":3,"a":4}`, `{"a":4,"b":2}`},
		{"array-index key repeated", `{"2":1,"1":2,"2":3}`, `{"1":2,"2":3}`},
		{"index and string key repeated", `{"z":1,"10":2,"z":3,"2":4,"10":5}`,
			`{"2":4,"10":5,"z":3}`},
		{"repeat past the hash-group boundary",
			`{"k0":0,"k1":1,"k2":2,"k3":3,"k4":4,"k5":5,"k6":6,"k7":7,"k8":8,` +
				`"k9":9,"k10":10,"k11":11,"k12":12,"k13":13,"k14":14,"k15":15,` +
				`"k16":16,"k17":17,"k0":99}`,
			`{"k0":99,"k1":1,"k2":2,"k3":3,"k4":4,"k5":5,"k6":6,"k7":7,"k8":8,` +
				`"k9":9,"k10":10,"k11":11,"k12":12,"k13":13,"k14":14,"k15":15,` +
				`"k16":16,"k17":17}`},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			v, err := DecodeFloat64([]byte(tc.in))
			if err != nil {
				t.Fatal(err)
			}
			if got := mustMarshalJS(t, v); got != tc.want {
				t.Errorf("got %s, want %s", got, tc.want)
			}
		})
	}
}

func TestDecodeRejectsTrailingContent(t *testing.T) {
	for _, doc := range []string{`{"a":1} garbage`, `[1,2] [3]`, `1 2`, `null null`} {
		if _, err := Decode([]byte(doc)); err == nil {
			t.Errorf("Decode(%q) succeeded; a decoder left mid-stream accepts junk", doc)
		}
	}
	// Trailing whitespace is fine.
	if _, err := Decode([]byte("{\"a\":1}  \n\t")); err != nil {
		t.Errorf("trailing whitespace rejected: %v", err)
	}
}

func TestMarshalRefusesUnknownTypes(t *testing.T) {
	// A reflection fallback would silently reintroduce Go's key sorting, HTML
	// escaping and float formatting for whichever type slipped through.
	for _, v := range []any{[]string{"a"}, map[string]string{"a": "b"}, struct{ A int }{1}, make(chan int)} {
		if _, err := MarshalJS(v); err == nil {
			t.Errorf("MarshalJS(%T) succeeded; it must refuse types outside the JSON value model", v)
		}
	}
}

func TestMarshalBoundsRecursion(t *testing.T) {
	deep := any(1)
	for i := 0; i < maxDepth+10; i++ {
		deep = []any{deep}
	}
	if _, err := MarshalJS(deep); err == nil {
		t.Error("deeply nested value marshalled; want a depth error rather than a stack overflow")
	}
}

func TestNonFiniteNumbers(t *testing.T) {
	tests := []struct {
		in        float64
		toString  string
		stringify string
	}{
		{math.NaN(), "NaN", "null"},
		{math.Inf(1), "Infinity", "null"},
		{math.Inf(-1), "-Infinity", "null"},
		{math.Copysign(0, -1), "0", "0"},
	}
	for _, tc := range tests {
		if got := FormatNumber(tc.in); got != tc.toString {
			t.Errorf("FormatNumber(%v) = %q, want %q", tc.in, got, tc.toString)
		}
		if got := mustMarshalJS(t, tc.in); got != tc.stringify {
			t.Errorf("MarshalJS(%v) = %q, want %q", tc.in, got, tc.stringify)
		}
	}
}

func TestCloneAndSetPreserveOrder(t *testing.T) {
	m := Pairs("b", 1, "a", Pairs("y", 1, "x", 2))

	clone := Clone(m).(*Map)
	if mustMarshalJS(t, clone) != mustMarshalJS(t, m) {
		t.Error("Clone changed the encoding")
	}
	clone.Set("b", 99)
	if v, _ := m.Get("b"); v != 1 {
		t.Error("Clone is not deep: mutating the clone changed the original")
	}

	// An existing key keeps its POSITION, as {...record, [k]: v} does.
	updated := Set(m, "b", 2)
	if want, got := `{"b":2,"a":{"y":1,"x":2}}`, mustMarshalJS(t, updated); got != want {
		t.Errorf("Set: got %s, want %s", got, want)
	}
	// A new key goes last.
	if want, got := `{"b":1,"a":{"y":1,"x":2},"c":3}`, mustMarshalJS(t, Set(m, "c", 3)); got != want {
		t.Errorf("Set new key: got %s, want %s", got, want)
	}
}

func TestAppendJSSharesABuffer(t *testing.T) {
	buf := []byte("prefix:")
	buf, err := AppendJS(buf, Pairs("a", 1))
	if err != nil {
		t.Fatal(err)
	}
	if want, got := `prefix:{"a":1}`, string(buf); got != want {
		t.Errorf("got %s, want %s", got, want)
	}
}

// TestDecodeBoundsDepth records that the decoder is already safe against
// hostile nesting, and why: jsontext's state machine enforces its own max depth
// (maxNestingDepth = 10000, jsontext/state.go:53) on ReadToken, so the
// recursion in decState.value cannot be driven deep enough to exhaust the
// stack. This is a test rather than a comment because it is a property of the
// standard library that could change.
func TestDecodeBoundsDepth(t *testing.T) {
	deep := strings.Repeat("[", 1_000_000) + strings.Repeat("]", 1_000_000)
	if _, err := Decode([]byte(deep)); err == nil {
		t.Fatal("1M-deep array decoded; the stack guard is gone")
	}
	// A depth a real document might reach still works.
	ok := strings.Repeat("[", 1000) + strings.Repeat("]", 1000)
	if _, err := Decode([]byte(ok)); err != nil {
		t.Fatalf("1000-deep array rejected: %v", err)
	}
}
