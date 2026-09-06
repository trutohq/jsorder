package jsorder

import (
	"math"
	"strconv"
)

// AppendNumber appends x to dst formatted as ECMAScript Number::toString(x, 10)
// would render it — which is what JSON.stringify emits for a finite number.
//
// This is NOT strconv.FormatFloat(x, 'g', -1, 64), and the difference is not
// cosmetic: every one of these produces different bytes, and a divergence here
// is a signature that verifies nowhere.
//
//	value                Go %v / 'g'          JavaScript
//	1e21                 1e+21                1e+21      (agree by luck)
//	1e20                 1e+20                100000000000000000000
//	1e-7                 1e-07                1e-7
//	1e-6                 1e-06                0.000001
//	-0                   -0                   0
//	0.1+0.2              0.30000000000000004  0.30000000000000004 (agree)
//
// Go switches to exponential when the decimal exponent is < -4 or >= the digit
// count; ECMAScript switches at n > 21 or n <= -6, pads a positive exponent
// with '+' and does NOT zero-pad the exponent digits.
//
// NaN and the infinities render as "NaN", "Infinity" and "-Infinity", matching
// Number::toString. JSON.stringify writes `null` for all three instead; that
// substitution belongs to the marshaller, not here.
func AppendNumber(dst []byte, x float64) []byte {
	switch {
	case math.IsNaN(x):
		return append(dst, "NaN"...)
	case math.IsInf(x, 1):
		return append(dst, "Infinity"...)
	case math.IsInf(x, -1):
		return append(dst, "-Infinity"...)
	}

	// Step 2 of Number::toString: "If x is either +0 or -0, return '0'." The
	// sign of negative zero is dropped BEFORE the sign is prepended, which is
	// why Go's own formatters disagree — they print "-0".
	if x == 0 {
		return append(dst, '0')
	}
	if x < 0 {
		dst = append(dst, '-')
		x = -x
	}

	// Fast path. Every float64 integer below 2^53 is exact and fits an int64,
	// and for an integer below 1e21 the spec's decimal branch produces exactly
	// the plain integer rendering. This covers ids, counts and epoch
	// milliseconds — the overwhelming majority of numbers in Truto's payloads —
	// without touching the general path below.
	if x < 1<<53 && x == math.Trunc(x) {
		return strconv.AppendInt(dst, int64(x), 10)
	}

	// General path. Ask Go for the SHORTEST round-tripping digits in a form
	// that hands us the mantissa and decimal exponent separately, then assemble
	// them with ECMAScript's rules rather than Go's.
	var scratch [32]byte
	buf := strconv.AppendFloat(scratch[:0], x, 'e', -1, 64)

	// buf is "d.ddde±dd" or "de±dd". Split it.
	expAt := len(buf) - 1
	for buf[expAt] != 'e' {
		expAt--
	}
	exp10 := 0
	negExp := buf[expAt+1] == '-'
	for _, c := range buf[expAt+2:] {
		exp10 = exp10*10 + int(c-'0')
	}
	if negExp {
		exp10 = -exp10
	}

	// digits holds s: the significand with the point removed. In spec terms
	// k = len(digits) and s x 10^(n-k) = x, so n = exp10 + 1.
	//
	// A separate array: buf aliases scratch, so reusing its tail would
	// overwrite the digits being read.
	var digitBuf [24]byte
	digits := append(digitBuf[:0], buf[0])
	if expAt > 1 {
		digits = append(digits, buf[2:expAt]...) // skip the '.'
	}
	k := len(digits)
	n := exp10 + 1

	switch {
	case k <= n && n <= 21:
		// Integer with trailing zeros: "1" with n-k = 20 zeros -> 1e20.
		dst = append(dst, digits...)
		for i := 0; i < n-k; i++ {
			dst = append(dst, '0')
		}
		return dst

	case 0 < n && n <= 21:
		// Decimal point inside the digits: "15", n=1 -> "1.5".
		dst = append(dst, digits[:n]...)
		dst = append(dst, '.')
		return append(dst, digits[n:]...)

	case -6 < n && n <= 0:
		// Leading zeros: n=-5 -> "0.000001". The -6 bound is why 1e-6 is
		// decimal and 1e-7 is exponential.
		dst = append(dst, '0', '.')
		for i := 0; i < -n; i++ {
			dst = append(dst, '0')
		}
		return append(dst, digits...)

	case k == 1:
		// Single significant digit: "1e+21", "5e-324".
		dst = append(dst, digits[0], 'e')
		return appendExponent(dst, n-1)

	default:
		// "1.2345678901234569e+23".
		dst = append(dst, digits[0], '.')
		dst = append(dst, digits[1:]...)
		dst = append(dst, 'e')
		return appendExponent(dst, n-1)
	}
}

// appendExponent writes the exponent the way ECMAScript does: an explicit '+'
// for a non-negative exponent, '-' otherwise, and NO zero padding. Go writes a
// minimum of two digits ("1e-07"), which is the trap.
func appendExponent(dst []byte, e int) []byte {
	if e >= 0 {
		dst = append(dst, '+')
	} else {
		dst = append(dst, '-')
		e = -e
	}
	return strconv.AppendInt(dst, int64(e), 10)
}

// FormatNumber returns x as ECMAScript Number::toString would render it.
func FormatNumber(x float64) string {
	var scratch [32]byte
	return string(AppendNumber(scratch[:0], x))
}
