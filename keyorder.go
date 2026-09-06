package jsorder

import (
	"cmp"
	"slices"
	"strconv"
)

// This file is taken close to verbatim from
// goenvoy/internal/ordered/jsorder.go, which is stdlib-only and implements the
// [[OwnPropertyKeys]] rule exactly. Retained here rather than re-derived,
// because a second derivation is a second chance to get it subtly wrong.
//
// JavaScript does not enumerate an object's keys purely in insertion order.
//
// [[OwnPropertyKeys]] emits ARRAY-INDEX keys first, in ascending NUMERIC order,
// and only then the remaining string keys in insertion order. So
// {"10":a,"2":b,"1":c,"name":d} enumerates as 1, 2, 10, name — through
// Object.keys, JSON.stringify, for...in, and therefore through JSONata's
// $keys, $each, $spread and $string.
//
// This is not a curiosity: an integration keyed by numeric custom-field id
// produces exactly this shape, and the difference decides CSV column order and
// the bytes of every signature computed over the result.

// maxArrayIndex is 2^32-2. A canonical decimal string naming an integer in
// [0, maxArrayIndex] is an array index; anything above it is an ordinary
// string key, which is why the boundary matters rather than "looks numeric".
//
// Verified against the reference: in "4294967294" is an index and "4294967295"
// is not, so the two sort into different groups.
const maxArrayIndex = uint64(1)<<32 - 2

// IsArrayIndex reports whether a key is an array index by the spec's rule.
//
// The test is on the CANONICAL representation: "01", "1.0", "+1", " 1" and "-1"
// are ordinary keys, because converting them to a number and back does not
// reproduce them.
func IsArrayIndex(key string) (uint64, bool) {
	if key == "" || len(key) > 10 {
		return 0, false
	}
	if key == "0" {
		return 0, true
	}
	// A leading zero means the string is not canonical.
	if key[0] < '1' || key[0] > '9' {
		return 0, false
	}
	n, err := strconv.ParseUint(key, 10, 64)
	if err != nil || n > maxArrayIndex {
		return 0, false
	}
	return n, true
}

// KeyOrder returns keys in JavaScript's enumeration order.
//
// It returns the input slice UNCHANGED when no key is an array index, which is
// the overwhelmingly common case — so the hot path costs one scan and no
// allocation. Callers must not mutate a returned slice they did not own,
// because it may be the one they passed in.
func KeyOrder(keys []string) []string {
	ordered, _ := keyOrder(keys)
	return ordered
}

// keyOrder additionally reports whether it had to reorder anything, which lets
// [JSOrder] and the marshallers skip work without comparing slice identities.
func keyOrder(keys []string) (ordered []string, reordered bool) {
	hasIndex := false
	for _, key := range keys {
		if _, ok := IsArrayIndex(key); ok {
			hasIndex = true
			break
		}
	}
	if !hasIndex {
		return keys, false
	}

	// ONE allocation: copy, then a STABLE in-place sort. Stable is what makes
	// the second half of the rule work — every pair of non-index keys compares
	// equal, so they keep the order they arrived in, which is what
	// [[OwnPropertyKeys]] requires for string keys. slices.SortFunc would
	// shuffle them, and the earlier partition-into-two-slices version of this
	// cost three allocations to say the same thing.
	out := make([]string, len(keys))
	copy(out, keys)
	slices.SortStableFunc(out, compareKeys)
	return out, true
}

// compareKeys orders two object keys the way [[OwnPropertyKeys]] does: array
// indices first in ascending numeric order, everything else left where it is.
func compareKeys(a, b string) int {
	na, ia := IsArrayIndex(a)
	nb, ib := IsArrayIndex(b)
	switch {
	case ia && ib:
		return cmp.Compare(na, nb)
	case ia:
		return -1
	case ib:
		return 1
	}
	return 0 // both ordinary: equal, so a stable sort preserves insertion order
}

// JSOrder rebuilds a map in JavaScript's own enumeration order.
//
// Returns the map unchanged when it holds no array-index keys.
func JSOrder(m *Map) *Map {
	if m == nil {
		return nil
	}
	ordered, reordered := keyOrder(m.Keys())
	if !reordered {
		return m
	}

	out := NewCap(len(ordered))
	for _, key := range ordered {
		value, _ := m.Get(key)
		out.Set(key, value)
	}
	return out
}
