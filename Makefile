# Go 1.27.0 lives at ~/.local/go and is NOT on the default PATH.
#   export PATH="$HOME/.local/go/bin:$PATH"
#
# fmt-check runs first on purpose: it fails on unformatted code rather than
# silently reformatting, because a formatting diff mixed into a behavioural one
# is how a real change gets waved through in review.

.PHONY: all fmt fmt-check vet test test-short bench bench-js fuzz goldens clean

all: fmt-check vet test

fmt:
	gofmt -w .

fmt-check:
	@out=$$(gofmt -l .); if [ -n "$$out" ]; then echo "unformatted:"; echo "$$out"; exit 1; fi

vet:
	CGO_ENABLED=0 go vet ./...

# -race needs cgo. CGO_ENABLED=0 is the rule for the shipped BINARY, not for a
# test run, and a race that only appears under load is a race you shipped.
test:
	CGO_ENABLED=1 go test -race -count=1 ./...

test-short:
	CGO_ENABLED=0 go test -count=1 ./...

bench:
	CGO_ENABLED=0 go test -run xxx -bench 'Decode|Marshal|Append|KeyOrder|IsArrayIndex' \
		-benchmem -count=6 . | tee bench.txt

# The concurrency experiment saturates the box, so it is measured on its own —
# run alongside the others it skews every number in the table.
bench-concurrency:
	CGO_ENABLED=0 go test -run xxx -bench ConcurrencyExperiment -benchmem -count=6 .

# Go against the JavaScript, same workload. A SMALL margin is a bug report.
bench-js:
	CGO_ENABLED=0 go run ./internal/bench
	@echo
	@bun internal/bench/bench.mjs 2>/dev/null || node internal/bench/bench.mjs

fuzz:
	CGO_ENABLED=1 go test -run xxx -fuzz FuzzDecode -fuzztime 60s .
	CGO_ENABLED=1 go test -run xxx -fuzz FuzzNumberFormat -fuzztime 60s .

# Goldens come from EXECUTING the reference. Never hand-edit testdata/.
goldens:
	bun internal/gen/generate.mjs 2>/dev/null || node internal/gen/generate.mjs

clean:
	rm -f bench.txt
