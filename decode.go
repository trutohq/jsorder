package jsorder

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"slices"
	"strconv"
	"sync"
)

// ErrTrailingContent is returned when a document is followed by anything other
// than whitespace.
var ErrTrailingContent = errors.New("jsorder: unexpected trailing content")

// Decode parses JSON into the JavaScript value model, preserving object key
// order and numeric literals.
//
// The value model:
//
//	object  -> *Map, in [[OwnPropertyKeys]] order
//	array   -> []any
//	string  -> string
//	number  -> json.Number, the literal exactly as written
//	true    -> bool
//	null    -> Null (the sentinel), never Go nil
//
// Numbers are kept as json.Number so that an integer id beyond 2^53 survives.
// src/common/fetchResourceForIntegratedAccount.ts:462 parses upstream bodies
// with json-bigint {storeAsString: true} for exactly this reason, and decoding
// into float64 would throw the precision away with no error.
//
// A json.Number re-marshals VERBATIM: [MarshalJS] writes back the literal it
// read. That is lossless but it is not what JSON.parse followed by
// JSON.stringify does — JavaScript would round 12345678901234567890 to
// 12345678901234567000. Where those exact bytes matter more than the value,
// use [DecodeFloat64]. See DIVERGENCES J-01.
func Decode(data []byte) (any, error) {
	return decode(data, false)
}

// DecodeFloat64 parses JSON as Decode does, but converts every number to
// float64 — reproducing JSON.parse exactly, including its loss of precision
// past 2^53.
//
// Use it when byte-identical agreement with a JavaScript round-trip matters
// more than the value: re-serialising a body whose hash the JavaScript side
// also computes. Use [Decode] everywhere else.
//
// This is goenvoy's behaviour (ordered/json.go:60-65), which it has because its
// engine type-asserts float64 at every call site.
func DecodeFloat64(data []byte) (any, error) {
	return decode(data, true)
}

func decode(data []byte, asFloat bool) (any, error) {
	dec := json.NewDecoder(bytes.NewReader(data))
	dec.UseNumber()
	value, err := decodeValue(dec, asFloat)
	if err != nil {
		return nil, err
	}
	// Reject trailing content, as json.Unmarshal does — a decoder left
	// mid-stream would silently accept `{"a":1} garbage`.
	if _, err := dec.Token(); !errors.Is(err, io.EOF) {
		return nil, ErrTrailingContent
	}
	return value, nil
}

func decodeValue(dec *json.Decoder, asFloat bool) (any, error) {
	token, err := dec.Token()
	if err != nil {
		return nil, err
	}
	return decodeFrom(dec, token, asFloat)
}

func decodeFrom(dec *json.Decoder, token json.Token, asFloat bool) (any, error) {
	switch t := token.(type) {
	case json.Delim:
		switch t {
		case '{':
			return decodeObject(dec, asFloat)
		case '[':
			return decodeArray(dec, asFloat)
		}
		return nil, fmt.Errorf("jsorder: unexpected %q", t)
	case json.Number:
		if !asFloat {
			return t, nil
		}
		f, err := strconv.ParseFloat(t.String(), 64)
		if err != nil {
			return nil, fmt.Errorf("jsorder: invalid JSON number %q: %w", t.String(), err)
		}
		return f, nil
	case string, bool:
		return t, nil
	case nil:
		return Null, nil
	}
	return token, nil
}

type pair struct {
	key   string
	value any
}

// pairPool holds the scratch used to accumulate one object's entries.
//
// The entries are collected here rather than written straight into a Map
// because the final ORDER is not known until the last key has been read, and
// building a Map and then rebuilding it in order — which is what JSOrder does —
// allocates a second map, a second key slice and a second hash table for every
// object that has a numeric key. That shape is not exotic: an integration keyed
// by numeric custom-field id produces it on every record of every page.
var pairPool = sync.Pool{
	New: func() any {
		s := make([]pair, 0, 16)
		return &s
	},
}

// linearScanLimit is where duplicate-key detection stops scanning and builds an
// index. Duplicate keys are rare, so the scan is normally a few comparisons;
// without the fallback a hostile 100k-key object would be quadratic.
const linearScanLimit = 16

func decodeObject(dec *json.Decoder, asFloat bool) (*Map, error) {
	scratch := pairPool.Get().(*[]pair)
	pairs := (*scratch)[:0]
	defer func() {
		// Clear before returning it: the values are `any` and would otherwise
		// keep a whole decoded document alive in the pool.
		clear(pairs)
		*scratch = pairs[:0]
		pairPool.Put(scratch)
	}()

	var seen map[string]int
	hasIndex := false

	for dec.More() {
		keyToken, err := dec.Token()
		if err != nil {
			return nil, err
		}
		key, ok := keyToken.(string)
		if !ok {
			return nil, fmt.Errorf("jsorder: non-string object key")
		}
		value, err := decodeValue(dec, asFloat)
		if err != nil {
			return nil, err
		}

		// A repeated key keeps its first POSITION and its last VALUE, which is
		// what a JavaScript object literal and JSON.parse both do.
		if at, dup := findKey(pairs, seen, key); dup {
			pairs[at].value = value
			continue
		}
		if seen == nil && len(pairs) >= linearScanLimit {
			seen = make(map[string]int, 2*len(pairs))
			for i, p := range pairs {
				seen[p.key] = i
			}
		}
		if seen != nil {
			seen[key] = len(pairs)
		}
		pairs = append(pairs, pair{key, value})
		if !hasIndex {
			_, hasIndex = IsArrayIndex(key)
		}
	}
	if _, err := dec.Token(); err != nil { // closing '}'
		return nil, err
	}

	// Enumeration order is JavaScript's, not the document's: array-index keys
	// come first in numeric order. Applied once here, at the boundary where
	// external JSON becomes an object, rather than at every read.
	if hasIndex {
		slices.SortStableFunc(pairs, func(a, b pair) int { return compareKeys(a.key, b.key) })
	}

	// Sized exactly, so neither the key slice nor the hash table grows.
	m := NewCap(len(pairs))
	for _, p := range pairs {
		m.Set(p.key, p.value)
	}
	return m, nil
}

func findKey(pairs []pair, seen map[string]int, key string) (int, bool) {
	if seen != nil {
		at, ok := seen[key]
		return at, ok
	}
	for i := range pairs {
		if pairs[i].key == key {
			return i, true
		}
	}
	return 0, false
}

func decodeArray(dec *json.Decoder, asFloat bool) ([]any, error) {
	// Non-nil so an empty array marshals as [] rather than null.
	out := []any{}
	for dec.More() {
		value, err := decodeValue(dec, asFloat)
		if err != nil {
			return nil, err
		}
		out = append(out, value)
	}
	if _, err := dec.Token(); err != nil { // closing ']'
		return nil, err
	}
	return out, nil
}
