package jsorder

import (
	"unicode/utf8"
)

const hexDigits = "0123456789abcdef"

// AppendString appends s to dst as a quoted JSON string, byte-identical to
// JSON.stringify(s).
//
// Written by hand rather than delegating to encoding/json because
// Encoder.SetEscapeHTML(false) is necessary and not sufficient. Three further
// differences, each of which changes a hash:
//
//   - Go escapes U+2028 (LINE SEPARATOR) and U+2029 (PARAGRAPH SEPARATOR) even
//     with HTML escaping off. JSON.stringify emits them literally. Go does this
//     so its output can be embedded in a JavaScript source file; that is a
//     different problem from producing the same bytes as JavaScript.
//   - Go spells backspace and form feed as six-character \u escapes. JavaScript uses the
//     short forms \b and \f. It uses the short forms for \n, \r and \t too,
//     which is the part that makes the difference easy to miss.
//   - Go escapes DEL (U+007F) in some paths. JSON.stringify never does.
//
// Lone surrogates: JavaScript strings are UTF-16 and, since the well-formed
// JSON.stringify proposal (ES2019), an unpaired surrogate is emitted as an
// escape rather than as invalid output. A Go string holding the WTF-8 encoding
// of a surrogate (ED A0..BF xx) is escaped the same way, so a value that came
// from a WTF-8-aware source round-trips. Genuinely invalid UTF-8 becomes
// U+FFFD, matching encoding/json — see DIVERGENCES J-04.
func AppendString(dst []byte, s string) []byte {
	dst = append(dst, '"')
	start := 0
	for i := 0; i < len(s); {
		if c := s[i]; c < utf8.RuneSelf {
			if c >= 0x20 && c != '"' && c != '\\' {
				i++
				continue
			}
			dst = append(dst, s[start:i]...)
			switch c {
			case '"':
				dst = append(dst, '\\', '"')
			case '\\':
				dst = append(dst, '\\', '\\')
			case '\b':
				dst = append(dst, '\\', 'b')
			case '\f':
				dst = append(dst, '\\', 'f')
			case '\n':
				dst = append(dst, '\\', 'n')
			case '\r':
				dst = append(dst, '\\', 'r')
			case '\t':
				dst = append(dst, '\\', 't')
			default:
				// The remaining C0 controls have no short form.
				dst = append(dst, '\\', 'u', '0', '0', hexDigits[c>>4], hexDigits[c&0xF])
			}
			i++
			start = i
			continue
		}

		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			dst = append(dst, s[start:i]...)
			// A WTF-8 encoded lone surrogate is three bytes ED A0..BF 80..BF.
			// Recover it rather than destroying it, so a surrogate that
			// survived the trip in still survives the trip out.
			if cp, ok := decodeSurrogate(s[i:]); ok {
				dst = appendUnicodeEscape(dst, cp)
				i += 3
			} else {
				dst = append(dst, "�"...)
				i++
			}
			start = i
			continue
		}
		// Everything else — including <, >, &, DEL, U+2028 and U+2029 — is
		// emitted literally, which is what JSON.stringify does.
		i += size
	}
	dst = append(dst, s[start:]...)
	return append(dst, '"')
}

// decodeSurrogate reads a WTF-8 encoded unpaired surrogate, which utf8's
// decoder rejects, and returns its code point.
func decodeSurrogate(s string) (rune, bool) {
	if len(s) < 3 || s[0] != 0xED {
		return 0, false
	}
	if s[1] < 0xA0 || s[1] > 0xBF || s[2] < 0x80 || s[2] > 0xBF {
		return 0, false
	}
	return rune(s[0]&0x0F)<<12 | rune(s[1]&0x3F)<<6 | rune(s[2]&0x3F), true
}

func appendUnicodeEscape(dst []byte, r rune) []byte {
	return append(dst, '\\', 'u',
		hexDigits[(r>>12)&0xF], hexDigits[(r>>8)&0xF],
		hexDigits[(r>>4)&0xF], hexDigits[r&0xF])
}
