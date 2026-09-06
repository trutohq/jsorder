package jsorder

import (
	"encoding/json"
	"fmt"
	"math"
	"slices"
	"strconv"
	"sync"
)

// MarshalJS encodes v byte-identically to JavaScript's JSON.stringify(v).
//
// Use it for anything on the wire, and for anything hashed or signed whose
// JavaScript counterpart hashes JSON.stringify output — webhook signatures,
// pagination loop-detection hashes, cursor payloads. Reaching for
// [StableMarshal] there produces a different hash and no error (D-0008).
//
// Key order is JavaScript's: array-index keys first in ascending numeric order,
// then the remaining keys in insertion order. A plain map[string]any has no
// insertion order to report, so its keys are sorted before that rule is
// applied; prefer a [Map] wherever the order came from data.
//
// undefined follows JSON.stringify exactly, which is not obvious and is
// observable:
//
//	MarshalJS(Pairs("a", nil, "b", 1))  ->  {"b":1}     key OMITTED
//	MarshalJS([]any{nil, 1})            ->  [null,1]    element becomes null
//
// A top-level nil is the one case with no JavaScript answer: JSON.stringify
// returns the undefined VALUE, not a string, and there are no bytes to return.
// This writes `null`. See DIVERGENCES J-03.
//
// NaN and the infinities become `null`, as JSON.stringify does.
func MarshalJS(v any) ([]byte, error) {
	e := encoder{stable: false}
	buf, err := e.value(make([]byte, 0, 256), v)
	if err != nil {
		return nil, err
	}
	return buf, nil
}

// StableMarshal encodes v with every object's keys sorted bytewise, at every
// level.
//
// For hashing and content addressing INTERNAL to Go, where the input is a Go
// value with no JavaScript peer and all that is needed is that the same value
// always produces the same bytes.
//
// It is NOT JavaScript's order and must never go on the wire: bytewise sorting
// puts "10" before "2", which is the opposite of what JavaScript does. Numbers,
// strings and undefined are treated exactly as [MarshalJS] treats them, so the
// key order is the only difference between the two.
func StableMarshal(v any) ([]byte, error) {
	e := encoder{stable: true}
	buf, err := e.value(make([]byte, 0, 256), v)
	if err != nil {
		return nil, err
	}
	return buf, nil
}

// AppendJS appends the MarshalJS encoding of v to dst, for callers feeding a
// hash and wanting to keep one buffer.
func AppendJS(dst []byte, v any) ([]byte, error) {
	e := encoder{stable: false}
	return e.value(dst, v)
}

type encoder struct {
	stable bool
	depth  int
}

// maxDepth bounds recursion. JSON.stringify throws a RangeError on a cyclic or
// pathologically deep value; a Go library must not blow the stack on hostile
// input, and a fuzzer finds this within seconds.
const maxDepth = 10000

func (e *encoder) value(dst []byte, v any) ([]byte, error) {
	switch t := v.(type) {
	case nil:
		// Reached only at the top level or as an array element; an object key
		// holding nil is dropped before it gets here.
		return append(dst, "null"...), nil
	case JSONNull:
		return append(dst, "null"...), nil
	case bool:
		if t {
			return append(dst, "true"...), nil
		}
		return append(dst, "false"...), nil
	case string:
		return AppendString(dst, t), nil
	case float64:
		return appendJSONNumber(dst, t), nil
	case float32:
		return appendJSONNumber(dst, float64(t)), nil
	case json.Number:
		return appendNumberLiteral(dst, t)
	case int:
		return strconv.AppendInt(dst, int64(t), 10), nil
	case int8:
		return strconv.AppendInt(dst, int64(t), 10), nil
	case int16:
		return strconv.AppendInt(dst, int64(t), 10), nil
	case int32:
		return strconv.AppendInt(dst, int64(t), 10), nil
	case int64:
		return strconv.AppendInt(dst, t, 10), nil
	case uint:
		return strconv.AppendUint(dst, uint64(t), 10), nil
	case uint8:
		return strconv.AppendUint(dst, uint64(t), 10), nil
	case uint16:
		return strconv.AppendUint(dst, uint64(t), 10), nil
	case uint32:
		return strconv.AppendUint(dst, uint64(t), 10), nil
	case uint64:
		return strconv.AppendUint(dst, t, 10), nil
	case *Map:
		return e.orderedMap(dst, t)
	case map[string]any:
		return e.plainMap(dst, t)
	case []any:
		return e.array(dst, t)
	}
	// Deliberately no reflection fallback and no delegation to encoding/json.
	// Either would silently reintroduce Go's key sorting, HTML escaping and
	// float formatting — the three bugs this package exists to prevent — for
	// whichever type slipped through. An error names the type instead.
	return nil, fmt.Errorf("jsorder: cannot marshal %T; convert it to the JSON value model first", v)
}

func (e *encoder) enter() error {
	e.depth++
	if e.depth > maxDepth {
		return fmt.Errorf("jsorder: nesting deeper than %d", maxDepth)
	}
	return nil
}

func (e *encoder) orderedMap(dst []byte, m *Map) ([]byte, error) {
	if m == nil {
		return append(dst, "null"...), nil
	}
	if err := e.enter(); err != nil {
		return nil, err
	}
	defer func() { e.depth-- }()

	// Keys() is the Map's OWN slice, so any reordering has to happen on a copy.
	// The copy comes from a pool because it is otherwise the single largest
	// source of allocation in the encoder — a profile of a 1000-record page
	// attributed 98.85% of allocated objects to exactly this line, one per
	// object that had a numeric key.
	keys := m.Keys()
	var scratch *[]string
	switch {
	case e.stable:
		scratch = getKeyScratch(keys)
		slices.Sort(*scratch)
		keys = *scratch
	case hasArrayIndexKey(keys):
		scratch = getKeyScratch(keys)
		slices.SortStableFunc(*scratch, compareKeys)
		keys = *scratch
	}
	if scratch != nil {
		defer putKeyScratch(scratch)
	}

	dst = append(dst, '{')
	first := true
	for _, key := range keys {
		value, _ := m.Get(key)
		if value == nil {
			continue // undefined: JSON.stringify omits the key
		}
		if !first {
			dst = append(dst, ',')
		}
		first = false
		dst = AppendString(dst, key)
		dst = append(dst, ':')
		var err error
		if dst, err = e.value(dst, value); err != nil {
			return nil, err
		}
	}
	return append(dst, '}'), nil
}

func (e *encoder) plainMap(dst []byte, m map[string]any) ([]byte, error) {
	if err := e.enter(); err != nil {
		return nil, err
	}
	defer func() { e.depth-- }()

	// Sorted first because Go map iteration is randomised, so there is no
	// insertion order to honour; then JS-ordered unless this is StableMarshal.
	scratch := keyScratchPool.Get().(*[]string)
	keys := (*scratch)[:0]
	for k := range m {
		keys = append(keys, k)
	}
	slices.Sort(keys)
	if !e.stable && hasArrayIndexKey(keys) {
		slices.SortStableFunc(keys, compareKeys)
	}
	*scratch = keys
	defer putKeyScratch(scratch)

	dst = append(dst, '{')
	first := true
	for _, key := range keys {
		value := m[key]
		if value == nil {
			continue
		}
		if !first {
			dst = append(dst, ',')
		}
		first = false
		dst = AppendString(dst, key)
		dst = append(dst, ':')
		var err error
		if dst, err = e.value(dst, value); err != nil {
			return nil, err
		}
	}
	return append(dst, '}'), nil
}

func (e *encoder) array(dst []byte, a []any) ([]byte, error) {
	if err := e.enter(); err != nil {
		return nil, err
	}
	defer func() { e.depth-- }()

	dst = append(dst, '[')
	for i, item := range a {
		if i > 0 {
			dst = append(dst, ',')
		}
		var err error
		// A nil element is undefined, and JSON.stringify writes null for an
		// undefined array element rather than omitting it — an array's length
		// is observable in a way an object's key set is not.
		if dst, err = e.value(dst, item); err != nil {
			return nil, err
		}
	}
	return append(dst, ']'), nil
}

// appendJSONNumber writes a float the way JSON.stringify does: Number::toString
// for finite values, and `null` for NaN and the infinities, which have no JSON
// representation.
func appendJSONNumber(dst []byte, f float64) []byte {
	if math.IsNaN(f) || math.IsInf(f, 0) {
		return append(dst, "null"...)
	}
	return AppendNumber(dst, f)
}

// appendNumberLiteral writes a json.Number back verbatim.
//
// Verbatim, not reformatted: the entire reason [Decode] preserves json.Number
// is that the literal carries more than a float64 can hold, so canonicalising
// it here would undo the preservation. The literal is validated first, because
// a json.Number is a string type and a caller can construct an invalid one —
// emitting it unchecked would produce a document that is not JSON.
func appendNumberLiteral(dst []byte, n json.Number) ([]byte, error) {
	s := string(n)
	if s == "" {
		return nil, fmt.Errorf("jsorder: empty json.Number")
	}
	if !validJSONNumber(s) {
		return nil, fmt.Errorf("jsorder: invalid json.Number %q", s)
	}
	return append(dst, s...), nil
}

// validJSONNumber reports whether s is a JSON number literal per RFC 8259.
// strconv.ParseFloat is more permissive — it accepts "Inf", "0x1p-2" and a
// leading "+" — so it cannot stand in for this.
func validJSONNumber(s string) bool {
	i := 0
	if i < len(s) && s[i] == '-' {
		i++
	}
	// int: "0" | [1-9][0-9]*
	start := i
	if i < len(s) && s[i] == '0' {
		i++
	} else {
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
	}
	if i == start {
		return false
	}
	// frac
	if i < len(s) && s[i] == '.' {
		i++
		digits := i
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if i == digits {
			return false
		}
	}
	// exp
	if i < len(s) && (s[i] == 'e' || s[i] == 'E') {
		i++
		if i < len(s) && (s[i] == '+' || s[i] == '-') {
			i++
		}
		digits := i
		for i < len(s) && s[i] >= '0' && s[i] <= '9' {
			i++
		}
		if i == digits {
			return false
		}
	}
	return i == len(s)
}

// MarshalJSString is MarshalJS returning a string, for the common
// hash(stringify(v)) call shape.
func MarshalJSString(v any) (string, error) {
	buf, err := MarshalJS(v)
	if err != nil {
		return "", err
	}
	return string(buf), nil
}

// keyScratchPool supplies the reordering buffer used per object. Each level of
// nesting holds its own until its defer runs, so a parent's key order is never
// clobbered by a child's.
var keyScratchPool = sync.Pool{
	New: func() any {
		s := make([]string, 0, 16)
		return &s
	},
}

func getKeyScratch(keys []string) *[]string {
	scratch := keyScratchPool.Get().(*[]string)
	*scratch = append((*scratch)[:0], keys...)
	return scratch
}

func putKeyScratch(scratch *[]string) {
	// Clear before returning it: the header keeps its strings alive otherwise,
	// which for a pooled buffer means holding a document's keys indefinitely.
	clear(*scratch)
	*scratch = (*scratch)[:0]
	keyScratchPool.Put(scratch)
}

// hasArrayIndexKey reports whether reordering is needed at all. The answer is
// no for almost every real object, and then the encoder touches no scratch.
func hasArrayIndexKey(keys []string) bool {
	for _, key := range keys {
		if _, ok := IsArrayIndex(key); ok {
			return true
		}
	}
	return false
}
