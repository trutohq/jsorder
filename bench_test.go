package jsorder

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"io"
	"math"
	"strconv"
	"testing"
)

// Inputs are sized like the real workload, not like a doc example. The two
// shapes that matter:
//
//   - one webhook body, which is what buildWebhookDeliveryRequest signs;
//   - one page of records, which is what the paginators hash for loop
//     detection — sha1(JSON.stringify(results)) once per page.
//
// A 3-key object proves nothing about either.

func webhookBody() *Map {
	return Pairs(
		"event_type", "record.created",
		"id", "01927f3e-8f1a-7c3d-9e4b-2a1f5c8d7e6f",
		"integrated_account_id", "ia_9f8e7d6c5b4a",
		"integration", "salesforce",
		"data", Pairs(
			"id", float64(1234567890123),
			"name", "Acme <Corp> & Sons",
			"amount", 1234.56,
			"active", true,
			"tags", []any{"enterprise", "priority"},
			"custom_fields", Pairs("10", "ten", "2", "two", "1", "one", "label", "value"),
			"address", Pairs("street", "1 Example Way", "city", "Berlin", "postal_code", "10115"),
			"deleted_at", Null,
		),
		"created_at", "2026-09-06T12:00:00.000Z",
	)
}

func recordPage(n int) []any {
	page := make([]any, n)
	for i := range page {
		page[i] = Pairs(
			"id", float64(1000000+i),
			"external_id", "ext_"+strconv.Itoa(i),
			"name", "Record "+strconv.Itoa(i),
			"score", float64(i)*1.5,
			"active", i%2 == 0,
			"meta", Pairs("2", i, "1", i*2, "source", "api"),
			"deleted_at", Null,
		)
	}
	return page
}

func benchInputs() (small []byte, page []byte) {
	s, err := MarshalJS(webhookBody())
	if err != nil {
		panic(err)
	}
	p, err := MarshalJS(recordPage(1000))
	if err != nil {
		panic(err)
	}
	return s, p
}

func BenchmarkDecodeWebhookBody(b *testing.B) {
	data, _ := benchInputs()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := Decode(data); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkDecodePage1000(b *testing.B) {
	_, data := benchInputs()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := Decode(data); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkDecodeStdlibPage1000 is the baseline: what encoding/json costs for
// the same bytes, into a value model that has already lost the key order. Go
// cannot be much slower than this without something being wrong.
func BenchmarkDecodeStdlibPage1000(b *testing.B) {
	_, data := benchInputs()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		var v any
		if err := json.Unmarshal(data, &v); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMarshalJSWebhookBody(b *testing.B) {
	v := webhookBody()
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := MarshalJS(v); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMarshalJSPage1000(b *testing.B) {
	v := recordPage(1000)
	out, _ := MarshalJS(v)
	b.SetBytes(int64(len(out)))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := MarshalJS(v); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkAppendJSPage1000 is the shape a caller feeding a hash should use:
// one buffer, reused, no allocation per call.
func BenchmarkAppendJSPage1000(b *testing.B) {
	v := recordPage(1000)
	buf := make([]byte, 0, 512*1024)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		var err error
		if buf, err = AppendJS(buf[:0], v); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkMarshalStdlibPage1000(b *testing.B) {
	v := recordPage(1000)
	out, _ := MarshalJS(v)
	b.SetBytes(int64(len(out)))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		var buf bytes.Buffer
		enc := json.NewEncoder(&buf)
		enc.SetEscapeHTML(false)
		if err := enc.Encode(v); err != nil {
			b.Fatal(err)
		}
	}
}

func BenchmarkStableMarshalPage1000(b *testing.B) {
	v := recordPage(1000)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		if _, err := StableMarshal(v); err != nil {
			b.Fatal(err)
		}
	}
}

// BenchmarkKeyOrder splits the two cases because they have completely different
// costs, and the common one must stay free. Almost every real object has no
// array-index keys, so KeyOrder has to be a scan that allocates nothing.
func BenchmarkKeyOrderNoIndices(b *testing.B) {
	keys := []string{"id", "external_id", "name", "score", "active", "meta", "deleted_at", "created_at"}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = KeyOrder(keys)
	}
}

func BenchmarkKeyOrderWithIndices(b *testing.B) {
	keys := []string{"10", "2", "1", "name", "0", "label", "100", "value"}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		_ = KeyOrder(keys)
	}
}

func BenchmarkAppendNumberInteger(b *testing.B) {
	buf := make([]byte, 0, 64)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		buf = AppendNumber(buf[:0], 1234567890123)
	}
}

func BenchmarkAppendNumberFloat(b *testing.B) {
	buf := make([]byte, 0, 64)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		buf = AppendNumber(buf[:0], 0.1+0.2)
	}
}

func BenchmarkAppendNumberExponential(b *testing.B) {
	buf := make([]byte, 0, 64)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		buf = AppendNumber(buf[:0], 1.2345678901234569e23)
	}
}

func BenchmarkAppendNumberStdlibFloat(b *testing.B) {
	buf := make([]byte, 0, 64)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		buf = strconv.AppendFloat(buf[:0], 0.1+0.2, 'g', -1, 64)
	}
}

func BenchmarkAppendString(b *testing.B) {
	const s = "Acme <Corp> & Sons — a reasonably long value with some unicode: naïve café"
	buf := make([]byte, 0, 256)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		buf = AppendString(buf[:0], s)
	}
}

func BenchmarkAppendStringEscapeHeavy(b *testing.B) {
	const s = "line1\nline2\ttabbed\r\n\"quoted\" and \\escaped\\ and <html> & more"
	buf := make([]byte, 0, 256)
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		buf = AppendString(buf[:0], s)
	}
}

// BenchmarkMarshalJSPageSizes measures the size at which parallelising the
// per-record encode could pay for itself. See DECISIONS D-0016: it does not,
// because the sequential encode of a whole page is already faster than the
// goroutine handoff for one.
func BenchmarkMarshalJSPageSizes(b *testing.B) {
	for _, n := range []int{1, 10, 100, 1000, 10000} {
		v := recordPage(n)
		b.Run(strconv.Itoa(n), func(b *testing.B) {
			b.ReportAllocs()
			b.ResetTimer()
			for b.Loop() {
				if _, err := MarshalJS(v); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}

var sinkFloat float64

func BenchmarkIsArrayIndex(b *testing.B) {
	keys := []string{"0", "1", "10", "4294967294", "4294967295", "name", "01", "-1"}
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		for _, k := range keys {
			n, ok := IsArrayIndex(k)
			if ok {
				sinkFloat += float64(n)
			}
		}
	}
	if math.IsNaN(sinkFloat) {
		b.Fatal("unreachable")
	}
}

// BenchmarkDecodeTokensPage1000 is the hard lower bound: the jsontext scan over
// the same bytes with no value model built at all. Decode cannot beat it, and
// the gap between the two is the cost of the ordered tree rather than of
// parsing — which is the number that says whether there is anything left to
// win here.
func BenchmarkDecodeTokensPage1000(b *testing.B) {
	_, data := benchInputs()
	b.SetBytes(int64(len(data)))
	b.ReportAllocs()
	b.ResetTimer()
	for b.Loop() {
		dec := jsontext.NewDecoder(bytes.NewBuffer(data), decodeOptions...)
		for {
			_, err := dec.ReadToken()
			if err == nil {
				continue
			}
			if !errors.Is(err, io.EOF) {
				b.Fatal(err)
			}
			break
		}
	}
}
