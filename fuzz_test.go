package jsorder

import (
	"encoding/json"
	"math"
	"math/rand/v2"
	"strconv"
	"strings"
	"testing"
)

func FuzzDecode(f *testing.F) {
	seeds := []string{
		`{"10":1,"2":2,"1":3,"name":4}`,
		`{"a":null,"b":[1,2,{"c":true}]}`,
		`[]`, `{}`, `null`, `0`, `-0`, `1e21`, `1e-7`, `""`, `"\ud800"`,
		`{"big":12345678901234567890}`,
		`{"a":1,"a":2}`,
		`{"":""}`,
		`"<>&"`,
		`{"x":"a b"}`,
	}
	for _, s := range seeds {
		f.Add([]byte(s))
	}

	f.Fuzz(func(t *testing.T, data []byte) {
		v, err := Decode(data)
		if err != nil {
			return // invalid input; rejecting it is correct
		}

		// Anything the decoder produces, the marshallers must be able to emit.
		// A value model that can decode something it cannot encode is a trap
		// that only fires on customer data.
		out, err := MarshalJS(v)
		if err != nil {
			t.Fatalf("decoded %q but MarshalJS failed: %v", data, err)
		}
		if _, err := StableMarshal(v); err != nil {
			t.Fatalf("decoded %q but StableMarshal failed: %v", data, err)
		}

		// The output must itself be valid JSON.
		if !json.Valid(out) {
			t.Fatalf("MarshalJS produced invalid JSON for %q: %s", data, out)
		}

		// And re-encoding must be a fixed point. If the second pass differs,
		// the encoding is not canonical, so a hash of it is not stable.
		again, err := Decode(out)
		if err != nil {
			t.Fatalf("cannot re-decode own output %s: %v", out, err)
		}
		out2, err := MarshalJS(again)
		if err != nil {
			t.Fatalf("re-marshal failed: %v", err)
		}
		if string(out) != string(out2) {
			t.Fatalf("encoding is not idempotent:\n in:  %q\n 1st: %s\n 2nd: %s", data, out, out2)
		}
	})
}

func FuzzNumberFormat(f *testing.F) {
	for _, seed := range []float64{0, -0, 1, 0.1, 1e21, 1e-7, 5e-324, math.MaxFloat64} {
		f.Add(math.Float64bits(seed))
	}
	f.Fuzz(func(t *testing.T, bits uint64) {
		x := math.Float64frombits(bits)
		if math.IsNaN(x) || math.IsInf(x, 0) {
			return
		}
		s := FormatNumber(x)

		// Number::toString is required to round-trip: parsing the output must
		// give back exactly the same float64.
		back, err := strconv.ParseFloat(s, 64)
		if err != nil {
			t.Fatalf("FormatNumber(%x) = %q, which does not parse: %v", bits, s, err)
		}
		if back != x && !(x == 0 && back == 0) {
			t.Fatalf("FormatNumber(%x) = %q, which parses back as %x", bits, s, math.Float64bits(back))
		}

		// Cross-check against the spec assembly done the slow, obvious way.
		if want := referenceNumberToString(x); s != want {
			t.Fatalf("FormatNumber(%x) = %q, reference says %q", bits, s, want)
		}

		// No Go-isms: two-digit exponent padding, a bare 'E', or a "-0".
		if strings.Contains(s, "E") {
			t.Fatalf("uppercase exponent in %q", s)
		}
		if s == "-0" {
			t.Fatalf("negative zero rendered as %q; JavaScript writes 0", s)
		}
		if i := strings.IndexByte(s, 'e'); i >= 0 {
			exp := s[i+1:]
			if exp[0] != '+' && exp[0] != '-' {
				t.Fatalf("exponent without a sign in %q", s)
			}
			if len(exp) > 2 && exp[1] == '0' {
				t.Fatalf("zero-padded exponent in %q; JavaScript writes 1e-7, not 1e-07", s)
			}
		}
	})
}

// referenceNumberToString is a deliberately slow, literal transcription of
// ECMAScript Number::toString step 5, using big-ish string surgery instead of
// the byte-appending fast paths. Its only job is to disagree with the optimised
// implementation if that one drifts.
func referenceNumberToString(x float64) string {
	if x == 0 {
		return "0"
	}
	sign := ""
	if x < 0 {
		sign = "-"
		x = -x
	}
	// strconv 'e' gives shortest round-trip digits with an explicit exponent.
	parts := strings.SplitN(strconv.FormatFloat(x, 'e', -1, 64), "e", 2)
	digits := strings.Replace(parts[0], ".", "", 1)
	exp, _ := strconv.Atoi(parts[1])
	k := len(digits)
	n := exp + 1

	switch {
	case k <= n && n <= 21:
		return sign + digits + strings.Repeat("0", n-k)
	case 0 < n && n <= 21:
		return sign + digits[:n] + "." + digits[n:]
	case -6 < n && n <= 0:
		return sign + "0." + strings.Repeat("0", -n) + digits
	}
	e := n - 1
	esign := "+"
	if e < 0 {
		esign = "-"
		e = -e
	}
	mantissa := digits
	if k > 1 {
		mantissa = digits[:1] + "." + digits[1:]
	}
	return sign + mantissa + "e" + esign + strconv.Itoa(e)
}

// TestFormatNumberRoundTripProperty is the property test over random doubles,
// separate from the fuzzer so it runs in the normal suite.
func TestFormatNumberRoundTripProperty(t *testing.T) {
	rng := rand.New(rand.NewPCG(1, 2))
	for i := 0; i < 200000; i++ {
		x := math.Float64frombits(rng.Uint64())
		if math.IsNaN(x) || math.IsInf(x, 0) {
			continue
		}
		s := FormatNumber(x)
		back, err := strconv.ParseFloat(s, 64)
		if err != nil || (back != x && !(x == 0 && back == 0)) {
			t.Fatalf("round trip failed for %x: %q -> %v (%v)", math.Float64bits(x), s, back, err)
		}
		if want := referenceNumberToString(x); s != want {
			t.Fatalf("drift at %x: got %q, reference %q", math.Float64bits(x), s, want)
		}
	}
}

// TestDecodeMarshalIsIdempotentProperty checks the fixed-point property over
// generated documents rather than fuzzer-discovered ones. An encoding that is
// not a fixed point cannot be hashed, because the hash depends on how many
// times the value has been through the pipe.
func TestDecodeMarshalIsIdempotentProperty(t *testing.T) {
	rng := rand.New(rand.NewPCG(7, 11))
	for i := 0; i < 20000; i++ {
		doc, err := MarshalJS(randomValue(rng, 0))
		if err != nil {
			t.Fatal(err)
		}
		v, err := Decode(doc)
		if err != nil {
			t.Fatalf("cannot decode own output %s: %v", doc, err)
		}
		again, err := MarshalJS(v)
		if err != nil {
			t.Fatal(err)
		}
		if string(doc) != string(again) {
			t.Fatalf("not idempotent:\n 1st: %s\n 2nd: %s", doc, again)
		}
	}
}

// randomValue builds a value from the JSON model, over-sampling numeric-looking
// keys because that is where the ordering rule bites.
func randomValue(rng *rand.Rand, depth int) any {
	if depth > 3 || rng.Float64() < 0.4 {
		switch rng.IntN(7) {
		case 0:
			return float64(rng.IntN(1000000))
		case 1:
			return rng.NormFloat64() * math.Pow(10, float64(rng.IntN(40)-20))
		case 2:
			return "s<&>" + strconv.Itoa(rng.IntN(1000))
		case 3:
			return Null
		case 4:
			return rng.IntN(2) == 0
		case 5:
			return json.Number(strconv.FormatUint(rng.Uint64(), 10))
		default:
			return ""
		}
	}
	if rng.Float64() < 0.6 {
		m := New()
		for i, n := 0, rng.IntN(6); i < n; i++ {
			m.Set(randomKey(rng), randomValue(rng, depth+1))
		}
		return m
	}
	a := make([]any, rng.IntN(5))
	for i := range a {
		a[i] = randomValue(rng, depth+1)
	}
	return a
}

func randomKey(rng *rand.Rand) string {
	switch rng.IntN(5) {
	case 0:
		return strconv.Itoa(rng.IntN(30))
	case 1:
		return strconv.Itoa(rng.IntN(5000000000))
	case 2:
		return "0" + strconv.Itoa(rng.IntN(100))
	default:
		return "k" + strconv.Itoa(rng.IntN(26))
	}
}
