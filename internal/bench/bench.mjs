// The JavaScript side of the comparison. Same workload as internal/bench/main.go:
// 200 iterations of JSON.parse + JSON.stringify over a 1000-record page.
//
//   bun internal/bench/bench.mjs
//   node internal/bench/bench.mjs

const RECORDS = 1000;
const ITERATIONS = 200;

function buildPage(n) {
  const page = [];
  for (let i = 0; i < n; i++) {
    page.push({
      id: 1000000 + i,
      external_id: "ext_" + i,
      name: "Record " + i,
      score: i * 1.5,
      active: i % 2 === 0,
      meta: { "2": i, "1": i * 2, source: "api" },
      deleted_at: null,
    });
  }
  return page;
}

const doc = JSON.stringify(buildPage(RECORDS));
console.log(
  `workload: ${RECORDS} records, ${ITERATIONS} iterations, ${doc.length} bytes/document`,
);

// Warm the JIT before measuring: comparing Go against a cold V8 would flatter
// Go for the wrong reason.
for (let i = 0; i < 20; i++) JSON.stringify(JSON.parse(doc));

let decoded;
let start = performance.now();
for (let i = 0; i < ITERATIONS; i++) decoded = JSON.parse(doc);
const decodeElapsed = performance.now() - start;

let out;
start = performance.now();
for (let i = 0; i < ITERATIONS; i++) out = JSON.stringify(decoded);
const encodeElapsed = performance.now() - start;

if (out.length !== doc.length) throw new Error("round trip changed the length");

const pad = (x) => x.toFixed(2).padStart(8);
console.log(`decode: ${pad(decodeElapsed)} ms total  ${(decodeElapsed / ITERATIONS).toFixed(3).padStart(7)} ms/iteration`);
console.log(`encode: ${pad(encodeElapsed)} ms total  ${(encodeElapsed / ITERATIONS).toFixed(3).padStart(7)} ms/iteration`);

// Peak RSS, the same VmHWM the Go side reports.
try {
  const status = require("node:fs").readFileSync("/proc/self/status", "utf8");
  const hwm = status.split("\n").find((l) => l.startsWith("VmHWM:"));
  if (hwm) console.log("peak RSS:", hwm.split(/\s+/)[1], "KiB");
} catch {
  /* not Linux */
}
