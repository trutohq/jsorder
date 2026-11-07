package jsorder

import (
	"maps"
	"slices"

	"github.com/recolabs/gnata"
)

// Map is a JSON object with ordered keys.
//
// A type ALIAS to gnata.OrderedMap, not a definition (D-0014). The alias is
// load-bearing, not stylistic: gnata's evaluator type-switches on
// *evaluator.OrderedMap in 30 places, so a distinct Go type — however
// identical its fields — would stop being recognised as ordered input and get
// SORTED on the way through an expression. That is the bug this package exists
// to close, reintroduced one layer up.
//
// The cost is real and measured: aliasing links all of gnata, about +3.4 MB of
// binary. See DECISIONS D-0015 for why that was accepted anyway.
//
// A Map is not safe for concurrent writes. Build a new one rather than mutating
// one you did not construct; Keys() returns the map's internal slice.
type Map = gnata.OrderedMap

// Null is the JSON null sentinel.
//
// JSON null decodes to this, NEVER to Go nil, because Go nil is spent on
// JavaScript's `undefined`. Conflating them makes $exists() false for a field a
// document explicitly set to null.
var Null = gnata.Null

// JSONNull is the type of [Null].
type JSONNull = gnata.JSONNull

// IsNull reports whether v is the JSON null sentinel. It is false for Go nil,
// which means undefined.
func IsNull(v any) bool {
	_, ok := v.(JSONNull)
	return ok
}

// New returns an empty Map.
func New() *Map { return gnata.NewOrderedMap() }

// NewCap returns an empty Map sized for n entries.
func NewCap(n int) *Map { return gnata.NewOrderedMapWithCapacity(n) }

// Pairs builds a Map from alternating key/value arguments, in the order given.
// It is the readable way to write an object literal in Go.
//
// The keys are stored in the order supplied; [MarshalJS] applies JavaScript's
// enumeration order at the boundary, so a caller does not have to pre-sort
// numeric keys to get JavaScript's bytes.
func Pairs(kv ...any) *Map {
	m := NewCap(len(kv) / 2)
	for i := 0; i+1 < len(kv); i += 2 {
		key, ok := kv[i].(string)
		if !ok {
			continue
		}
		m.Set(key, kv[i+1])
	}
	return m
}

// FromMap converts a plain map into a Map with its keys SORTED.
//
// Sorted rather than arbitrary because the alternative is Go's randomised map
// iteration, which would make the same input produce different output between
// runs. It is a lossy conversion — the original order is already gone — so
// prefer building a Map at the point the data is created.
func FromMap(m map[string]any) *Map {
	out := NewCap(len(m))
	for _, k := range slices.Sorted(maps.Keys(m)) {
		out.Set(k, m[k])
	}
	return out
}

// IsObject reports whether v is an object in either representation.
func IsObject(v any) bool {
	switch v.(type) {
	case *Map, map[string]any:
		return true
	}
	return false
}

// Clone returns a deep copy, preserving key order.
//
// Values outside the JSON model are returned as-is; copying them would be
// meaningless.
func Clone(v any) any {
	switch t := v.(type) {
	case *Map:
		out := NewCap(t.Len())
		t.Range(func(key string, value any) bool {
			out.Set(key, Clone(value))
			return true
		})
		return out
	case map[string]any:
		out := make(map[string]any, len(t))
		for k, item := range t {
			out[k] = Clone(item)
		}
		return out
	case []any:
		out := make([]any, len(t))
		for i, item := range t {
			out[i] = Clone(item)
		}
		return out
	}
	return v
}

// Set returns a copy of the object with key set to value, preserving order.
// An existing key keeps its POSITION, which is what JavaScript's
// `{...record, [k]: v}` does.
func Set(v any, key string, value any) *Map {
	var src *Map
	switch t := v.(type) {
	case *Map:
		src = t
	case map[string]any:
		src = FromMap(t)
	}

	out := NewCap(srcLen(src) + 1)
	if src != nil {
		src.Range(func(k string, val any) bool {
			out.Set(k, val)
			return true
		})
	}
	out.Set(key, value)
	return out
}

func srcLen(m *Map) int {
	if m == nil {
		return 0
	}
	return m.Len()
}

// Keys returns the keys of an object value in JavaScript's enumeration order,
// which is what Object.keys would report. A plain map has no insertion order to
// report, so its keys are sorted first and then JS-ordered.
func Keys(v any) []string {
	switch t := v.(type) {
	case *Map:
		if t == nil {
			return nil
		}
		return KeyOrder(t.Keys())
	case map[string]any:
		return KeyOrder(slices.Sorted(maps.Keys(t)))
	}
	return nil
}
