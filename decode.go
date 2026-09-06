package jsorder

import (
	"bytes"
	"encoding/json"
	"encoding/json/jsontext"
	"errors"
	"fmt"
	"io"
	"slices"
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

// decodeOptions restores JSON.parse's permissiveness, which jsontext does not
// have by default because it implements RFC 7493 rather than RFC 8259.
//
//   - Duplicate keys: JavaScript keeps the LAST value at the FIRST key's
//     position; jsontext rejects the document outright. [decState.object]
//     applies the JavaScript rule, so allowing them here is all that is needed.
//   - Invalid UTF-8: JavaScript replaces a malformed sequence with U+FFFD
//     rather than failing the parse, and so does jsontext under this option.
//     That replacement is also why a lone surrogate cannot round-trip — see
//     DIVERGENCES J-04, which jsontext does not change.
//
// A package-level slice rather than a literal at each call site so that passing
// it variadically to Reset costs nothing.
var decodeOptions = []jsontext.Options{
	jsontext.AllowDuplicateNames(true),
	jsontext.AllowInvalidUTF8(true),
}

// decState holds the reader and the scratch shared by one Decode call.
//
// It reads through encoding/json/jsontext rather than encoding/json's
// Decoder.Token. Both are standard library and both preserve order; the
// difference is that Token returns `any`, so every string, number, delimiter
// and boolean is boxed into an interface on the way out. A memory profile of a
// 1000-record page put 52% of the decoder's allocations in Decoder.Token alone.
// jsontext.Token is a struct, so the same walk allocates only for the values
// that genuinely escape.
type decState struct {
	dec     *jsontext.Decoder
	pairs   []pair
	asFloat bool

	// park is where the decoder is pointed on release, so that a pooled state
	// does not keep the caller's last document alive. Resetting onto a
	// *bytes.Buffer drops the decoder's own buffer, which is the aliased one.
	park bytes.Buffer
}

var decStatePool = sync.Pool{New: func() any { return new(decState) }}

func decode(data []byte, asFloat bool) (any, error) {
	s := decStatePool.Get().(*decState)
	s.asFloat = asFloat

	// bytes.Buffer, not bytes.Reader: jsontext parses a *bytes.Buffer in place
	// rather than copying it into an intermediate buffer (decode.go:119). On a
	// 145 KB page that is a whole memcpy per call, traded for one 32-byte
	// header.
	r := bytes.NewBuffer(data)
	if s.dec == nil {
		s.dec = jsontext.NewDecoder(r, decodeOptions...)
	} else {
		s.dec.Reset(r, decodeOptions...)
	}

	value, err := s.document()
	s.release()
	if err != nil {
		return nil, err
	}
	return value, nil
}

func (s *decState) document() (any, error) {
	tok, err := s.dec.ReadToken()
	if err != nil {
		return nil, err
	}
	value, err := s.value(tok)
	if err != nil {
		return nil, err
	}
	// Reject trailing content, as json.Unmarshal does — a decoder left
	// mid-stream would silently accept `{"a":1} garbage`.
	if _, err := s.dec.ReadToken(); !errors.Is(err, io.EOF) {
		return nil, ErrTrailingContent
	}
	return value, nil
}

func (s *decState) release() {
	s.dec.Reset(&s.park, decodeOptions...)
	// The values are `any` and would otherwise keep a whole decoded document
	// alive in the pool.
	clear(s.pairs)
	s.pairs = s.pairs[:0]
	decStatePool.Put(s)
}

func (s *decState) value(tok jsontext.Token) (any, error) {
	switch tok.Kind() {
	case '"':
		return tok.String(), nil
	case '0':
		if !s.asFloat {
			// Token.String on a number returns the raw literal, which is
			// exactly what json.Number has to hold (D-0017).
			return json.Number(tok.String()), nil
		}
		f, err := tok.Float()
		if err != nil {
			return nil, fmt.Errorf("jsorder: invalid JSON number %q: %w", tok.String(), err)
		}
		return f, nil
	case '{':
		return s.object()
	case '[':
		return s.array()
	case 't':
		return true, nil
	case 'f':
		return false, nil
	case 'n':
		return Null, nil
	}
	return nil, fmt.Errorf("jsorder: unexpected %q", byte(tok.Kind()))
}

type pair struct {
	key   string
	value any
}

// object reads members until the closing brace, whose opening the caller has
// already consumed.
//
// The entries are collected on a shared stack rather than written straight into
// a Map, because the final ORDER is not known until the last key has been read,
// and building a Map and then rebuilding it in order — which is what JSOrder
// does — allocates a second map, a second key slice and a second hash table for
// every object that has a numeric key. That shape is not exotic: an integration
// keyed by numeric custom-field id produces it on every record of every page.
//
// One stack for the whole document, indexed by depth, rather than a pooled
// slice per object: nested objects simply take a higher window of the same
// backing array, so a page of 1000 records borrows it 2000 times and allocates
// once.
//
// Duplicate keys are NOT detected here, and that is deliberate rather than an
// omission. A repeated key must keep its first POSITION and its last VALUE,
// which is what a JavaScript object literal and JSON.parse both do — and it is
// also exactly what Map.Set does, since Set appends to the key order only when
// the key is new. Staging both occurrences and letting Set collapse them gives
// the same object as scanning for the duplicate first, for any ordering,
// because compareKeys returns 0 for two copies of one key and the sort is
// stable. The scan it replaces was a few string comparisons per member on every
// object in every document, to serve a case that is essentially absent from
// real payloads.
func (s *decState) object() (*Map, error) {
	base := len(s.pairs)
	hasIndex := false

	for {
		tok, err := s.dec.ReadToken()
		if err != nil {
			return nil, err
		}
		if tok.Kind() == '}' {
			break
		}
		if tok.Kind() != '"' {
			return nil, fmt.Errorf("jsorder: non-string object key")
		}
		key := tok.String()

		valueTok, err := s.dec.ReadToken()
		if err != nil {
			return nil, err
		}
		value, err := s.value(valueTok)
		if err != nil {
			return nil, err
		}

		s.pairs = append(s.pairs, pair{key, value})
		if !hasIndex {
			_, hasIndex = IsArrayIndex(key)
		}
	}

	entries := s.pairs[base:]

	// Enumeration order is JavaScript's, not the document's: array-index keys
	// come first in numeric order. Applied once here, at the boundary where
	// external JSON becomes an object, rather than at every read.
	if hasIndex {
		slices.SortStableFunc(entries, func(a, b pair) int { return compareKeys(a.key, b.key) })
	}

	// Sized so neither the key slice nor the hash table grows. Exact unless the
	// object repeated a key, in which case it is an upper bound and the map is
	// slightly over-allocated — the price of not scanning for duplicates.
	m := NewCap(len(entries))
	for _, p := range entries {
		m.Set(p.key, p.value)
	}

	clear(entries)
	s.pairs = s.pairs[:base]
	return m, nil
}

func (s *decState) array() ([]any, error) {
	// Non-nil so an empty array marshals as [] rather than null.
	out := []any{}
	for {
		tok, err := s.dec.ReadToken()
		if err != nil {
			return nil, err
		}
		if tok.Kind() == ']' {
			return out, nil
		}
		value, err := s.value(tok)
		if err != nil {
			return nil, err
		}
		out = append(out, value)
	}
}
