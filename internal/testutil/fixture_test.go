package testutil

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
)

// recorder captures failures so tests can check what a helper reports.
type recorder struct {
	testing.TB
	mu    sync.Mutex
	errs  []string
	fatal bool
}

func (r *recorder) Helper() {}

func (r *recorder) Errorf(format string, args ...any) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.errs = append(r.errs, fmt.Sprintf(format, args...))
}

func (r *recorder) Fatalf(format string, args ...any) {
	r.Errorf(format, args...)
	r.fatal = true
	runtime.Goexit()
}

func (r *recorder) text() string { return strings.Join(r.errs, "\n") }

// run calls f with a recorder on its own goroutine, so Fatalf can exit it.
func run(t *testing.T, f func(tb testing.TB)) *recorder {
	r := &recorder{TB: t}
	done := make(chan struct{})
	go func() {
		defer close(done)
		f(r)
	}()
	<-done
	return r
}

// writeFixture writes a one-case, one-stream fixture holding data.
func writeFixture(t *testing.T, data []int32, chunk, prefix int) string {
	t.Helper()
	dir := t.TempDir()
	raw := EncodeLE(nil, data)
	var chunks []string
	for start := 0; start < len(data); start += chunk {
		end := min(start+chunk, len(data))
		sum := sha256.Sum256(raw[4*start : 4*end])
		chunks = append(chunks, hex.EncodeToString(sum[:]))
	}
	s := Stream{Name: "out", Type: "int32", Count: int64(len(data)), Chunk: int64(chunk), Chunks: chunks}
	if prefix > 0 {
		p := raw[:4*prefix]
		sum := sha256.Sum256(p)
		s.File = &File{Path: "c.out.raw", Count: int64(prefix), SHA256: hex.EncodeToString(sum[:])}
		if err := os.WriteFile(filepath.Join(dir, "c.out.raw"), p, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	m := Manifest{Schema: 1, Component: "test", Reference: Reference{Commit: ReferenceCommit},
		Cases: []Case{{Name: "c", Params: json.RawMessage(`{"k": 1}`), Streams: []Stream{s}}}}
	b, err := json.Marshal(m)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "manifest.json"), b, 0o644); err != nil {
		t.Fatal(err)
	}
	return dir
}

func seq(n int) []int32 {
	v := make([]int32, n)
	for i := range v {
		v[i] = int32(i*7919 - 50000)
	}
	return v
}

func check(tb testing.TB, dir string, got []int32, partial bool) {
	m := LoadManifest(tb, dir)
	c := m.Case(tb, "c")
	k := NewStreamChecker[int32](tb, m, c, c.Stream(tb, "out"), partial)
	for _, v := range got {
		k.Put(v)
	}
	k.Done()
}

func TestStreamCheckerAcceptsIdenticalStream(t *testing.T) {
	data := seq(20)
	dir := writeFixture(t, data, 8, 5)
	if r := run(t, func(tb testing.TB) { check(tb, dir, data, false) }); len(r.errs) > 0 {
		t.Fatalf("unexpected failure: %s", r.text())
	}
}

func TestStreamCheckerReportsPrefixIndex(t *testing.T) {
	data := seq(20)
	dir := writeFixture(t, data, 8, 5)
	got := append([]int32(nil), data...)
	got[3]++
	r := run(t, func(tb testing.TB) { check(tb, dir, got, false) })
	if !strings.Contains(r.text(), "first difference at index 3") {
		t.Fatalf("want the prefix index reported, got: %q", r.text())
	}
}

func TestStreamCheckerReportsChunk(t *testing.T) {
	data := seq(20)
	dir := writeFixture(t, data, 8, 5)
	got := append([]int32(nil), data...)
	got[17]++
	r := run(t, func(tb testing.TB) { check(tb, dir, got, false) })
	if !strings.Contains(r.text(), "chunk 2 (elements [16, 20))") {
		t.Fatalf("want chunk 2 reported, got: %q", r.text())
	}
}

func TestStreamCheckerReportsShortAndLongStreams(t *testing.T) {
	data := seq(20)
	dir := writeFixture(t, data, 8, 5)
	if r := run(t, func(tb testing.TB) { check(tb, dir, data[:12], false) }); !strings.Contains(r.text(), "ended after 12 of 20") {
		t.Errorf("short stream: got %q", r.text())
	}
	if r := run(t, func(tb testing.TB) { check(tb, dir, append(data, 1), false) }); !strings.Contains(r.text(), "more than 20") {
		t.Errorf("long stream: got %q", r.text())
	}
	if r := run(t, func(tb testing.TB) { check(tb, dir, data[:12], true) }); len(r.errs) > 0 {
		t.Errorf("partial stream with one full chunk: unexpected %q", r.text())
	}
	if r := run(t, func(tb testing.TB) { check(tb, dir, data[:6], true) }); !strings.Contains(r.text(), "before the first chunk") {
		t.Errorf("partial stream without a full chunk: got %q", r.text())
	}
}

func TestStreamCheckerUsesFullReference(t *testing.T) {
	data := seq(20)
	dir := writeFixture(t, data, 8, 5)
	full := t.TempDir()
	if err := os.WriteFile(filepath.Join(full, "c.out.raw"), EncodeLE(nil, data), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv(FullReferenceEnv, full)
	got := append([]int32(nil), data...)
	got[13] = -1
	r := run(t, func(tb testing.TB) { check(tb, dir, got, false) })
	if !strings.Contains(r.text(), "first difference at element 13") {
		t.Fatalf("want element 13 reported, got: %q", r.text())
	}
}

func TestReadFileVerifiesHash(t *testing.T) {
	data := seq(20)
	dir := writeFixture(t, data, 8, 5)
	if err := os.WriteFile(filepath.Join(dir, "c.out.raw"), EncodeLE(nil, []int32{9, 9, 9, 9, 9}), 0o644); err != nil {
		t.Fatal(err)
	}
	r := run(t, func(tb testing.TB) {
		m := LoadManifest(tb, dir)
		c := m.Case(tb, "c")
		ReadFile[int32](tb, m, c.Stream(tb, "out"))
	})
	if !r.fatal || !strings.Contains(r.text(), "SHA-256") {
		t.Fatalf("want a fatal hash error, got: %q", r.text())
	}
}

func TestReadFileRejectsWrongType(t *testing.T) {
	dir := writeFixture(t, seq(4), 8, 4)
	r := run(t, func(tb testing.TB) {
		m := LoadManifest(tb, dir)
		c := m.Case(tb, "c")
		ReadFile[int16](tb, m, c.Stream(tb, "out"))
	})
	if !r.fatal || !strings.Contains(r.text(), "read as int16") {
		t.Fatalf("want a type error, got: %q", r.text())
	}
}

func TestLoadManifestValidates(t *testing.T) {
	for _, tc := range []struct {
		name, edit, want string
	}{
		{"commit", `"commit":"` + ReferenceCommit + `"`, "reference commit"},
		{"chunks", `"chunk":8`, "chunk hashes"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := writeFixture(t, seq(20), 8, 5)
			path := filepath.Join(dir, "manifest.json")
			b, err := os.ReadFile(path)
			if err != nil {
				t.Fatal(err)
			}
			repl := map[string]string{"commit": `"commit":"0000"`, "chunks": `"chunk":4`}[tc.name]
			s := strings.Replace(string(b), tc.edit, repl, 1)
			if s == string(b) {
				t.Fatalf("edit %q not applied", tc.edit)
			}
			if err := os.WriteFile(path, []byte(s), 0o644); err != nil {
				t.Fatal(err)
			}
			r := run(t, func(tb testing.TB) { LoadManifest(tb, dir) })
			if !r.fatal || !strings.Contains(r.text(), tc.want) {
				t.Fatalf("want %q, got %q", tc.want, r.text())
			}
		})
	}
}

func TestCaseParams(t *testing.T) {
	dir := writeFixture(t, seq(4), 8, 0)
	m := LoadManifest(t, dir)
	var p struct{ K int }
	m.Case(t, "c").DecodeParams(t, &p)
	if p.K != 1 {
		t.Fatalf("params: got %+v", p)
	}
}

func TestEncodeDecodeRoundTrip(t *testing.T) {
	f := []float32{0, -0.5, 32767, -32768.25}
	if got := DecodeLE[float32](EncodeLE(nil, f)); !Compare(t, "float32", f, got) {
		return
	}
	i := []int16{-32768, -1, 0, 1, 32767}
	b := EncodeLE(nil, i)
	if b[0] != 0x00 || b[1] != 0x80 {
		t.Fatalf("int16 -32768 encodes as % x, want 00 80", b[:2])
	}
	Compare(t, "int16", i, DecodeLE[int16](b))
}
