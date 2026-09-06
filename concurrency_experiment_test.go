package jsorder

import (
	"runtime"
	"strconv"
	"sync"
	"testing"
)

// EXPERIMENT, deliberately test-only and NOT shipped API. Measures whether fanning the per-record encode
// of a page across goroutines pays for itself. Deleted once the numbers are in
// DECISIONS.md; kept in git history only.
//
// Order-preserving by construction: each worker owns a contiguous chunk and
// writes into its own buffer, and the buffers are concatenated in index order.
// Appending from several goroutines would be faster and would also break every
// signature, which is the trap the concurrency rules exist to stop.
func marshalPageParallel(page []any, workers int) ([]byte, error) {
	if workers <= 1 || len(page) < workers {
		return MarshalJS(page)
	}
	chunks := make([][]byte, workers)
	errs := make([]error, workers)
	per := (len(page) + workers - 1) / workers

	var wg sync.WaitGroup
	for w := 0; w < workers; w++ {
		lo := w * per
		hi := min(lo+per, len(page))
		if lo >= hi {
			continue
		}
		wg.Add(1)
		go func(w, lo, hi int) {
			defer wg.Done()
			buf := make([]byte, 0, 1024)
			for i := lo; i < hi; i++ {
				if i > lo {
					buf = append(buf, ',')
				}
				var err error
				if buf, err = AppendJS(buf, page[i]); err != nil {
					errs[w] = err
					return
				}
			}
			chunks[w] = buf
		}(w, lo, hi)
	}
	wg.Wait()

	total := 2
	for _, c := range chunks {
		total += len(c) + 1
	}
	out := make([]byte, 0, total)
	out = append(out, '[')
	for w, c := range chunks {
		if errs[w] != nil {
			return nil, errs[w]
		}
		if len(c) == 0 {
			continue
		}
		if len(out) > 1 {
			out = append(out, ',')
		}
		out = append(out, c...)
	}
	return append(out, ']'), nil
}

func TestParallelMarshalMatchesSequential(t *testing.T) {
	for _, n := range []int{1, 2, 5, 17, 100, 1000} {
		page := recordPage(n)
		want, err := MarshalJS(page)
		if err != nil {
			t.Fatal(err)
		}
		got, err := marshalPageParallel(page, runtime.NumCPU())
		if err != nil {
			t.Fatal(err)
		}
		if string(got) != string(want) {
			t.Fatalf("n=%d: parallel output differs from sequential", n)
		}
	}
}

func BenchmarkConcurrencyExperiment(b *testing.B) {
	for _, n := range []int{10, 30, 50, 100, 1000, 10000, 100000} {
		page := recordPage(n)
		b.Run("seq/"+strconv.Itoa(n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := MarshalJS(page); err != nil {
					b.Fatal(err)
				}
			}
		})
		b.Run("par/"+strconv.Itoa(n), func(b *testing.B) {
			b.ReportAllocs()
			for b.Loop() {
				if _, err := marshalPageParallel(page, runtime.NumCPU()); err != nil {
					b.Fatal(err)
				}
			}
		})
	}
}
