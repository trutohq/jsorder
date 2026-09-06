// Golden generator. Run with `bun internal/gen/generate.mjs` (node works too).
//
// Everything under testdata/ is produced by EXECUTING JavaScript, never by
// hand. An expected value nobody watched the reference produce is a guess with
// a test around it.
//
// Writes three files:
//   testdata/numbers.jsonl    float64 bits -> Number::toString / JSON.stringify
//   testdata/strings.jsonl    code-unit sequence -> JSON.stringify
//   testdata/stringify.jsonl  JSON text -> JSON.stringify(JSON.parse(text))

import { writeFileSync, mkdirSync } from "node:fs";
import { dirname, join } from "node:path";
import { fileURLToPath } from "node:url";

const root = join(dirname(fileURLToPath(import.meta.url)), "..", "..");
const outDir = join(root, "testdata");
mkdirSync(outDir, { recursive: true });

const dv = new DataView(new ArrayBuffer(8));

// bitsOf returns the IEEE-754 bit pattern as a 16-hex-digit string, so Go can
// reconstruct the EXACT same float64. Comparing decimal text would beg the
// question the test is asking.
function bitsOf(n) {
  dv.setFloat64(0, n);
  return dv.getBigUint64(0).toString(16).padStart(16, "0");
}

// ---------------------------------------------------------------- numbers

const adversarial = [
  0, -0, 1, -1, 2, 10, 100, 1000,
  0.1, 0.2, 0.1 + 0.2, 0.3, 1 / 3, 2 / 3, 4.35, 1.005,
  // The exponential-notation thresholds. ECMAScript switches at n > 21 and
  // n <= -6; Go switches somewhere else entirely.
  1e15, 1e16, 1e17, 1e18, 1e19, 1e20, 1e21, 1e22, 1e23,
  -1e20, -1e21,
  1e-1, 1e-5, 1e-6, 1e-7, 1e-8, 1e-10, 1e-20,
  9.999999999999999e20, 1.0000000000000001e21,
  // Denormals and the extremes.
  5e-324, 1e-323, 2.2250738585072014e-308, 2.2250738585072009e-308,
  1.7976931348623157e308, -1.7976931348623157e308,
  // The integer-precision boundary.
  2 ** 53 - 1, 2 ** 53, 2 ** 53 + 2, -(2 ** 53),
  2 ** 31, 2 ** 32 - 1, 2 ** 32, 2 ** 63,
  // Values with long shortest-representations.
  1.2345678901234567e-5, 1.23e-10, 123456789012345678901234,
  5.960464477539063e-8, 1.7e-308,
  // Non-finite: Number::toString and JSON.stringify disagree about these.
  NaN, Infinity, -Infinity,
];

// A deterministic PRNG, so re-running the generator does not churn the goldens.
let seed = 0x2545f491;
function rnd() {
  seed ^= seed << 13; seed >>>= 0;
  seed ^= seed >>> 17;
  seed ^= seed << 5; seed >>>= 0;
  return seed / 0x100000000;
}

const random = [];
// Random doubles from raw bit patterns: hits denormals, huge exponents and
// digit strings no human would think to write.
for (let i = 0; i < 3000; i++) {
  const hi = Math.floor(rnd() * 0x100000000);
  const lo = Math.floor(rnd() * 0x100000000);
  dv.setUint32(0, hi);
  dv.setUint32(4, lo);
  random.push(dv.getFloat64(0));
}
// Random values across the decimal ranges that real payloads actually contain.
for (let i = 0; i < 2000; i++) {
  const exp = Math.floor(rnd() * 45) - 22;
  random.push((rnd() * 2 - 1) * 10 ** exp);
}
// Random integers, the common case.
for (let i = 0; i < 1000; i++) {
  random.push(Math.floor(rnd() * 2 ** Math.floor(rnd() * 60)));
}

const numberLines = [];
for (const n of [...adversarial, ...random]) {
  numberLines.push(JSON.stringify({
    bits: bitsOf(n),
    str: String(n),              // Number::toString
    json: JSON.stringify(n) ?? "null",  // stringify substitutes null for non-finite
  }));
}
writeFileSync(join(outDir, "numbers.jsonl"), numberLines.join("\n") + "\n");

// ---------------------------------------------------------------- strings

const stringCases = [];
function addString(s) {
  // units records the UTF-16 code units, so Go reconstructs the same string
  // (including lone surrogates) rather than trusting this file's own encoding.
  const units = [];
  for (let i = 0; i < s.length; i++) units.push(s.charCodeAt(i));
  stringCases.push(JSON.stringify({ units, out: JSON.stringify(s) }));
}

addString("");
addString("hello");
addString("<>&");                       // Go escapes these by default
addString("</script>");
addString("a\u2028b\u2029c");           // Go escapes these even with SetEscapeHTML(false)
addString("\b\f\n\r\t\v\0");            // short forms vs \u escapes
addString("\u007f");                    // DEL: Go escapes it in some paths
addString('quote" back\\ slash');
addString("é ü ß");
addString("😀");              // emoji, a valid surrogate PAIR
addString("\ud800");                    // lone HIGH surrogate
addString("\udfff");                    // lone LOW surrogate
addString("a\ud800b");
addString("\ud800\ud800");
addString("\udc00\ud800");              // reversed pair: still two lone surrogates
addString("\u0000\u0001\u001f ");
addString("naïve café 日本語 עברית");
addString("line1\nline2\r\nline3");
for (let cp = 0; cp < 0x400; cp++) addString(String.fromCharCode(cp));
for (let i = 0; i < 400; i++) {
  let s = "";
  const len = 1 + Math.floor(rnd() * 8);
  for (let j = 0; j < len; j++) s += String.fromCharCode(Math.floor(rnd() * 0x11000));
  addString(s);
}
writeFileSync(join(outDir, "strings.jsonl"), stringCases.join("\n") + "\n");

// ---------------------------------------------------------------- documents

const documents = [
  // [[OwnPropertyKeys]]: array-index keys first, ascending numeric.
  '{"10":"a","2":"b","1":"c","name":"d"}',
  '{"b":1,"2":2,"1":3}',
  '{"0":"z","4294967294":"max","4294967295":"over","-1":"neg","01":"lead","1.0":"dot","+1":"plus"," 1":"space"}',
  '{"9999999999":1,"10000000000":2,"999":3}',
  '{"name":"x","10":1,"9":2,"100":3,"0":4,"zz":5,"aa":6}',
  // Nesting: the rule applies at every level.
  '{"a":{"2":1,"1":2,"x":3},"b":[{"3":1,"1":2}]}',
  '[{"2":"a","1":"b"},{"z":1,"0":2}]',
  // Duplicate keys: first position, last value.
  '{"a":1,"b":2,"a":3}',
  '{"2":1,"1":2,"2":3}',
  // Escaping and the HTML characters.
  '{"html":"<div>&amp;</div>","script":"</script>"}',
  '{"sep":"a\\u2028b\\u2029c"}',
  '{"ctl":"\\u0000\\u0008\\u000c\\u001f"}',
  // Numbers as JSON.parse sees them.
  '{"a":1e21,"b":1e20,"c":1e-7,"d":1e-6,"e":-0,"f":0.1}',
  '{"big":12345678901234567890,"safe":9007199254740991,"unsafe":9007199254740993}',
  '{"neg":-0.0,"exp":1.5e300,"tiny":5e-324}',
  '[0.1,0.2,0.30000000000000004,1/3]'.replace("1/3", "0.3333333333333333"),
  // Null, empty containers, mixed types.
  '{"n":null,"arr":[],"obj":{},"s":"","b":false}',
  '[null,null,[],{}]',
  // Scalars at the top level.
  "null", "true", "false", "0", "-0", '"str"', "1e21", "[]", "{}",
  // A realistic Truto-shaped payload: a webhook body, which is the thing whose
  // signature must not change.
  JSON.stringify({
    event_type: "record.created",
    id: "01927f3e-8f1a-7c3d-9e4b-2a1f5c8d7e6f",
    integrated_account_id: "ia_9f8e7d6c",
    data: {
      id: 1234567890123,
      name: "Acme <Corp> & Sons",
      amount: 1234.56,
      tags: ["a", "b"],
      custom_fields: { "10": "ten", "2": "two", "1": "one", label: "l" },
      nested: { deep: { deeper: [1, 2, { x: null }] } },
    },
    created_at: "2026-09-06T12:00:00.000Z",
  }),
];

// Randomly generated documents, so the corpus is not limited to what was
// thought of. Key names deliberately over-sample numeric-looking strings.
function randomKey() {
  const r = rnd();
  if (r < 0.35) return String(Math.floor(rnd() * 30));
  if (r < 0.45) return String(Math.floor(rnd() * 5000000000));
  if (r < 0.5) return "0" + Math.floor(rnd() * 100);
  return "k" + Math.floor(rnd() * 26).toString(36);
}
function randomValue(depth) {
  const r = rnd();
  if (depth > 3 || r < 0.35) {
    const s = rnd();
    if (s < 0.25) return Math.floor(rnd() * 1e6);
    if (s < 0.4) return (rnd() * 2 - 1) * 10 ** (Math.floor(rnd() * 30) - 15);
    if (s < 0.6) return "s" + Math.floor(rnd() * 1000) + "<&>";
    if (s < 0.7) return null;
    if (s < 0.85) return rnd() < 0.5;
    return "";
  }
  if (r < 0.7) {
    const o = {};
    const n = Math.floor(rnd() * 7);
    for (let i = 0; i < n; i++) o[randomKey()] = randomValue(depth + 1);
    return o;
  }
  const a = [];
  const n = Math.floor(rnd() * 5);
  for (let i = 0; i < n; i++) a.push(randomValue(depth + 1));
  return a;
}
for (let i = 0; i < 600; i++) documents.push(JSON.stringify(randomValue(0)));

const docLines = [];
for (const text of documents) {
  const parsed = JSON.parse(text);
  docLines.push(JSON.stringify({
    in: text,
    out: JSON.stringify(parsed),
    // Top-level enumeration order, checked separately so a failure says
    // whether it was ordering or encoding that broke.
    keys: parsed && typeof parsed === "object" && !Array.isArray(parsed)
      ? Object.keys(parsed)
      : null,
  }));
}
writeFileSync(join(outDir, "stringify.jsonl"), docLines.join("\n") + "\n");

console.log(
  `numbers=${numberLines.length} strings=${stringCases.length} documents=${docLines.length}`,
);
