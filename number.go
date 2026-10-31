package jsorder

import "github.com/trutohq/jsbuiltin"

// AppendNumber appends x to dst formatted as ECMAScript Number::toString(x, 10)
// would render it, which is what JSON.stringify emits for a finite number.
// NaN and the infinities render as "NaN", "Infinity" and "-Infinity";
// JSON.stringify writes `null` for all three instead, and that substitution
// belongs to the marshaller, not here.
//
// The formatter lives in github.com/trutohq/jsbuiltin since 2026-10-31
// (gotruto D-0868: JavaScript's built-in operations are one module). This
// wrapper keeps jsorder's API; the goldens here (testdata/numbers.jsonl,
// FuzzNumberFormat) still hold it.
func AppendNumber(dst []byte, x float64) []byte { return jsbuiltin.AppendNumber(dst, x) }

// FormatNumber returns x as ECMAScript Number::toString would render it.
func FormatNumber(x float64) string { return jsbuiltin.FormatNumber(x) }
