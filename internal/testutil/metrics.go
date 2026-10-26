package testutil

import (
	"math"
	"math/cmplx"
)

// Metrics report signal quality in human terms. Levels are on the 16-bit
// scale; dBFS is relative to a full-scale amplitude of 32768.

// RMS returns the root mean square of x, or 0 for an empty slice.
func RMS(x []int16) float64 {
	if len(x) == 0 {
		return 0
	}
	return math.Sqrt(energy(x) / float64(len(x)))
}

// DBFS returns 20*log10(level/32768). A zero level gives -Inf.
func DBFS(level float64) float64 {
	return 20 * math.Log10(level/32768)
}

// SNR returns 10*log10(sum(ref^2)/sum((test-ref)^2)) in dB over the common
// length: +Inf if test equals a non-silent ref, NaN if both are silent.
func SNR(ref, test []int16) float64 {
	n := min(len(ref), len(test))
	return ratioDB(energy(ref[:n]), errEnergy(ref[:n], test[:n]))
}

// SegmentalSNR is the mean per-frame SNR, each clamped to [-10, 35] dB.
// Silent reference frames are skipped; with none left the result is NaN.
func SegmentalSNR(ref, test []int16, frame int) float64 {
	n := min(len(ref), len(test))
	sum, count := 0.0, 0
	for start := 0; frame > 0 && start+frame <= n; start += frame {
		r, e := ref[start:start+frame], test[start:start+frame]
		er := energy(r)
		if er == 0 {
			continue
		}
		sum += max(-10, min(35, ratioDB(er, errEnergy(r, e))))
		count++
	}
	if count == 0 {
		return math.NaN()
	}
	return sum / float64(count)
}

// ERLE returns 10*log10(sum(echo^2)/sum(residual^2)) in dB: echo before
// cancellation against what remains after it.
func ERLE(echo, residual []int16) float64 {
	n := min(len(echo), len(residual))
	return ratioDB(energy(echo[:n]), energy(residual[:n]))
}

// SpectralFlatness is the mean over Hann-windowed power-of-two frames of
// geometric over arithmetic mean power (1 for white noise, 0 for a tone).
func SpectralFlatness(x []int16, frame int) float64 {
	if frame < 4 || frame&(frame-1) != 0 {
		return math.NaN()
	}
	buf := make([]complex128, frame)
	sum, count := 0.0, 0
	for start := 0; start+frame <= len(x); start += frame {
		for i := range buf {
			w := 0.5 - 0.5*math.Cos(2*math.Pi*float64(i)/float64(frame))
			buf[i] = complex(w*float64(x[start+i]), 0)
		}
		fft(buf)
		logSum, linSum := 0.0, 0.0
		bins := frame/2 - 1
		for k := 1; k <= bins; k++ {
			p := real(buf[k])*real(buf[k]) + imag(buf[k])*imag(buf[k])
			logSum += math.Log(p) // -Inf for an empty bin: flatness 0
			linSum += p
		}
		if linSum == 0 {
			continue
		}
		sum += math.Exp(logSum/float64(bins)) / (linSum / float64(bins))
		count++
	}
	if count == 0 {
		return math.NaN()
	}
	return sum / float64(count)
}

// fft is an in-place iterative radix-2 FFT; len(a) must be a power of two.
func fft(a []complex128) {
	n := len(a)
	for i, j := 1, 0; i < n; i++ {
		bit := n >> 1
		for ; j&bit != 0; bit >>= 1 {
			j ^= bit
		}
		j ^= bit
		if i < j {
			a[i], a[j] = a[j], a[i]
		}
	}
	for size := 2; size <= n; size <<= 1 {
		w := cmplx.Exp(complex(0, -2*math.Pi/float64(size)))
		for start := 0; start < n; start += size {
			wk := complex(1, 0)
			for k := 0; k < size/2; k++ {
				u, v := a[start+k], a[start+k+size/2]*wk
				a[start+k], a[start+k+size/2] = u+v, u-v
				wk *= w
			}
		}
	}
}

func energy(x []int16) float64 {
	e := 0.0
	for _, v := range x {
		e += float64(v) * float64(v)
	}
	return e
}

func errEnergy(ref, test []int16) float64 {
	e := 0.0
	for i := range ref {
		d := float64(test[i]) - float64(ref[i])
		e += d * d
	}
	return e
}

func ratioDB(num, den float64) float64 {
	switch {
	case den == 0 && num == 0:
		return math.NaN()
	case den == 0:
		return math.Inf(1)
	case num == 0:
		return math.Inf(-1)
	}
	return 10 * math.Log10(num/den)
}
