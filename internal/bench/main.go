// Command bench runs one realistic workload through jsorder and reports wall
// time and peak RSS, so it can be compared against the same workload in
// JavaScript (internal/bench/bench.mjs).
//
// The comparison is not there to prove Go wins — it will. It is there because a
// SMALL margin would be a bug report about this package: if a Go implementation
// is only marginally ahead of a JIT-warmed V8, something is allocating or
// copying where it should not be.
//
//	go run ./internal/bench            # Go side
//	bun internal/bench/bench.mjs       # JavaScript side
package main

import (
	"fmt"
	"os"
	"runtime"
	"strconv"
	"time"

	"github.com/trutohq/jsorder"
)

// The workload: 200 iterations over a 1000-record page — parse the JSON, then
// re-serialise it, which is exactly what a paginator does per page before
// hashing the result for loop detection.
const (
	records    = 1000
	iterations = 200
)

func main() {
	page := buildPage(records)
	doc, err := jsorder.MarshalJS(page)
	if err != nil {
		fail(err)
	}
	fmt.Printf("workload: %d records, %d iterations, %d bytes/document\n",
		records, iterations, len(doc))

	// Decode.
	start := time.Now()
	var decoded any
	for i := 0; i < iterations; i++ {
		if decoded, err = jsorder.DecodeFloat64(doc); err != nil {
			fail(err)
		}
	}
	decodeElapsed := time.Since(start)

	// Encode, into a reused buffer — the shape a caller feeding a hash uses.
	buf := make([]byte, 0, len(doc)+1024)
	start = time.Now()
	for i := 0; i < iterations; i++ {
		if buf, err = jsorder.AppendJS(buf[:0], decoded); err != nil {
			fail(err)
		}
	}
	encodeElapsed := time.Since(start)

	if len(buf) != len(doc) {
		fail(fmt.Errorf("round trip changed the length: %d -> %d", len(doc), len(buf)))
	}

	fmt.Printf("decode: %8.2f ms total  %7.3f ms/iteration\n",
		ms(decodeElapsed), ms(decodeElapsed)/iterations)
	fmt.Printf("encode: %8.2f ms total  %7.3f ms/iteration\n",
		ms(encodeElapsed), ms(encodeElapsed)/iterations)
	fmt.Printf("peak RSS: %d KiB\n", peakRSS())

	var stats runtime.MemStats
	runtime.ReadMemStats(&stats)
	fmt.Printf("total allocated: %d MiB over the whole run\n", stats.TotalAlloc>>20)
}

func buildPage(n int) []any {
	page := make([]any, n)
	for i := range page {
		page[i] = jsorder.Pairs(
			"id", float64(1000000+i),
			"external_id", "ext_"+strconv.Itoa(i),
			"name", "Record "+strconv.Itoa(i),
			"score", float64(i)*1.5,
			"active", i%2 == 0,
			"meta", jsorder.Pairs("2", float64(i), "1", float64(i*2), "source", "api"),
			"deleted_at", jsorder.Null,
		)
	}
	return page
}

func ms(d time.Duration) float64 { return float64(d.Nanoseconds()) / 1e6 }

// peakRSS reads VmHWM, which is the number a `time -v` run would report and the
// one that matters for a container limit.
func peakRSS() int64 {
	data, err := os.ReadFile("/proc/self/status")
	if err != nil {
		return -1
	}
	for line := range splitLines(string(data)) {
		if len(line) > 6 && line[:6] == "VmHWM:" {
			var kib int64
			fmt.Sscanf(line[6:], "%d", &kib)
			return kib
		}
	}
	return -1
}

func splitLines(s string) func(func(string) bool) {
	return func(yield func(string) bool) {
		start := 0
		for i := 0; i < len(s); i++ {
			if s[i] == '\n' {
				if !yield(s[start:i]) {
					return
				}
				start = i + 1
			}
		}
	}
}

func fail(err error) {
	fmt.Fprintln(os.Stderr, "bench:", err)
	os.Exit(1)
}
