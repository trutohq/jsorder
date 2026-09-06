package jsorder

import (
	"bufio"
	"bytes"
	"encoding/json"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestLiveDifferentialAgainstJS generates documents HERE, in Go, and sends them
// to a real JavaScript runtime to be stringified.
//
// The committed goldens are a fixed corpus: they prove the cases someone
// thought of. This proves the cases nobody thought of, because the generator
// and the oracle are on opposite sides of the comparison. goenvoy's recorded
// lesson is that every real divergence it found came from executing both
// engines and none from reading the source.
//
// Skipped when neither bun nor node is on PATH, so the suite still runs in a
// container that has no JavaScript.
func TestLiveDifferentialAgainstJS(t *testing.T) {
	runtime := jsRuntime(t)

	const cases = 20000
	rng := rand.New(rand.NewPCG(20260906, 1))

	docs := make([]string, 0, cases)
	values := make([]any, 0, cases)
	for len(docs) < cases {
		v := randomValue(rng, 0)
		// Round-trip through DecodeFloat64 so the value holds exactly what
		// JSON.parse would hold — otherwise this would be testing json.Number
		// preservation, which JavaScript cannot represent and which the goldens
		// cover separately.
		encoded, err := MarshalJS(v)
		if err != nil {
			t.Fatal(err)
		}
		parsed, err := DecodeFloat64(encoded)
		if err != nil {
			t.Fatalf("cannot re-decode %s: %v", encoded, err)
		}
		reencoded, err := MarshalJS(parsed)
		if err != nil {
			t.Fatal(err)
		}
		docs = append(docs, string(reencoded))
		values = append(values, parsed)
	}

	dir := t.TempDir()
	inPath := filepath.Join(dir, "in.jsonl")
	if err := os.WriteFile(inPath, []byte(strings.Join(docs, "\n")+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// The oracle: parse each line and stringify it again. Whatever comes back
	// is by definition what JavaScript does.
	script := filepath.Join(dir, "oracle.mjs")
	const oracle = `import { readFileSync } from "node:fs";
const lines = readFileSync(process.argv[2], "utf8").split("\n").filter(Boolean);
const out = [];
for (const line of lines) out.push(JSON.stringify(JSON.stringify(JSON.parse(line))));
process.stdout.write(out.join("\n") + "\n");
`
	if err := os.WriteFile(script, []byte(oracle), 0o600); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(runtime, script, inPath)
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("%s: %v\n%s", runtime, err, stderr.String())
	}

	scan := bufio.NewScanner(&stdout)
	scan.Buffer(make([]byte, 0, 64*1024), 8*1024*1024)
	var checked, mismatches int
	for i := 0; scan.Scan(); i++ {
		if i >= len(values) {
			t.Fatalf("%s returned more lines than were sent", runtime)
		}
		var want string
		if err := json.Unmarshal(scan.Bytes(), &want); err != nil {
			t.Fatalf("oracle line %d: %v", i, err)
		}
		got, err := MarshalJS(values[i])
		if err != nil {
			t.Fatal(err)
		}
		checked++
		if string(got) != want {
			mismatches++
			if mismatches <= 10 {
				t.Errorf("case %d\n in:  %s\n got: %s\n JS:  %s", i, docs[i], got, want)
			}
		}
	}
	if err := scan.Err(); err != nil {
		t.Fatal(err)
	}
	if checked != len(values) {
		t.Fatalf("%s returned %d lines for %d documents", runtime, checked, len(values))
	}
	if mismatches > 10 {
		t.Errorf("%d total mismatches", mismatches)
	}
	t.Logf("%d generated documents checked against %s", checked, runtime)
}

func jsRuntime(t *testing.T) string {
	t.Helper()
	for _, name := range []string{"bun", "node"} {
		if path, err := exec.LookPath(name); err == nil {
			return path
		}
	}
	t.Skip("neither bun nor node on PATH; skipping the live differential")
	return ""
}
