package testutil

import (
	"bufio"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"hash"
	"io"
	"math"
	"os"
	"path/filepath"
	"testing"
)

// ReferenceCommit is the pinned SpeexDSP commit every manifest must name.
const ReferenceCommit = "1b28a0f61bc31162979e1f26f3981fc3637095c8"

// FullReferenceEnv names an environment variable that, when set to a
// directory of complete harness streams (written by a driver run with
// HARNESS_FULL_OUT), makes stream checks report the exact first differing
// element instead of only the differing chunk.
const FullReferenceEnv = "GOVOIP_REFERENCE_FULL"

// Manifest is a fixture set written by a reference driver. The format is
// described in docs/contracts.md ("Fixtures").
type Manifest struct {
	Schema    int       `json:"schema"`
	Component string    `json:"component"`
	Driver    string    `json:"driver"`
	Reference Reference `json:"reference"`
	Build     Build     `json:"build"`
	Cases     []Case    `json:"cases"`

	dir string
}

// Reference identifies the upstream source.
type Reference struct {
	Repository string `json:"repository"`
	Tag        string `json:"tag"`
	Commit     string `json:"commit"`
}

// Build records the toolchain that produced a fixture set.
type Build struct {
	Mode         string `json:"mode"`
	Image        string `json:"image"`
	Compiler     string `json:"compiler"`
	CFlags       string `json:"cflags"`
	OS           string `json:"os"`
	Arch         string `json:"arch"`
	ConfigSHA256 string `json:"config_h_sha256"`
}

// Case is one configuration of a component with its streams.
type Case struct {
	Name    string          `json:"name"`
	Params  json.RawMessage `json:"params"`
	Streams []Stream        `json:"streams"`
}

// Stream is a sequence of little-endian elements, recorded as one SHA-256 per
// chunk of Chunk elements, with an optional committed prefix in File.
type Stream struct {
	Name   string   `json:"name"`
	Type   string   `json:"type"`
	Count  int64    `json:"count"`
	Chunk  int64    `json:"chunk"`
	Chunks []string `json:"chunks"`
	File   *File    `json:"file"`
}

// File is a committed raw file holding the first Count elements of a stream.
type File struct {
	Path   string `json:"path"`
	Count  int64  `json:"count"`
	SHA256 string `json:"sha256"`
}

var typeSizes = map[string]int64{"int16": 2, "int32": 4, "float32": 4, "float64": 8}

// LoadManifest reads dir/manifest.json and validates its structure.
func LoadManifest(t testing.TB, dir string) *Manifest {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(dir, "manifest.json"))
	if err != nil {
		t.Fatalf("fixture manifest: %v", err)
	}
	m := &Manifest{dir: dir}
	if err := json.Unmarshal(data, m); err != nil {
		t.Fatalf("fixture manifest %s: %v", dir, err)
	}
	if err := m.validate(); err != nil {
		t.Fatalf("fixture manifest %s: %v", dir, err)
	}
	return m
}

func (m *Manifest) validate() error {
	if m.Schema != 1 {
		return fmt.Errorf("schema %d, want 1", m.Schema)
	}
	if m.Reference.Commit != ReferenceCommit {
		return fmt.Errorf("reference commit %q, want %s", m.Reference.Commit, ReferenceCommit)
	}
	seen := make(map[string]bool)
	for _, c := range m.Cases {
		if seen[c.Name] {
			return fmt.Errorf("duplicate case %q", c.Name)
		}
		seen[c.Name] = true
		for _, s := range c.Streams {
			if typeSizes[s.Type] == 0 {
				return fmt.Errorf("case %s stream %s: unknown type %q", c.Name, s.Name, s.Type)
			}
			if s.Chunk <= 0 || s.Count < 0 {
				return fmt.Errorf("case %s stream %s: bad count or chunk", c.Name, s.Name)
			}
			if want := (s.Count + s.Chunk - 1) / s.Chunk; int64(len(s.Chunks)) != want {
				return fmt.Errorf("case %s stream %s: %d chunk hashes, want %d", c.Name, s.Name, len(s.Chunks), want)
			}
			if s.File != nil && (s.File.Count > s.Count || s.File.Count < 0) {
				return fmt.Errorf("case %s stream %s: file count %d exceeds stream count %d", c.Name, s.Name, s.File.Count, s.Count)
			}
		}
	}
	return nil
}

// Dir returns the directory the manifest was loaded from.
func (m *Manifest) Dir() string { return m.dir }

// Case returns the named case, failing the test if it does not exist.
func (m *Manifest) Case(t testing.TB, name string) *Case {
	t.Helper()
	for i := range m.Cases {
		if m.Cases[i].Name == name {
			return &m.Cases[i]
		}
	}
	t.Fatalf("fixture %s: no case %q", m.Component, name)
	return nil
}

// Stream returns the named stream of c, failing the test if it does not exist.
func (c *Case) Stream(t testing.TB, name string) *Stream {
	t.Helper()
	for i := range c.Streams {
		if c.Streams[i].Name == name {
			return &c.Streams[i]
		}
	}
	t.Fatalf("fixture case %s: no stream %q", c.Name, name)
	return nil
}

// DecodeParams unmarshals the case parameters into v.
func (c *Case) DecodeParams(t testing.TB, v any) {
	t.Helper()
	if err := json.Unmarshal(c.Params, v); err != nil {
		t.Fatalf("fixture case %s params: %v", c.Name, err)
	}
}

// ReadFile returns the committed file of s after checking its SHA-256. The
// element type must match s.Type.
func ReadFile[T Elem](t testing.TB, m *Manifest, s *Stream) []T {
	t.Helper()
	if s.File == nil {
		t.Fatalf("fixture stream %s: no committed file", s.Name)
	}
	checkType[T](t, s)
	data, err := os.ReadFile(filepath.Join(m.dir, s.File.Path))
	if err != nil {
		t.Fatalf("fixture: %v", err)
	}
	sum := sha256.Sum256(data)
	if got := hex.EncodeToString(sum[:]); got != s.File.SHA256 {
		t.Fatalf("fixture %s: SHA-256 %s, manifest says %s", s.File.Path, got, s.File.SHA256)
	}
	if int64(len(data)) != s.File.Count*typeSizes[s.Type] {
		t.Fatalf("fixture %s: %d bytes, want %d elements of %s", s.File.Path, len(data), s.File.Count, s.Type)
	}
	return DecodeLE[T](data)
}

// Elem is an element type a fixture stream can hold.
type Elem interface {
	int16 | int32 | float32 | float64
}

type codec[T Elem] struct {
	name string
	size int
	enc  func([]byte, T) []byte
	dec  func([]byte) T
}

func codecOf[T Elem]() codec[T] {
	var zero T
	switch any(zero).(type) {
	case int16:
		return any(codec[int16]{"int16", 2,
			func(b []byte, v int16) []byte { return binary.LittleEndian.AppendUint16(b, uint16(v)) },
			func(b []byte) int16 { return int16(binary.LittleEndian.Uint16(b)) },
		}).(codec[T])
	case int32:
		return any(codec[int32]{"int32", 4,
			func(b []byte, v int32) []byte { return binary.LittleEndian.AppendUint32(b, uint32(v)) },
			func(b []byte) int32 { return int32(binary.LittleEndian.Uint32(b)) },
		}).(codec[T])
	case float32:
		return any(codec[float32]{"float32", 4,
			func(b []byte, v float32) []byte { return binary.LittleEndian.AppendUint32(b, math.Float32bits(v)) },
			func(b []byte) float32 { return math.Float32frombits(binary.LittleEndian.Uint32(b)) },
		}).(codec[T])
	default:
		return any(codec[float64]{"float64", 8,
			func(b []byte, v float64) []byte { return binary.LittleEndian.AppendUint64(b, math.Float64bits(v)) },
			func(b []byte) float64 { return math.Float64frombits(binary.LittleEndian.Uint64(b)) },
		}).(codec[T])
	}
}

func checkType[T Elem](t testing.TB, s *Stream) {
	t.Helper()
	if name := codecOf[T]().name; name != s.Type {
		t.Fatalf("fixture stream %s holds %s, read as %s", s.Name, s.Type, name)
	}
}

// DecodeLE decodes little-endian elements.
func DecodeLE[T Elem](data []byte) []T {
	c := codecOf[T]()
	out := make([]T, len(data)/c.size)
	for i := range out {
		out[i] = c.dec(data[i*c.size:])
	}
	return out
}

// EncodeLE appends the little-endian encoding of v to b.
func EncodeLE[T Elem](b []byte, v []T) []byte {
	c := codecOf[T]()
	for _, x := range v {
		b = c.enc(b, x)
	}
	return b
}

func same[T Elem](a, b T) bool {
	switch x := any(a).(type) {
	case float32:
		return math.Float32bits(x) == math.Float32bits(any(b).(float32))
	case float64:
		return math.Float64bits(x) == math.Float64bits(any(b).(float64))
	}
	return a == b
}

// Compare reports the first index where got differs from want, with the
// surrounding elements, and returns whether they are identical. Floats are
// compared by bit pattern.
func Compare[T Elem](t testing.TB, name string, want, got []T) bool {
	t.Helper()
	n := min(len(want), len(got))
	for i := 0; i < n; i++ {
		if !same(want[i], got[i]) {
			t.Errorf("%s: first difference at index %d: got %v, want %v\n%s", name, i, got[i], want[i], context(want, got, i))
			return false
		}
	}
	if len(want) != len(got) {
		t.Errorf("%s: length %d, want %d (identical up to %d)", name, len(got), len(want), n)
		return false
	}
	return true
}

func context[T Elem](want, got []T, i int) string {
	s := "  index        want         got\n"
	for j := max(0, i-4); j < min(len(want), len(got), i+5); j++ {
		mark := " "
		if !same(want[j], got[j]) {
			mark = "*"
		}
		s += fmt.Sprintf("%s %6d %11v %11v\n", mark, j, want[j], got[j])
	}
	return s
}

// StreamChecker compares elements produced by a test, one at a time, with a
// fixture stream: element by element against the committed prefix, and by
// SHA-256 per chunk for the whole stream. It holds no more than the prefix.
type StreamChecker[T Elem] struct {
	t       testing.TB
	m       *Manifest
	s       *Stream
	label   string
	prefix  []T
	got     []T
	chunk   hash.Hash
	inChunk int64
	n       int64
	buf     []byte
	partial bool
	failed  bool
	full    *bufio.Reader
	fullF   *os.File
	codec   codec[T]
	one     []byte
}

// NewStreamChecker starts a check of stream s of case c. With partial set,
// the test may stop early; Done then verifies the complete chunks it saw,
// and needs at least one unless the stream is empty.
func NewStreamChecker[T Elem](t testing.TB, m *Manifest, c *Case, s *Stream, partial bool) *StreamChecker[T] {
	t.Helper()
	checkType[T](t, s)
	k := &StreamChecker[T]{
		t:       t,
		m:       m,
		s:       s,
		label:   fmt.Sprintf("%s/%s/%s", m.Component, c.Name, s.Name),
		chunk:   sha256.New(),
		buf:     make([]byte, 0, 4096),
		partial: partial,
		codec:   codecOf[T](),
	}
	k.one = make([]byte, k.codec.size)
	if s.File != nil {
		k.prefix = ReadFile[T](t, m, s)
		k.got = make([]T, 0, len(k.prefix))
	}
	if dir := os.Getenv(FullReferenceEnv); dir != "" {
		f, err := os.Open(filepath.Join(dir, c.Name+"."+s.Name+".raw"))
		if err == nil {
			k.fullF = f
			k.full = bufio.NewReader(f)
		}
	}
	return k
}

// Put adds the next element.
func (k *StreamChecker[T]) Put(v T) {
	if k.failed {
		return
	}
	if k.n >= k.s.Count {
		k.fail("more than %d elements", k.s.Count)
		return
	}
	if k.n < int64(len(k.prefix)) {
		k.got = append(k.got, v)
		if int64(len(k.got)) == int64(len(k.prefix)) && !Compare(k.t, k.label+" (committed prefix)", k.prefix, k.got) {
			k.failed = true
			return
		}
	}
	if k.full != nil {
		k.compareFull(v)
	}
	k.buf = k.codec.enc(k.buf, v)
	if len(k.buf) == cap(k.buf) {
		k.chunk.Write(k.buf)
		k.buf = k.buf[:0]
	}
	k.n++
	k.inChunk++
	if k.inChunk == k.s.Chunk {
		k.endChunk()
	}
}

func (k *StreamChecker[T]) compareFull(v T) {
	if _, err := io.ReadFull(k.full, k.one); err != nil {
		k.fail("full reference ended at element %d: %v", k.n, err)
		return
	}
	if w := k.codec.dec(k.one); !same(w, v) {
		k.fail("first difference at element %d: got %v, want %v (full reference)", k.n, v, w)
	}
}

func (k *StreamChecker[T]) endChunk() {
	if k.failed {
		return
	}
	k.chunk.Write(k.buf)
	k.buf = k.buf[:0]
	idx := (k.n - 1) / k.s.Chunk
	got := hex.EncodeToString(k.chunk.Sum(nil))
	if got != k.s.Chunks[idx] {
		start := idx * k.s.Chunk
		k.fail("chunk %d (elements [%d, %d)) differs from the reference; set %s to a directory of full harness output to find the element",
			idx, start, start+k.inChunk, FullReferenceEnv)
	}
	k.chunk.Reset()
	k.inChunk = 0
}

func (k *StreamChecker[T]) fail(format string, args ...any) {
	k.t.Helper()
	k.t.Errorf("%s: %s", k.label, fmt.Sprintf(format, args...))
	k.failed = true
}

// Count returns the number of elements added so far.
func (k *StreamChecker[T]) Count() int64 { return k.n }

// Done finishes the check. It reports a stream that is shorter than the
// fixture unless the checker is partial.
func (k *StreamChecker[T]) Done() {
	k.t.Helper()
	if k.fullF != nil {
		k.fullF.Close()
	}
	if k.failed {
		return
	}
	if int64(len(k.got)) < int64(len(k.prefix)) {
		Compare(k.t, k.label+" (committed prefix)", k.prefix[:len(k.got)], k.got)
		if !k.partial {
			k.fail("ended after %d elements, inside the committed prefix of %d", k.n, len(k.prefix))
		}
		return
	}
	if k.n == k.s.Count {
		if k.inChunk > 0 {
			k.endChunk()
		}
		return
	}
	if !k.partial {
		k.fail("ended after %d of %d elements", k.n, k.s.Count)
		return
	}
	if k.n < k.s.Chunk {
		k.fail("partial check ended after %d elements, before the first chunk of %d", k.n, k.s.Chunk)
	}
}
