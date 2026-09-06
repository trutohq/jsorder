# jsorder

JavaScript value semantics for Go: `[[OwnPropertyKeys]]` ordering,
`JSON.stringify` byte-fidelity, and an order-preserving JSON decoder that does
not lose big integers.

```go
import "github.com/trutohq/jsorder"

v, _ := jsorder.Decode(body)          // *jsorder.Map, keys in JS order
out, _ := jsorder.MarshalJS(v)        // byte-identical to JSON.stringify
sum := sha1.Sum(out)                  // ...so this hash matches the JS one
```

## Why it exists

Because there were two of it.

`goenvoy/internal/ordered` type-aliases gnata's ordered map and decodes JSON
into it (`ordered.go:57`, `json.go`). `cardamom-go/go/ordered.go:23` defines a
second, 290-line ordered map *beside* a gnata dependency it already has
(`go/go.mod`, used at `expression.go:21`), and carries a lossy converter between
the two — `normalizeExpressionResult`, `expression.go:32-36`, commented
"converts gnata's ordered maps into plain ones" — sitting exactly where key
order is supposed to survive.

A second implementation of a *value model* is not duplicated code, it is
duplicated decisions. Nothing tests two of them against each other, so they
drift, and the drift surfaces as a signature that will not verify or a cursor
that will not decode — never as an error. This package is the single answer
(D-0013, D-0014).

Truto's exposure, for sizing: 26 files in `src/` use `hashString`, and 16 of
those also call `JSON.stringify` — including
`webhook/buildWebhookDeliveryRequest.ts` (the customer-visible signature),
`alarm/Alarm.ts`, all three paginators, and `link-token`.

## What `encoding/json` gets wrong for this job

Measured on the committed corpus, with `SetEscapeHTML(false)` already applied —
the fix everyone reaches for first:

| | stdlib disagrees with `JSON.stringify` on |
|---|---|
| documents | **250 of 628** |
| strings | **54 of 1442** |
| numbers | **1 of 6058** |

Four distinct problems:

**1. Key order.** Go sorts map keys; `JSON.stringify` does not. And "insertion
order" is itself the wrong model — JavaScript's `[[OwnPropertyKeys]]` emits
array-index keys first in ascending *numeric* order, then string keys in
insertion order, so `{"10":a,"2":b,"1":c,"name":d}` enumerates as
`1, 2, 10, name`. An integration keyed by numeric custom-field id produces
exactly that shape. `"4294967294"` is an index; `"4294967295"` is not.

**2. String escaping.** `SetEscapeHTML(false)` stops `<`, `>` and `&` being
escaped, and is still not sufficient: Go escapes U+2028 and U+2029 (so its
output can be embedded in a JS source file — a different problem), and spells
backspace and form feed as six-character `\u` escapes where JavaScript uses the
short forms. That is the 54 above.

**3. Numbers.** This one is the opposite of what everyone assumes, and it is
worth stating plainly: **Go's `encoding/json` already mimics ECMAScript float
formatting** — it switches to exponential at the same `1e-6`/`1e21` thresholds
and even rewrites `e-09` into `e-9`. On this corpus its *only* float divergence
is negative zero, which Go writes as `-0` and JavaScript as `0`.

The trap is what a hand-rolled marshaller reaches for instead:

| formatter | disagrees with `JSON.stringify` |
|---|---|
| `encoding/json` | 1 of 6058 (`-0`) |
| `strconv.FormatFloat(x,'g',-1,64)` | **1670 of 6058** |
| `fmt.Sprintf("%v", x)` | **1670 of 6058** |

So the moment you write your own encoder to fix problems 1 and 2 — which you
must — you inherit problem 3 unless you also implement `Number::toString`. This
package does. See `number.go`.

**4. Large integers.** JSON integers past 2^53 become lossy `float64`.
`Decode` keeps the literal as a `json.Number`; `DecodeFloat64` reproduces
`JSON.parse`'s loss when byte-identical output matters more.

## API

### Two marshallers, which must never be mixed

```go
jsorder.MarshalJS(v)      // JS enumeration order — the wire, and anything hashed
jsorder.StableMarshal(v)  // keys sorted bytewise — hashing internal to Go
jsorder.AppendJS(dst, v)  // MarshalJS into a caller-owned buffer
```

This split is D-0008; goenvoy hit the bug it prevents. They differ on exactly
the values that matter — `MarshalJS` writes `{"1":…,"2":…,"10":…}` where
`StableMarshal` writes `{"1":…,"10":…,"2":…}`.

**If the JavaScript side computes the same value, you want `MarshalJS`.**
`StableMarshal` is for content addressing that has no JavaScript peer.

### Decoding

```go
jsorder.Decode(data)         // numbers stay json.Number — lossless
jsorder.DecodeFloat64(data)  // numbers become float64 — exactly JSON.parse
```

### Value model

| JSON | Go |
|---|---|
| object | `*jsorder.Map` (alias of `gnata.OrderedMap`) |
| array | `[]any` |
| string | `string` |
| number | `json.Number` (or `float64`) |
| `true` | `bool` |
| `null` | `jsorder.Null` — **never Go nil** |

Go nil is spent on JavaScript's `undefined`, following goenvoy: conflating them
makes `$exists()` false for a field a document explicitly set to null.
`MarshalJS` then follows `JSON.stringify` exactly — an undefined-valued object
key is **omitted**, an undefined array element becomes **null**:

```go
jsorder.MarshalJS(jsorder.Pairs("a", nil, "b", 1))  // {"b":1}
jsorder.MarshalJS([]any{nil, 1})                    // [null,1]
```

### Ordering

```go
jsorder.KeyOrder(keys)     // []string in JS enumeration order
jsorder.JSOrder(m)         // *Map rebuilt in JS order
jsorder.IsArrayIndex(key)  // the canonical-representation test
jsorder.Keys(v)            // Object.keys(v)
```

`MarshalJS` applies the ordering itself, so a `Map` built with `Pairs` in any
order still emits JavaScript's bytes.

## Verification

Correctness here is invisible when wrong — it surfaces as a signature that will
not verify, never as an error. So the goldens are produced by **executing**
`bun`/`node`, never written by hand:

```sh
bun internal/gen/generate.mjs      # regenerate testdata/
go test ./...
```

| suite | cases | what it proves |
|---|---|---|
| `TestNumberFormattingAgainstJS` | 6058 | `Number::toString` and `JSON.stringify`, compared on IEEE-754 **bit patterns** so the corpus cannot beg the question |
| `TestStringEscapingAgainstJS` | 1442 | escaping, compared on UTF-16 **code-unit sequences**, including lone surrogates |
| `TestStringifyAgainstJS` | 628 | full documents, plus `Object.keys` order checked separately |
| `TestLiveDifferentialAgainstJS` | 20000 | documents generated **in Go** and stringified by a **live `bun`** — the generator and the oracle on opposite sides |
| `TestEncodingJSONWouldDiverge` | — | fails if stdlib *passes* a category, i.e. if the corpus stopped discriminating |
| `FuzzDecode` | 610k execs | decode → marshal → decode is a fixed point; anything decodable is encodable |
| `FuzzNumberFormat` | 2.0M execs | round-trip, plus cross-check against a slow literal transcription of the spec |
| property tests | 220k | 200k random doubles, 20k random documents |

The suite was mutation-tested: moving the exponential threshold from 21 to 20,
escaping `<>&` like Go does, and dropping the `[[OwnPropertyKeys]]` rule are
each caught.

## Performance

Intel Xeon E-2386G, Go 1.27.0, `-count=6`, medians. The workload is one page of
1000 records — what a paginator hashes for loop detection — and one webhook
body, which is what `buildWebhookDeliveryRequest` signs.

| | ns/op | B/op | allocs/op |
|---|---:|---:|---:|
| `Decode` webhook body | 5,097 | 4,065 | 110 |
| `Decode` 1000-record page | 1,847,396 | 1,380,910 | 44,012 |
| — `encoding/json` for reference | 2,795,610 | 979,760 | 28,124 |
| `MarshalJS` webhook body | 1,119 | 768 | 2 |
| `MarshalJS` 1000-record page | 531,572 | 686,822 | 21 |
| `AppendJS` 1000-record page (reused buffer) | 388,892 | **24** | **1** |
| — `encoding/json` encoder for reference | 6,867,873 | 6,217,110 | 95,048 |
| `StableMarshal` 1000-record page | 619,435 | 686,918 | 23 |
| `KeyOrder`, no index keys | 16 | 0 | 0 |
| `KeyOrder`, with index keys | 228 | 128 | 1 |
| `AppendNumber` integer | 13 | 0 | 0 |
| `AppendNumber` float | 36 | 0 | 0 |
| `AppendString` | 86 | 0 | 0 |

Against `encoding/json`'s encoder on the same page, `MarshalJS` is **12.9×
faster with 4,526× fewer allocations** (21 vs 95,048), and `AppendJS` into a
caller-owned buffer is **17.7× faster at a single allocation**. Decoding is 1.5×
faster than stdlib *while preserving key order*, at 1.6× the allocations — that
extra is the ordered map's key slice and hash table per object, which is what
buys the ordering.

### Against JavaScript

Same workload, 200 iterations over a 1000-record page (144,875 bytes):

| | decode ms/iter | encode ms/iter | peak RSS |
|---|---:|---:|---:|
| **Go (jsorder)** | **2.091** | **0.381** | **10.6 MiB** |
| bun 1.x | 0.481 | 0.664 | 87.3 MiB |
| node | 0.515 | 0.555 | — |

```sh
go run ./internal/bench
bun internal/bench/bench.mjs
```

**Encoding is 1.7× faster than bun in a twelfth of the memory. Decoding is 4.3×
SLOWER**, and that is a real finding rather than a rounding error: V8 and
JavaScriptCore parse JSON with hand-written C++, and Go's `encoding/json` token
API boxes every string and number into an `any`. A profile attributes ~50% of
decode allocations to `Decoder.Token` alone.

A prototype using `tidwall/gjson` — already a transitive dependency through
gnata, so free — decodes the same page in **1.04 ms/iter including a full
validation pass**, against 2.90 for `json.Unmarshal`. That is a ~1.6× win for
this package's decoder and it still would not close the gap to bun. It was
**not** adopted here: gjson's string unescaping is a second implementation of
the same value semantics, which is precisely what D-0013 says not to introduce
without its own differential corpus. Recorded as a measured lead, not done.

### Concurrency: measured, and deliberately not shipped

`concurrency_experiment_test.go` fans the per-record encode of a page across
`NumCPU` goroutines, order-preserving (each worker owns a contiguous chunk and
its own buffer; the buffers concatenate in index order).

| records | sequential | parallel (4 cores) | |
|---:|---:|---:|---|
| 10 | 4.8 µs | 11.2 µs | **2.3× slower** |
| 30 | 15.2 µs | 18.3 µs | slower |
| 50 | 26.0 µs | 24.6 µs | break-even |
| 1,000 | 548 µs | 365 µs | 1.5× |
| 100,000 | 54.2 ms | 30.2 ms | 1.8× |

Crossover is around **40 records**, and the ceiling is 1.8× while allocating
**22% more memory** (99.9 MB vs 82.0 MB at 100k). It is not shipped, for three
reasons: `MarshalJS` has a scalar signature and hiding a fan-out behind it is
the anti-pattern; every consumer already runs inside a concurrent request
handler, so this would multiply goroutines rather than add them; and a leaf
library should not own a goroutine policy its caller is better placed to set.
The test stays as the evidence, and as the recipe to copy if a caller decides
otherwise. See D-0016.

## Dependencies

One: `github.com/recolabs/gnata`, for the `OrderedMap` type only. Nothing here
evaluates anything.

`jsorder.Map` is a **type alias**, not a wrapper, and that is load-bearing:
gnata's evaluator type-switches on `*evaluator.OrderedMap` in 30 places, so a
distinct Go type — however identical its fields — would stop being recognised as
ordered input and get sorted on the way through an expression. That is the bug
this package exists to close, reintroduced one layer up.

The cost is measured and real: **+3.4 MB of binary** (2.06 MB → 5.54 MB for a
trivial program), and it does not shrink, because gnata's `init()`
(`gnata.go:142`) builds the full builtin environment and defeats dead-code
elimination. Patching that to be lazy recovers only 0.4 MB of it. See D-0015 for
why it was accepted anyway.

## Records

`docs/go-port/wiki/` in the `truto` repo: `DECISIONS.md` (D-0015 … D-0018),
`DIVERGENCES.md` (J-01 … J-05), `GOTCHAS.md`, `TRACKER.md`.
