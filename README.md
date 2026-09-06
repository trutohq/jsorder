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
| `FuzzDecode` | 5.9M execs | decode → marshal → decode is a fixed point; anything decodable is encodable |
| `FuzzNumberFormat` | 2.0M execs | round-trip, plus cross-check against a slow literal transcription of the spec |
| property tests | 220k | 200k random doubles, 20k random documents |
| *(retired)* `FuzzDecodeMatchesLegacy` | 3.2M execs | ran once, against the `encoding/json` decoder this one replaced: same accept/reject and same output bytes on arbitrary input. Deleted with the implementation it compared against |

The suite was mutation-tested: moving the exponential threshold from 21 to 20,
escaping `<>&` like Go does, and dropping the `[[OwnPropertyKeys]]` rule are
each caught.

## Performance

Intel Xeon E-2386G, Go 1.27.0. The workload is one page of 1000 records — what
a paginator hashes for loop detection — and one webhook body, which is what
`buildWebhookDeliveryRequest` signs.

`B/op` and `allocs/op` are exact. `ns/op` is the **minimum of six interleaved
runs**, not the median: this is a shared box that sits at load 10–18, where
medians swing ±80% and the minimum is the only figure that reproduces. (It does
reproduce: the pre-`jsontext` minima below match the medians this table
previously carried, to within 1%.)

| | ns/op | B/op | allocs/op |
|---|---:|---:|---:|
| `Decode` webhook body | **3,873** | **2,632** | **74** |
| `Decode` 1000-record page | **1,550,579** | **1,147,975** | **29,995** |
| — the same, before `jsontext` | 4,910 / 1,830,754 | 4,065 / 1,380,972 | 110 / 44,012 |
| — `encoding/json` into `any`, key order LOST | 2,868,034 | 979,857 | 28,125 |
| — `jsontext` token scan, building nothing | 255,641 | 448 | 7 |
| `MarshalJS` webhook body | 1,083 | 768 | 2 |
| `MarshalJS` 1000-record page | 519,403 | 686,746 | 21 |
| `AppendJS` 1000-record page (reused buffer) | 388,466 | **24** | **1** |
| — `encoding/json` encoder for reference | 7,097,800 | 6,253,947 | 95,044 |
| `StableMarshal` 1000-record page | 608,914 | 686,798 | 22 |
| `KeyOrder`, no index keys | 15.5 | 0 | 0 |
| `KeyOrder`, with index keys | 222 | 128 | 1 |
| `AppendNumber` integer | 13 | 0 | 0 |
| `AppendNumber` float | 35 | 0 | 0 |
| `AppendString` | 82 | 0 | 0 |

Against `encoding/json`'s encoder on the same page, `MarshalJS` is **13.7×
faster with 4,526× fewer allocations** (21 vs 95,044), and `AppendJS` into a
caller-owned buffer is **18.3× faster at a single allocation**. Decoding is
**1.85× faster than stdlib's own decode into `any` while additionally preserving
key order**, at 1.07× the allocations — that remainder is the ordered map's key
slice and hash table per object, which is what buys the ordering.

The last reference row is the one that says whether there is anything left to
win: reading every token and building **nothing** costs 256 µs of the 1,551 µs.
So 84% of decode is constructing the ordered value model, not parsing — three
allocations per object for `gnata.OrderedMap` (struct, key slice, hash table)
plus one interface box per value. Shaving the parser further would move a
sixth of the time at most.

### The decoder reads `encoding/json/jsontext`

Both `encoding/json`'s `Decoder.Token` and `jsontext`'s `Decoder.ReadToken`
preserve order and both are standard library in Go 1.27. The difference is that
`Token` returns `any`, so every string, number, delimiter and boolean is boxed
on the way out; a memory profile put **52% of decode allocations in
`Decoder.Token` alone**. `jsontext.Token` is a struct.

Switching cost 32% of the allocations and about a quarter of the wall time, with
**no new dependency and no second string unescaper** — which is why this was
taken and the `tidwall/gjson` prototype (P-2, 1.6× on its own) was not: gjson's
unescaping is a second implementation of the same value semantics, exactly what
D-0013 forbids without its own differential corpus. See D-0026.

Two options are required for parity and both are off by default, because
`jsontext` implements RFC 7493 and `JSON.parse` does not:
`AllowDuplicateNames(true)` and `AllowInvalidUTF8(true)`. Without the first,
`{"a":1,"a":2}` is rejected outright; without the second, so is a lone
surrogate, where `encoding/json` silently substituted U+FFFD.

3.2 million fuzz executions compared the two decoders on arbitrary bytes — same
accept/reject, same output bytes — before the old one was deleted.

### Against JavaScript

Same workload, 200 iterations over a 1000-record page (144,875 bytes), all
three binaries interleaved in one session, minimum of twelve rounds each:

| | decode ms/iter | encode ms/iter | peak RSS |
|---|---:|---:|---:|
| **Go (jsorder), now** | **1.455** | **0.381** | **9.4 MiB** |
| Go (jsorder), before `jsontext` | 2.053 | 0.379 | 10.0 MiB |
| bun 1.3.14 | 0.483 | 0.595 | 86 MiB |

```sh
go run ./internal/bench
bun internal/bench/bench.mjs
```

**Encoding is 1.56× faster than bun in a ninth of the memory. Decoding is 3.0×
SLOWER**, improved from 4.25× — a real gap, honestly reported. V8 and
JavaScriptCore parse JSON with hand-written C++ into objects with inline
properties and interned strings; Go builds a hash table and a key slice per
object and boxes every value into an interface. The reference rows in the table
above bound what is left: the parse itself is 16% of the time, so even a free
parser would leave Go at roughly 2.5× bun.

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
