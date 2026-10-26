package fixed

import (
	"flag"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/danieldonoghue/govoip-dsp/internal/testutil"
)

// This file mirrors reference/drivers/fixed.c: the case table, the edge
// lists and the input generators must stay identical, which the input
// stream hashes check.

const fixtureDir = "../../testdata/speexdsp/fixed"

var harnessInputs = flag.String("harness-inputs", "", "directory to write harness input files into (used by reference/fixtures.sh)")

const (
	tI16 = iota
	tI32
	tU32
	tPFE
)

type kind struct {
	t      int
	lo, hi int64
}

var (
	i16 = kind{tI16, -32768, 32767}
	i32 = kind{tI32, math.MinInt32, math.MaxInt32}
	u32 = kind{tU32, 0, math.MaxUint32}
	pfe = kind{tPFE, -32768, 32767}
)

func r16(lo, hi int64) kind  { return kind{tI16, lo, hi} }
func r32(lo, hi int64) kind  { return kind{tI32, lo, hi} }
func rpfe(lo, hi int64) kind { return kind{tPFE, lo, hi} }

const (
	mExhaustive = iota
	mEdgesRandom
	mEdgesSweepRandom
	mEdges
)

var modeNames = []string{"exhaustive", "edges+random", "edges+sweep16+random", "edges"}

type refCase struct {
	name  string
	mode  int
	args  []kind
	nout  int
	op    func(a []int64, o []int32)
	valid func(a []int64) bool
}

func w16(a []int64, i int) int16 { return int16(a[i]) }
func w32(a []int64, i int) int32 { return int32(a[i]) }
func pf(a []int64, i int) Float  { return Float{M: int16(a[i]), E: int16(a[i+1])} }
func putPF(r Float, o []int32)   { o[0], o[1] = int32(r.M), int32(r.E) }

func b2i(b bool) int32 {
	if b {
		return 1
	}
	return 0
}

func validDiv(a []int64) bool {
	return a[1] != 0 && !(a[0] == math.MinInt32 && a[1] == -1)
}

func validPDiv(a []int64) bool {
	num := int32(a[0]) + int32(int16(a[1])>>1)
	return a[1] != 0 && !(num == math.MinInt32 && a[1] == -1)
}

func one(f func(a []int64) int32) func([]int64, []int32) {
	return func(a []int64, o []int32) { o[0] = f(a) }
}

var refCases = []refCase{
	{"abs16", mExhaustive, []kind{i16}, 1, one(func(a []int64) int32 { return Abs16(w16(a, 0)) }), nil},
	{"neg16", mExhaustive, []kind{i16}, 1, one(func(a []int64) int32 { return Neg16(w16(a, 0)) }), nil},
	{"extend32", mExhaustive, []kind{i16}, 1, one(func(a []int64) int32 { return Extend32(w16(a, 0)) }), nil},
	{"cos", mExhaustive, []kind{i16}, 1, one(func(a []int64) int32 { return int32(Cos(w16(a, 0))) }), nil},
	{"exp", mExhaustive, []kind{i16}, 1, one(func(a []int64) int32 { return Exp(w16(a, 0)) }), nil},
	{"exp2", mExhaustive, []kind{i16}, 1, one(func(a []int64) int32 { return exp2(w16(a, 0)) }), nil},
	{"atan01", mExhaustive, []kind{i16}, 1, one(func(a []int64) int32 { return int32(atan01(w16(a, 0))) }), nil},
	{"cos_pi_2", mExhaustive, []kind{i16}, 1, one(func(a []int64) int32 { return int32(cosPi2(w16(a, 0))) }), nil},
	{"abs32", mEdgesSweepRandom, []kind{i32}, 1, one(func(a []int64) int32 { return Abs32(w32(a, 0)) }), nil},
	{"neg32", mEdgesSweepRandom, []kind{i32}, 1, one(func(a []int64) int32 { return Neg32(w32(a, 0)) }), nil},
	{"extract16", mEdgesSweepRandom, []kind{i32}, 1, one(func(a []int64) int32 { return int32(Extract16(w32(a, 0))) }), nil},
	{"word2int", mEdgesSweepRandom, []kind{i32}, 1, one(func(a []int64) int32 { return int32(Word2Int(w32(a, 0))) }), nil},
	{"sqrt", mEdgesSweepRandom, []kind{i32}, 1, one(func(a []int64) int32 { return int32(Sqrt(w32(a, 0))) }), nil},
	{"atan", mEdgesSweepRandom, []kind{i32}, 1, one(func(a []int64) int32 { return int32(Atan(w32(a, 0))) }), nil},
	{"cos_norm", mEdgesSweepRandom, []kind{i32}, 1, one(func(a []int64) int32 { return int32(CosNorm(w32(a, 0))) }), nil},
	{"pseudofloat", mEdgesSweepRandom, []kind{i32}, 2, func(a []int64, o []int32) { putPF(PseudoFloat(w32(a, 0)), o) }, nil},
	{"ilog2", mEdgesSweepRandom, []kind{u32}, 1, one(func(a []int64) int32 { return int32(ILog2(uint32(a[0]))) }), nil},
	{"ilog4", mEdgesSweepRandom, []kind{u32}, 1, one(func(a []int64) int32 { return int32(ilog4(uint32(a[0]))) }), nil},
	{"add16", mEdgesRandom, []kind{i16, i16}, 1, one(func(a []int64) int32 { return int32(Add16(w16(a, 0), w16(a, 1))) }), nil},
	{"sub16", mEdgesRandom, []kind{i16, i16}, 1, one(func(a []int64) int32 { return Sub16(w16(a, 0), w16(a, 1)) }), nil},
	{"add32", mEdgesRandom, []kind{i32, i32}, 1, one(func(a []int64) int32 { return Add32(w32(a, 0), w32(a, 1)) }), nil},
	{"sub32", mEdgesRandom, []kind{i32, i32}, 1, one(func(a []int64) int32 { return Sub32(w32(a, 0), w32(a, 1)) }), nil},
	{"min16", mEdgesRandom, []kind{i16, i16}, 1, one(func(a []int64) int32 { return int32(Min16(w16(a, 0), w16(a, 1))) }), nil},
	{"max16", mEdgesRandom, []kind{i16, i16}, 1, one(func(a []int64) int32 { return int32(Max16(w16(a, 0), w16(a, 1))) }), nil},
	{"min32", mEdgesRandom, []kind{i32, i32}, 1, one(func(a []int64) int32 { return Min32(w32(a, 0), w32(a, 1)) }), nil},
	{"max32", mEdgesRandom, []kind{i32, i32}, 1, one(func(a []int64) int32 { return Max32(w32(a, 0), w32(a, 1)) }), nil},
	{"mult16_16", mEdgesRandom, []kind{i16, i16}, 1, one(func(a []int64) int32 { return Mult16x16(w16(a, 0), w16(a, 1)) }), nil},
	{"mult16_16_q14", mEdgesRandom, []kind{i16, i16}, 1, one(func(a []int64) int32 { return Mult16x16Q14(w16(a, 0), w16(a, 1)) }), nil},
	{"mult16_16_q15", mEdgesRandom, []kind{i16, i16}, 1, one(func(a []int64) int32 { return Mult16x16Q15(w16(a, 0), w16(a, 1)) }), nil},
	{"mult16_16_p13", mEdgesRandom, []kind{i16, i16}, 1, one(func(a []int64) int32 { return mult16x16P13(w16(a, 0), w16(a, 1)) }), nil},
	{"mult16_16_p14", mEdgesRandom, []kind{i16, i16}, 1, one(func(a []int64) int32 { return mult16x16P14(w16(a, 0), w16(a, 1)) }), nil},
	{"mult16_16_p15", mEdgesRandom, []kind{i16, i16}, 1, one(func(a []int64) int32 { return Mult16x16P15(w16(a, 0), w16(a, 1)) }), nil},
	{"mult16_32_q15", mEdgesRandom, []kind{i16, i32}, 1, one(func(a []int64) int32 { return Mult16x32Q15(w16(a, 0), w32(a, 1)) }), nil},
	{"mult16_32_p15", mEdgesRandom, []kind{i16, i32}, 1, one(func(a []int64) int32 { return Mult16x32P15(w16(a, 0), w32(a, 1)) }), nil},
	{"mac16_16", mEdgesRandom, []kind{i32, i16, i16}, 1, one(func(a []int64) int32 { return Mac16x16(w32(a, 0), w16(a, 1), w16(a, 2)) }), nil},
	{"mac16_32_q15", mEdgesRandom, []kind{i32, i16, i32}, 1, one(func(a []int64) int32 { return Mac16x32Q15(w32(a, 0), w16(a, 1), w32(a, 2)) }), nil},
	{"shr16", mExhaustive, []kind{i16, r16(0, 15)}, 1, one(func(a []int64) int32 { return int32(shr16(w16(a, 0), uint(a[1]))) }), nil},
	{"shl16", mExhaustive, []kind{i16, r16(0, 15)}, 1, one(func(a []int64) int32 { return Shl16(w16(a, 0), uint(a[1])) }), nil},
	{"pshr16", mExhaustive, []kind{i16, r16(0, 15)}, 1, one(func(a []int64) int32 { return int32(PShr16(w16(a, 0), uint(a[1]))) }), nil},
	{"shr32", mEdgesRandom, []kind{i32, r16(0, 31)}, 1, one(func(a []int64) int32 { return Shr32(w32(a, 0), uint(a[1])) }), nil},
	{"shl32", mEdgesRandom, []kind{i32, r16(0, 31)}, 1, one(func(a []int64) int32 { return Shl32(w32(a, 0), uint(a[1])) }), nil},
	{"pshr32", mEdgesRandom, []kind{i32, r16(0, 30)}, 1, one(func(a []int64) int32 { return PShr32(w32(a, 0), uint(a[1])) }), nil},
	{"vshr32", mEdgesRandom, []kind{i32, r16(-31, 31)}, 1, one(func(a []int64) int32 { return VShr32(w32(a, 0), int(a[1])) }), nil},
	{"saturate32", mEdgesRandom, []kind{i32, r32(0, math.MaxInt32)}, 1, one(func(a []int64) int32 { return Saturate32(w32(a, 0), w32(a, 1)) }), nil},
	{"saturate32pshr", mEdgesRandom, []kind{i32, r16(0, 16), r16(0, 32767)}, 1, one(func(a []int64) int32 { return Saturate32PShr(w32(a, 0), uint(a[1]), w32(a, 2)) }), nil},
	{"div32_16", mEdgesRandom, []kind{i32, i16}, 1, one(func(a []int64) int32 { return int32(Div32x16(w32(a, 0), w16(a, 1))) }), validDiv},
	{"pdiv32_16", mEdgesRandom, []kind{i32, i16}, 1, one(func(a []int64) int32 { return int32(PDiv32x16(w32(a, 0), w16(a, 1))) }), validPDiv},
	{"div32", mEdgesRandom, []kind{i32, i32}, 1, one(func(a []int64) int32 { return Div32(w32(a, 0), w32(a, 1)) }), validDiv},
	{"pdiv32", mEdgesRandom, []kind{i32, i32}, 1, one(func(a []int64) int32 { return PDiv32(w32(a, 0), w32(a, 1)) }), validPDiv},
	{"float_add", mEdgesRandom, []kind{i16, pfe, i16, pfe}, 2, func(a []int64, o []int32) { putPF(FloatAdd(pf(a, 0), pf(a, 2)), o) }, nil},
	{"float_sub", mEdgesRandom, []kind{i16, pfe, i16, pfe}, 2, func(a []int64, o []int32) { putPF(FloatSub(pf(a, 0), pf(a, 2)), o) }, nil},
	{"float_lt", mEdgesRandom, []kind{i16, pfe, i16, pfe}, 1, one(func(a []int64) int32 { return b2i(FloatLT(pf(a, 0), pf(a, 2))) }), nil},
	{"float_gt", mEdgesRandom, []kind{i16, pfe, i16, pfe}, 1, one(func(a []int64) int32 { return b2i(FloatGT(pf(a, 0), pf(a, 2))) }), nil},
	{"float_mult", mEdgesRandom, []kind{i16, pfe, i16, pfe}, 2, func(a []int64, o []int32) { putPF(FloatMult(pf(a, 0), pf(a, 2)), o) }, nil},
	{"float_amult", mEdgesRandom, []kind{i16, pfe, i16, pfe}, 2, func(a []int64, o []int32) { putPF(FloatAMult(pf(a, 0), pf(a, 2)), o) }, nil},
	{"float_shl", mEdgesRandom, []kind{i16, pfe, i16}, 2, func(a []int64, o []int32) { putPF(FloatShl(pf(a, 0), int(a[2])), o) }, nil},
	{"float_extract16", mExhaustive, []kind{i16, rpfe(-31, 31)}, 1, one(func(a []int64) int32 { return int32(FloatExtract16(pf(a, 0))) }), nil},
	{"float_extract32", mExhaustive, []kind{i16, rpfe(-31, 31)}, 1, one(func(a []int64) int32 { return FloatExtract32(pf(a, 0)) }), nil},
	{"float_mul32", mEdgesRandom, []kind{i16, rpfe(-46, 16), i32}, 1, one(func(a []int64) int32 { return FloatMul32(pf(a, 0), w32(a, 2)) }), nil},
	{"float_mul32u", mEdgesRandom, []kind{i32, i32}, 2, func(a []int64, o []int32) { putPF(FloatMul32U(w32(a, 0), w32(a, 1)), o) }, nil},
	{"float_div32_float", mEdgesRandom, []kind{i32, r16(1, 32767), pfe}, 2, func(a []int64, o []int32) { putPF(FloatDiv32Float(w32(a, 0), pf(a, 1)), o) }, nil},
	{"float_div32", mEdgesRandom, []kind{i32, r32(1, math.MaxInt32)}, 2, func(a []int64, o []int32) { putPF(FloatDiv32(w32(a, 0), w32(a, 1)), o) }, nil},
	{"float_divu", mEdgesRandom, []kind{i16, pfe, r16(1, 32767), pfe}, 2, func(a []int64, o []int32) { putPF(FloatDivU(pf(a, 0), pf(a, 2)), o) }, nil},
	{"float_divu_nonpositive", mEdges, []kind{i16, pfe, r16(-32768, 0), pfe}, 2, func(a []int64, o []int32) { putPF(FloatDivU(pf(a, 0), pf(a, 2)), o) }, nil},
	{"float_sqrt", mEdgesRandom, []kind{i16, pfe}, 2, func(a []int64, o []int32) { putPF(FloatSqrt(pf(a, 0)), o) }, nil},
}

const (
	randomCount   = 10000000
	exhaustiveMax = 1 << 24
)

var (
	edges16 = []int64{
		-32768, -32767, -16385, -16384, -16383, -256, -2, -1, 0,
		1, 2, 255, 256, 16383, 16384, 16385, 32766, 32767,
	}
	edges32Extra = []int64{
		math.MinInt32, math.MinInt32 + 1, -1073741825, -1073741824, -16777216, -65537,
		-65536, -65535, -32769, 32768, 65535, 65536, 65537, 16777216, 1073741824,
		math.MaxInt32 - 1, math.MaxInt32,
	}
	edgesPFE = []int64{-32768, -46, -31, -30, -16, -15, -14, -1, 0, 1, 14, 15, 16, 31, 32767}
)

func edgesOf(k kind) []int64 {
	var l []int64
	add := func(x int64) {
		if x < k.lo || x > k.hi {
			return
		}
		for _, v := range l {
			if v == x {
				return
			}
		}
		l = append(l, x)
	}
	if k.hi-k.lo+1 <= 64 {
		for x := k.lo; x <= k.hi; x++ {
			add(x)
		}
		return l
	}
	switch k.t {
	case tI16:
		for _, x := range edges16 {
			add(x)
		}
	case tI32:
		for _, x := range edges16 {
			add(x)
		}
		for _, x := range edges32Extra {
			add(x)
		}
	case tU32:
		add(0)
		for b := 1; b <= 31; b++ {
			add(int64(1)<<b - 1)
			add(int64(1) << b)
			add(int64(1)<<b + 1)
		}
		add(math.MaxUint32)
	default:
		for _, x := range edgesPFE {
			add(x)
		}
	}
	add(k.lo)
	add(k.hi)
	return l
}

func mix16(u uint64) int64 {
	if u&1 != 0 {
		return int64(int16(uint16(u >> 48)))
	}
	w := (u >> 1) & 15
	mag := int64((u >> 32) & (1<<w - 1))
	if (u>>8)&1 != 0 {
		return -mag
	}
	return mag
}

func mix32(u uint64) int64 {
	if u&1 != 0 {
		return int64(int32(uint32(u >> 32)))
	}
	w := (u >> 1) & 31
	mag := int64((u >> 32) & (1<<w - 1))
	if (u>>8)&1 != 0 {
		return -mag
	}
	return mag
}

func mixU32(u uint64) int64 {
	if u&1 != 0 {
		return int64(uint32(u >> 32))
	}
	w := (u>>1)&31 + 1
	return int64((u >> 32) & (1<<w - 1))
}

func draw(r *testutil.SplitMix64, k kind) int64 {
	span := uint64(k.hi-k.lo) + 1
	if span <= 4096 {
		return k.lo + int64(r.Next()%span)
	}
	for {
		u := r.Next()
		var v int64
		switch k.t {
		case tI16:
			v = mix16(u)
		case tI32:
			v = mix32(u)
		case tU32:
			v = mixU32(u)
		default:
			if u&7 == 0 {
				v = int64(int16(uint16(u >> 48)))
			} else {
				v = -40 + int64((u>>3)%81)
			}
		}
		if v >= k.lo && v <= k.hi {
			return v
		}
	}
}

func kindString(k kind) string {
	return fmt.Sprintf("%s[%d,%d]", []string{"int16", "int32", "uint32", "exponent"}[k.t], k.lo, k.hi)
}

// caseParams is the parameter object the driver writes for each case.
type caseParams struct {
	Macro    string   `json:"macro"`
	Upstream string   `json:"upstream"`
	Mode     string   `json:"mode"`
	Args     []string `json:"args"`
	Outputs  int      `json:"outputs"`
	Tuples   int64    `json:"tuples"`
	Edges    int64    `json:"edges"`
	Sweep    int64    `json:"sweep"`
	Random   int64    `json:"random"`
	Seed     string   `json:"seed"`
	Warnings int64    `json:"warnings"`
}

// generate calls emit for every input tuple of c in the driver's order, and
// stops early when emit returns false. It returns the edge count.
func generate(c refCase, emit func(a []int64) bool) (edges int64) {
	n := len(c.args)
	a := make([]int64, n)
	visit := func() bool {
		if c.valid != nil && !c.valid(a) {
			return true
		}
		return emit(a)
	}
	if c.mode == mExhaustive {
		for i, k := range c.args {
			a[i] = k.lo
		}
		for {
			if !visit() {
				return 0
			}
			i := n - 1
			for ; i >= 0; i-- {
				if a[i]++; a[i] <= c.args[i].hi {
					break
				}
				a[i] = c.args[i].lo
			}
			if i < 0 {
				return 0
			}
		}
	}
	lists := make([][]int64, n)
	for i, k := range c.args {
		lists[i] = edgesOf(k)
	}
	idx := make([]int, n)
	for {
		for i := range a {
			a[i] = lists[i][idx[i]]
		}
		if c.valid == nil || c.valid(a) {
			edges++
			if !emit(a) {
				return edges
			}
		}
		i := n - 1
		for ; i >= 0; i-- {
			if idx[i]++; idx[i] < len(lists[i]) {
				break
			}
			idx[i] = 0
		}
		if i < 0 {
			break
		}
	}
	if c.mode == mEdgesSweepRandom {
		lo := int64(-32768)
		if c.args[0].t == tU32 {
			lo = 0
		}
		for x := lo; x < lo+65536; x++ {
			a[0] = x
			if !emit(a) {
				return edges
			}
		}
	}
	if c.mode == mEdges {
		return edges
	}
	r := testutil.SplitMix64{State: testutil.Seed(c.name)}
	for done := 0; done < randomCount; {
		for i, k := range c.args {
			a[i] = draw(&r, k)
		}
		if c.valid != nil && !c.valid(a) {
			continue
		}
		done++
		if !emit(a) {
			return edges
		}
	}
	return edges
}

// partialCheck is true when only the first chunk of each stream is
// compared: in -short mode, and under the race detector (raceEnabled).
func partialCheck() bool { return testing.Short() || raceEnabled }

func TestReference(t *testing.T) {
	m := testutil.LoadManifest(t, fixtureDir)
	if m.Component != "fixed" {
		t.Fatalf("manifest component %q", m.Component)
	}
	known := map[string]bool{"qconst16": true, "qconst32": true, "word2int_f32": true, "word2int_f64": true}
	for _, c := range refCases {
		known[c.name] = true
	}
	for _, c := range m.Cases {
		if !known[c.Name] {
			t.Errorf("manifest case %q has no Go counterpart", c.Name)
		}
	}
	for _, rc := range refCases {
		t.Run(rc.name, func(t *testing.T) {
			t.Parallel()
			checkCase(t, m, rc)
		})
	}
}

func checkCase(t *testing.T, m *testutil.Manifest, rc refCase) {
	c := m.Case(t, rc.name)
	var p caseParams
	c.DecodeParams(t, &p)
	args := make([]string, len(rc.args))
	for i, k := range rc.args {
		args[i] = kindString(k)
	}
	if p.Mode != modeNames[rc.mode] || strings.Join(p.Args, " ") != strings.Join(args, " ") || p.Outputs != rc.nout {
		t.Fatalf("case table differs from the driver: driver %s %v outputs %d, Go %s %v outputs %d",
			p.Mode, p.Args, p.Outputs, modeNames[rc.mode], args, rc.nout)
	}
	if want := fmt.Sprintf("0x%016x", testutil.Seed(rc.name)); p.Seed != want {
		t.Fatalf("seed %s, want %s", p.Seed, want)
	}
	partial := partialCheck()
	inStream, outStream := c.Stream(t, "in"), c.Stream(t, "out")
	in := testutil.NewStreamChecker[int32](t, m, c, inStream, partial)
	out := testutil.NewStreamChecker[int32](t, m, c, outStream, partial)
	inLimit, outLimit := inStream.Count, outStream.Count
	if partial {
		inLimit, outLimit = min(inLimit, inStream.Chunk), min(outLimit, outStream.Chunk)
	}
	o := make([]int32, 2)
	var tuples int64
	edges := generate(rc, func(a []int64) bool {
		for _, v := range a {
			in.Put(int32(uint32(v)))
		}
		rc.op(a, o)
		for _, v := range o[:rc.nout] {
			out.Put(v)
		}
		tuples++
		return in.Count() < inLimit || out.Count() < outLimit
	})
	if !partial && (tuples != p.Tuples || edges != p.Edges) {
		t.Errorf("generated %d tuples with %d edges, driver %d with %d", tuples, edges, p.Tuples, p.Edges)
	}
	in.Done()
	out.Done()
}

// float32Inputs and float64Inputs are the WORD2INT inputs: boundaries with
// their neighbours, special values, values spread over [-40000, 40000], and
// random bit patterns. NaN is excluded (C is undefined there).
func float32Inputs() []float32 {
	v := []float32{0, float32(math.Copysign(0, -1)), float32(math.Inf(1)), float32(math.Inf(-1)),
		math.MaxFloat32, -math.MaxFloat32, math.SmallestNonzeroFloat32, -math.SmallestNonzeroFloat32,
		1, -1, 0.5, -0.5, 1.5, -1.5}
	for _, b := range []float32{-32768.5, -32768, -32767.5, -32767, -32766.5, -1, 0, 1, 32766, 32766.5, 32767, 32767.5, 32768} {
		v = append(v, b, math.Nextafter32(b, float32(math.Inf(1))), math.Nextafter32(b, float32(math.Inf(-1))))
	}
	r := testutil.SplitMix64{State: testutil.Seed("word2int_f32")}
	for i := 0; i < 1000000; i++ {
		v = append(v, float32(int32(r.Next()%80000001)-40000000)/1000)
	}
	for i := 0; i < 1000000; {
		f := math.Float32frombits(uint32(r.Next()))
		if f != f {
			continue
		}
		v = append(v, f)
		i++
	}
	return v
}

func float64Inputs() []float64 {
	v := []float64{0, math.Copysign(0, -1), math.Inf(1), math.Inf(-1),
		math.MaxFloat64, -math.MaxFloat64, math.SmallestNonzeroFloat64, -math.SmallestNonzeroFloat64,
		1, -1, 0.5, -0.5, 1.5, -1.5}
	for _, b := range []float64{-32768.5, -32768, -32767.5, -32767, -32766.5, -1, 0, 1, 32766, 32766.5, 32767, 32767.5, 32768} {
		v = append(v, b, math.Nextafter(b, math.Inf(1)), math.Nextafter(b, math.Inf(-1)))
	}
	r := testutil.SplitMix64{State: testutil.Seed("word2int_f64")}
	for i := 0; i < 1000000; i++ {
		v = append(v, float64(int64(r.Next()%80000000001)-40000000000)/1000000)
	}
	for i := 0; i < 1000000; {
		f := math.Float64frombits(r.Next())
		if f != f {
			continue
		}
		v = append(v, f)
		i++
	}
	return v
}

// TestHarnessInputs writes the Go-generated driver inputs. It runs only when
// reference/fixtures.sh passes -harness-inputs.
func TestHarnessInputs(t *testing.T) {
	if *harnessInputs == "" {
		t.Skip("writes harness inputs only when -harness-inputs is set (reference/fixtures.sh)")
	}
	write := func(name string, b []byte) {
		if err := os.WriteFile(filepath.Join(*harnessInputs, name), b, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	write("word2int_f32.in.raw", testutil.EncodeLE(nil, float32Inputs()))
	write("word2int_f64.in.raw", testutil.EncodeLE(nil, float64Inputs()))
}

func TestReferenceWord2IntFloat(t *testing.T) {
	m := testutil.LoadManifest(t, fixtureDir)
	t.Run("float32", func(t *testing.T) {
		c := m.Case(t, "word2int_f32")
		in := testutil.NewStreamChecker[float32](t, m, c, c.Stream(t, "in"), false)
		out := testutil.NewStreamChecker[int32](t, m, c, c.Stream(t, "out"), false)
		for _, x := range float32Inputs() {
			in.Put(x)
			out.Put(int32(Word2IntFloat32(x)))
		}
		in.Done()
		out.Done()
	})
	t.Run("float64", func(t *testing.T) {
		c := m.Case(t, "word2int_f64")
		in := testutil.NewStreamChecker[float64](t, m, c, c.Stream(t, "in"), false)
		out := testutil.NewStreamChecker[int32](t, m, c, c.Stream(t, "out"), false)
		for _, x := range float64Inputs() {
			in.Put(x)
			out.Put(int32(Word2IntFloat64(x)))
		}
		in.Done()
		out.Done()
	})
}

// TestReferenceQConst checks QConst16 and QConst32 against the compiler's
// evaluation of every literal upstream passes to QCONST16 and QCONST32.
func TestReferenceQConst(t *testing.T) {
	m := testutil.LoadManifest(t, fixtureDir)
	for _, name := range []string{"qconst16", "qconst32"} {
		c := m.Case(t, name)
		var p struct {
			Literals [][2]any `json:"literals"`
		}
		c.DecodeParams(t, &p)
		out := testutil.NewStreamChecker[int32](t, m, c, c.Stream(t, "out"), false)
		for _, l := range p.Literals {
			lit, bits := l[0].(string), uint(l[1].(float64))
			x := parseCLiteral(t, lit)
			if name == "qconst16" {
				out.Put(int32(QConst16(x, bits)))
			} else {
				out.Put(QConst32(x, bits))
			}
		}
		out.Done()
	}
}

// parseCLiteral reads a C floating or integer literal; an f suffix rounds it
// to float32, as the C compiler does.
func parseCLiteral(t *testing.T, lit string) float64 {
	t.Helper()
	if s, ok := strings.CutSuffix(lit, "f"); ok {
		v, err := strconv.ParseFloat(s, 32)
		if err != nil {
			t.Fatalf("literal %q: %v", lit, err)
		}
		return float64(float32(v))
	}
	v, err := strconv.ParseFloat(lit, 64)
	if err != nil {
		t.Fatalf("literal %q: %v", lit, err)
	}
	return v
}
