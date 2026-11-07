// Package jsorder is Truto's one implementation of JavaScript value semantics
// for Go.
//
// It exists because there were two.
//
// `goenvoy/internal/ordered` type-aliases gnata's ordered map and decodes JSON
// into it (`ordered.go:57`, `json.go`). `cardamom-go/go/ordered.go:23` defines a
// second, 290-line ordered map *beside* a gnata dependency it already has
// (`go/go.mod`, used at `expression.go:21`), and carries a lossy converter
// between the two — `normalizeExpressionResult`, `expression.go:32-36`, whose
// comment reads "converts gnata's ordered maps into plain ones" — sitting
// exactly where key order is supposed to survive.
//
// A second implementation of a value model is not duplicated code, it is
// duplicated decisions. Nothing tests two of them against each other, so they
// drift, and the drift surfaces as a signature that will not verify or a cursor
// that will not decode — never as an error. This package is the single answer
// (D-0013, D-0014).
//
// # What encoding/json gets wrong for this job
//
// 1. KEY ORDER. `encoding/json` sorts map keys; `JSON.stringify` does not. And
// "insertion order" is itself the wrong model: JavaScript's [[OwnPropertyKeys]]
// emits ARRAY-INDEX keys first in ascending NUMERIC order, then string keys in
// insertion order, so `{"10":a,"2":b,"1":c,"name":d}` enumerates as
// `1, 2, 10, name`. See [JSOrder].
//
// 2. NUMBER FORMATTING. ECMAScript `Number::toString` is neither Go's `%g` nor
// `strconv.FormatFloat(f, 'g', -1, 64)`. It switches to exponential notation at
// different thresholds (1e21 and 1e-7, not Go's), writes `1e-7` where Go writes
// `1e-07`, and renders -0 as `0`. See [AppendNumber].
//
// 3. STRING ESCAPING. `encoding/json` escapes `<`, `>` and `&`;
// `JSON.stringify` does not. `Encoder.SetEscapeHTML(false)` is necessary and
// NOT sufficient: Go additionally escapes U+2028 and U+2029, and
// spells backspace and form feed as six-character \u escapes where JavaScript
// uses the two-character short forms. See [AppendString].
//
// 4. LARGE INTEGERS. JSON integers past 2^53 become lossy float64. [Decode]
// preserves the literal as a [json.Number]; [DecodeFloat64] reproduces
// `JSON.parse`'s lossy behaviour when byte-identical output matters more.
//
// # The two marshallers, which must never be mixed
//
//   - [MarshalJS] — JavaScript enumeration order, byte-identical to
//     `JSON.stringify`. For anything on the wire, and for anything HASHED OR
//     SIGNED whose JavaScript counterpart hashes `JSON.stringify` output.
//   - [StableMarshal] — keys sorted bytewise at every level. For hashing and
//     content addressing INTERNAL to Go, where no JavaScript peer exists.
//
// Reaching for the wrong one produces a hash that differs from the JavaScript's
// and no error. When in doubt: if the JavaScript side computes the same value,
// use [MarshalJS].
//
// # null versus undefined
//
// Go has one nil and JavaScript has two empties, so this package spends the
// distinction the way goenvoy does:
//
//   - JSON `null` decodes to the [Null] sentinel, never to Go nil.
//   - Go nil means JavaScript `undefined`.
//
// Conflating them makes `$exists()` false for a field a document explicitly set
// to null, and makes a collector suppress an item that should print as null.
// [MarshalJS] follows `JSON.stringify` exactly: an undefined-valued object key
// is OMITTED, an undefined array element becomes `null`.
package jsorder
