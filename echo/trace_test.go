package echo

import (
	"encoding/binary"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"testing"

	"github.com/danieldonoghue/govoip-dsp/internal/fixed"
)

// State tracing, for finding where the port first diverges from the
// reference. Produce a trace with the driver (docs/testing.md, "echo"):
//
//	HARNESS_ECHO_TRACE=<dir> HARNESS_ECHO_TRACE_CASE=<case> [HARNESS_ECHO_DUMP_OP=<k>] \
//	    reference/build/<mode>/bin/echo <scratch-out> reference/build/in/echo
//
// then run the reference test with GOVOIP_ECHO_TRACE=<dir> (and
// GOVOIP_ECHO_DUMP_OP=<k> for an element-wise comparison of op k). The test
// reports the first op after which a state variable differs.

const (
	traceEnv  = "GOVOIP_ECHO_TRACE"
	dumpOpEnv = "GOVOIP_ECHO_DUMP_OP"
)

type traceVar struct {
	name string
	v    []int32
}

func widen16(x []int16) []int32 {
	v := make([]int32, len(x))
	for i, e := range x {
		v[i] = int32(e)
	}
	return v
}

func widenF(x ...fixed.Float) []int32 {
	v := make([]int32, 0, 2*len(x))
	for _, f := range x {
		v = append(v, int32(f.M), int32(f.E))
	}
	return v
}

func b2i(b bool) int32 {
	if b {
		return 1
	}
	return 0
}

// traceVars lists the state in the order of trace_state in
// reference/drivers/echo.c.
func traceVars(c *state) []traceVar {
	one := func(name string, x int32) traceVar { return traceVar{name, []int32{x}} }
	return []traceVar{
		one("cancel_count", c.cancelCount),
		one("adapted", b2i(c.adapted)),
		one("saturated", int32(c.saturated)),
		one("screwed_up", int32(c.screwedUp)),
		one("sampling_rate", int32(c.samplingRate)),
		one("spec_average", int32(c.specAverage)),
		one("beta0", int32(c.beta0)),
		one("beta_max", int32(c.betaMax)),
		one("sum_adapt", c.sumAdapt),
		one("leak_estimate", int32(c.leakEstimate)),
		one("Davg1", c.davg1),
		one("Davg2", c.davg2),
		{"Dvar1", widenF(c.dvar1)},
		{"Dvar2", widenF(c.dvar2)},
		{"Pey", widenF(c.pey)},
		{"Pyy", widenF(c.pyy)},
		one("memX", int32(c.memX)),
		one("memD", int32(c.memD)),
		one("memE", int32(c.memE)),
		one("preemph", int32(c.preemph)),
		one("notch_radius", int32(c.notchRad)),
		{"notch_mem", c.notchMem[:]},
		one("play_buf_pos", int32(c.playBufPos)),
		one("play_buf_started", b2i(c.playBufStarted)),
		{"e", widen16(c.e)},
		{"x", widen16(c.x)},
		{"X", widen16(c.X)},
		{"input", widen16(c.input)},
		{"y", widen16(c.y)},
		{"last_y", widen16(c.lastY)},
		{"Y", widen16(c.Y)},
		{"E", widen16(c.E)},
		{"PHI", c.PHI},
		{"W", c.W},
		{"foreground", widen16(c.foreground)},
		{"power", c.power},
		{"power_1", widenF(c.power1...)},
		{"wtmp", widen16(c.wtmp)},
		{"wtmp2", widen16(c.wtmp2)},
		{"Rf", c.Rf},
		{"Yf", c.Yf},
		{"Xf", c.Xf},
		{"Eh", c.Eh},
		{"Yh", c.Yh},
		{"window", widen16(c.window)},
		{"prop", widen16(c.prop)},
		{"play_buf", widen16(c.playBuf)},
	}
}

func fnv32s(v []int32) uint64 {
	h := uint64(0xcbf29ce484222325)
	for _, x := range v {
		u := uint32(x)
		for b := 0; b < 4; b++ {
			h ^= uint64(u>>(8*b)) & 0xff
			h *= 0x100000001b3
		}
	}
	return h
}

type tracer struct {
	t      *testing.T
	st     *state
	f      *os.File
	dumpOp int
	dump   []byte
	buf    []byte
}

// openTrace returns a tracer when GOVOIP_ECHO_TRACE holds a trace of case.
func openTrace(t *testing.T, name string, ec *Canceller) *tracer {
	dir := os.Getenv(traceEnv)
	if dir == "" {
		return nil
	}
	f, err := os.Open(filepath.Join(dir, name+".trace"))
	if err != nil {
		return nil
	}
	tr := &tracer{t: t, st: ec.st, f: f, dumpOp: -1}
	if s := os.Getenv(dumpOpEnv); s != "" {
		if tr.dumpOp, err = strconv.Atoi(s); err != nil {
			t.Fatalf("%s: %v", dumpOpEnv, err)
		}
		if tr.dump, err = os.ReadFile(filepath.Join(dir, name+".dump")); err != nil {
			t.Fatalf("%s is set: %v", dumpOpEnv, err)
		}
	}
	t.Logf("comparing the state trace %s", f.Name())
	return tr
}

// check compares the state after op k with the trace. It returns false,
// reporting the first differing variable, on a mismatch.
func (tr *tracer) check(k int) bool {
	vars := traceVars(tr.st)
	if len(tr.buf) != 8*len(vars) {
		tr.buf = make([]byte, 8*len(vars))
	}
	if _, err := io.ReadFull(tr.f, tr.buf); err != nil {
		tr.t.Errorf("trace ended before op %d: %v", k, err)
		return false
	}
	ok := true
	for i, v := range vars {
		if binary.LittleEndian.Uint64(tr.buf[8*i:]) != fnv32s(v.v) {
			tr.t.Errorf("state trace: first difference after op %d in %s (%d values)", k, v.name, len(v.v))
			ok = false
			break
		}
	}
	if k == tr.dumpOp {
		tr.compareDump(k, vars)
	}
	return ok
}

func (tr *tracer) compareDump(k int, vars []traceVar) {
	off := 0
	for _, v := range vars {
		for i, x := range v.v {
			if off+4 > len(tr.dump) {
				tr.t.Errorf("dump of op %d ends inside %s", k, v.name)
				return
			}
			if want := int32(binary.LittleEndian.Uint32(tr.dump[off:])); want != x {
				tr.t.Errorf("dump of op %d: %s[%d] = %d, want %d", k, v.name, i, x, want)
				return
			}
			off += 4
		}
	}
	tr.t.Logf("dump of op %d: all %d variables equal", k, len(vars))
}

func (tr *tracer) close() { tr.f.Close() }
