//go:build gopus_custom

package custom

import (
	"errors"
	"math"

	"github.com/thesyncim/gopus/celt"
)

// Error values returned by the Custom API.
var (
	ErrBadArg    = errors.New("opus custom: bad argument")
	ErrAllocFail = errors.New("opus custom: allocation failed")
)

// staticFrameSizes is the set of (Fs, frame_size) pairs that correspond to the
// standard Opus static modes, in the same check order as libopus modes.c
// opus_custom_mode_create():
//
//	if Fs == static_mode_list[i]->Fs && (frame_size<<j) == shortMdctSize * nbShortMdcts
//
// where j in 0..3 (the on-the-fly 2× size-switch table).
// At 48 kHz shortMdctSize is 120, nbShortMdcts is 1/2/4/8 → sizes 120,240,480,960.
// Reference: libopus celt/modes.c opus_custom_mode_create() lines 244-258.
type staticEntry struct {
	Fs        int
	FrameSize int // base shortMdctSize * nbShortMdcts
}

var staticModes = []staticEntry{
	{48000, 120}, // 2.5ms
	{48000, 240}, // 5ms
	{48000, 480}, // 10ms
	{48000, 960}, // 20ms
}

// isStandardFrame reports whether (Fs, frameSize) is a standard Opus static mode
// or any of its on-the-fly doubles (up to ×8), matching libopus mode.c detection.
// When true the existing celt encoder/decoder (hardwired to 48 kHz 120-sample base)
// can be used directly and will produce byte-identical output to libopus.
func isStandardFrame(fs, frameSize int) bool {
	for _, e := range staticModes {
		if fs != e.Fs {
			continue
		}
		s := e.FrameSize
		for j := 0; j < 4; j++ {
			if frameSize == s<<j {
				return true
			}
		}
	}
	return false
}

// bandBarkFreq contains the 25 Bark critical-band boundaries (in Hz) used by
// libopus compute_ebands() when Fs != 400*frame_size.
// Reference: libopus celt/modes.c bark_freq[].
var bandBarkFreq = [26]int{
	0, 100, 200, 300, 400,
	510, 630, 770, 920, 1080,
	1270, 1480, 1720, 2000, 2320,
	2700, 3150, 3700, 4400, 5300,
	6400, 7700, 9500, 12000, 15500,
	20000,
}

// eband5ms is the standard 5ms table (libopus celt/modes.c eband5ms).
var eband5ms = [22]int16{
	0, 1, 2, 3, 4, 5, 6, 7, 8, 10,
	12, 14, 16, 20, 24, 28, 34, 40, 48, 60,
	78, 100,
}

// bandAllocTable is the 11×21 allocation matrix from libopus celt/modes.c.
var bandAllocTable = [11][21]uint8{
	{0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0},
	{90, 80, 75, 69, 63, 56, 49, 40, 34, 29, 20, 18, 10, 0, 0, 0, 0, 0, 0, 0, 0},
	{110, 100, 90, 84, 78, 71, 65, 58, 51, 45, 39, 32, 26, 20, 12, 0, 0, 0, 0, 0, 0},
	{118, 110, 103, 93, 86, 80, 75, 70, 65, 59, 53, 47, 40, 31, 23, 15, 4, 0, 0, 0, 0},
	{126, 119, 112, 104, 95, 89, 83, 78, 72, 66, 60, 54, 47, 39, 32, 25, 17, 12, 1, 0, 0},
	{134, 127, 120, 114, 103, 97, 91, 85, 78, 72, 66, 60, 54, 47, 41, 35, 29, 23, 16, 10, 1},
	{144, 137, 130, 124, 113, 107, 101, 95, 88, 82, 76, 70, 64, 57, 51, 45, 39, 33, 26, 15, 1},
	{152, 145, 138, 132, 123, 117, 111, 105, 98, 92, 86, 80, 74, 67, 61, 55, 49, 43, 36, 20, 1},
	{162, 155, 148, 142, 133, 127, 121, 115, 108, 102, 96, 90, 84, 77, 71, 65, 59, 53, 46, 30, 1},
	{172, 165, 158, 152, 143, 137, 131, 125, 118, 112, 106, 100, 94, 87, 81, 75, 69, 63, 56, 45, 20},
	{200, 200, 200, 200, 200, 200, 200, 200, 198, 193, 188, 183, 178, 173, 168, 163, 158, 153, 148, 129, 104},
}

// maxEBands5ms is the number of bands in the standard 5ms table (21 bands, 22 edges).
const maxBands5ms = 21

// CustomMode holds all the information necessary to create a CustomEncoder or
// CustomDecoder for a given (Fs, frame_size) pair.
//
// It mirrors libopus CELTMode as exposed through opus_custom.h, but is a pure
// Go value rather than an opaque pointer. Callers must keep the mode alive for
// as long as any encoder or decoder created from it is in use.
//
// Reference: libopus celt/modes.h CELTMode, celt/modes.c opus_custom_mode_create().
type CustomMode struct {
	Fs            int       // Sample rate in Hz (8000–96000)
	FrameSize     int       // Samples per frame per channel
	ShortMdctSize int       // frameSize / nbShortMdcts (= frameSize >> maxLM)
	NbShortMdcts  int       // 1 << maxLM
	MaxLM         int       // log2(nbShortMdcts)
	Overlap       int       // MDCT overlap window size = (shortMdctSize >> 2) << 2
	NbEBands      int       // Number of frequency bands
	EffEBands     int       // Effective band count (bands within shortMdctSize)
	EBands        []int16   // Band edge positions (NbEBands+1 values)
	AllocVectors  []uint8   // 11 × NbEBands allocation table
	Window        []float32 // Overlap window values (length = Overlap)
	LogN          []int16   // log2(band_width) in Q3 per band (NbEBands values)
	// Preemphasis / de-emphasis coefficients, matching libopus mode->preemph[0..3].
	// [0] = coef_a (pre-emphasis), [1] = coef_b, [2] = scale (1/preemph[3]),
	// [3] = inverse scale.
	Preemph [4]float32
	// CacheIndex maps (LM+1, band) -> offset into CacheBits, length
	// NbEBands*(MaxLM+2). CacheBits is the concatenated pulse cache.
	// CacheCaps is the (MaxLM+1)*2*NbEBands per-band PVQ cap table.
	// These mirror libopus PulseCache (mode->cache.{index,bits,caps}) and are
	// computed by compute_pulse_cache at mode-create time for non-standard modes.
	// Reference: libopus celt/rate.c compute_pulse_cache(), celt/modes.h PulseCache.
	CacheIndex []int16
	CacheBits  []uint8
	CacheCaps  []uint8
	// isStandard is true when this mode maps to one of the four libopus static
	// modes (48 kHz, 120/240/480/960 samples). Standard modes can be encoded
	// and decoded with byte-exact libopus parity using the existing celt package.
	isStandard bool
}

// NewMode creates a CustomMode for the given sample rate and frame size.
// It validates the arguments exactly as libopus opus_custom_mode_create() does
// (Fs in 8000–96000, frame_size in 40–1024, even, frame_size*1000 >= Fs, short
// block ≤ 3.3ms).  For standard Opus frame sizes at 48 kHz the returned mode
// maps to the existing static mode so encode/decode will be byte-identical to
// libopus.
//
// Reference: libopus celt/modes.c opus_custom_mode_create().
func NewMode(fs, frameSize int) (*CustomMode, error) {
	// Validation mirrors libopus lines 268-313.
	if fs < 8000 || fs > 96000 {
		return nil, ErrBadArg
	}
	if frameSize < 40 || frameSize > 1024 || frameSize%2 != 0 {
		return nil, ErrBadArg
	}
	// Frames shorter than 1 ms are not supported.
	if int64(frameSize)*1000 < int64(fs) {
		return nil, ErrBadArg
	}

	// Compute maxLM (log2(nbShortMdcts)).
	// Reference: libopus celt/modes.c lines 293-305.
	var maxLM int
	switch {
	case int64(frameSize)*75 >= int64(fs) && frameSize%16 == 0:
		maxLM = 3
	case int64(frameSize)*150 >= int64(fs) && frameSize%8 == 0:
		maxLM = 2
	case int64(frameSize)*300 >= int64(fs) && frameSize%4 == 0:
		maxLM = 1
	default:
		maxLM = 0
	}

	nbShortMdcts := 1 << maxLM
	shortMdctSize := frameSize / nbShortMdcts

	// Short blocks longer than 3.3 ms are not supported.
	// Reference: libopus celt/modes.c lines 307-312.
	if int64(shortMdctSize)*300 > int64(fs) {
		return nil, ErrBadArg
	}

	// Overlap must be divisible by 4. Reference: libopus modes.c line 380.
	overlap := (shortMdctSize >> 2) << 2

	mode := &CustomMode{
		Fs:            fs,
		FrameSize:     frameSize,
		ShortMdctSize: shortMdctSize,
		NbShortMdcts:  nbShortMdcts,
		MaxLM:         maxLM,
		Overlap:       overlap,
	}

	// Check for standard mode first (like libopus does before CUSTOM_MODES path).
	// Reference: libopus celt/modes.c lines 244-258.
	mode.isStandard = isStandardFrame(fs, frameSize)

	// Compute band table.
	// When fs == 400*frame_size (the 5ms-equivalent condition) use the 5ms table.
	// Reference: libopus celt/modes.c compute_ebands() lines 96-103.
	res := (fs + shortMdctSize) / (2 * shortMdctSize)
	eBands, nbEBands, err := computeEBands(fs, shortMdctSize, res)
	if err != nil {
		return nil, err
	}
	mode.EBands = eBands
	mode.NbEBands = nbEBands

	// Compute effEBands: highest band whose end <= shortMdctSize.
	// Reference: libopus celt/modes.c lines 375-377.
	effEBands := nbEBands
	for effEBands > 0 && int(eBands[effEBands]) > shortMdctSize {
		effEBands--
	}
	mode.EffEBands = effEBands

	// Compute allocation table.
	// Reference: libopus celt/modes.c compute_allocation_table().
	mode.AllocVectors = computeAllocVectors(mode)

	// Compute window.
	// Reference: libopus celt/modes.c lines 386-401:
	//   window[i] = Q15ONE * sin(0.5*π * sin(0.5*π*(i+0.5)/overlap)^2)
	win := make([]float32, overlap)
	for i := 0; i < overlap; i++ {
		x := (float64(i) + 0.5) / float64(overlap)
		s := math.Sin(0.5 * math.Pi * x)
		win[i] = float32(math.Sin(0.5 * math.Pi * s * s))
	}
	mode.Window = win

	// Compute logN per band.
	// Reference: libopus celt/modes.c lines 404-409:
	//   logN[i] = log2_frac(eBands[i+1]-eBands[i], BITRES)  where BITRES=3
	logN := make([]int16, nbEBands)
	for i := 0; i < nbEBands; i++ {
		bw := int(eBands[i+1]) - int(eBands[i])
		logN[i] = int16(log2Frac(bw, 3))
	}
	mode.LogN = logN

	// Compute preemphasis coefficients by sample rate.
	// Reference: libopus celt/modes.c lines 322-356.
	mode.Preemph = preemphForFs(fs)

	// Compute the pulse cache (index/bits/caps).
	// Reference: libopus celt/rate.c compute_pulse_cache(mode, maxLM).
	computePulseCache(mode)

	return mode, nil
}

// preemphForFs returns the [4]float32 preemphasis table matching
// libopus celt/modes.c opus_custom_mode_create() per-rate assignments.
// The values are in float (not fixed-point) regardless of build config.
// Reference: libopus celt/modes.c lines 322-356 (FIXED_POINT=0 branch).
func preemphForFs(fs int) [4]float32 {
	switch {
	case fs < 12000: // 8 kHz
		return [4]float32{0.3500061035, -0.1799926758, 0.2719968125, 3.6765136719}
	case fs < 24000: // 16 kHz
		return [4]float32{0.6000061035, -0.1799926758, 0.4424998650, 2.2598876953}
	case fs < 40000: // 32 kHz
		return [4]float32{0.7799987793, -0.1000061035, 0.7499771125, 1.3333740234}
	default: // 48 kHz (and 96 kHz treated same in non-QEXT builds)
		return [4]float32{0.8500061035, 0.0, 1.0, 1.0}
	}
}

// computeEBands computes the band-edge table for a custom mode.
// Reference: libopus celt/modes.c compute_ebands().
func computeEBands(fs, shortMdctSize, res int) ([]int16, int, error) {
	// When fs == 400 * shortMdctSize: same as the 5ms standard table.
	// Reference: libopus modes.c lines 96-103.
	if fs == 400*shortMdctSize {
		bands := make([]int16, len(eband5ms))
		copy(bands, eband5ms[:])
		return bands, maxBands5ms, nil
	}

	// Find the number of Bark critical bands supported at this sample rate.
	nBark := 1
	for nBark < 25 {
		if bandBarkFreq[nBark+1]*2 >= fs {
			break
		}
		nBark++
	}

	// Find where the linear part ends.
	lin := 0
	for lin < nBark {
		if bandBarkFreq[lin+1]-bandBarkFreq[lin] >= res {
			break
		}
		lin++
	}

	low := (bandBarkFreq[lin] + res/2) / res
	high := nBark - lin
	nbEBands := low + high

	eBands := make([]int16, nbEBands+2)

	// Linear spacing.
	for i := 0; i < low; i++ {
		eBands[i] = int16(i)
	}
	offset := 0
	if low > 0 {
		offset = int(eBands[low-1])*res - bandBarkFreq[lin-1]
	}
	// Critical-band spacing.
	for i := 0; i < high; i++ {
		target := bandBarkFreq[lin+i]
		eBands[i+low] = int16((target + offset/2 + res) / (2 * res) * 2)
		offset = int(eBands[i+low])*res - target
	}
	// Enforce minimum spacing.
	for i := 0; i < nbEBands; i++ {
		if int(eBands[i]) < i {
			eBands[i] = int16(i)
		}
	}
	// Round end band.
	eBands[nbEBands] = int16((bandBarkFreq[nBark] + res) / (2 * res) * 2)
	if int(eBands[nbEBands]) > shortMdctSize {
		eBands[nbEBands] = int16(shortMdctSize)
	}
	// Smooth monotone-increasing constraint.
	for i := 1; i < nbEBands-1; i++ {
		if eBands[i+1]-eBands[i] < eBands[i]-eBands[i-1] {
			eBands[i] -= (2*eBands[i] - eBands[i-1] - eBands[i+1]) / 2
		}
	}
	// Remove empty bands (compact).
	j := 0
	for i := 0; i < nbEBands; i++ {
		if eBands[i+1] > eBands[j] {
			j++
			eBands[j] = eBands[i+1]
		}
	}
	nbEBands = j

	return eBands[:nbEBands+1], nbEBands, nil
}

// computeAllocVectors builds the 11×nbEBands bit-allocation matrix.
// For the standard 5ms case (fs == 400*shortMdctSize) it copies the table
// directly; otherwise it interpolates from the 5ms table on a per-Bark basis.
// Reference: libopus celt/modes.c compute_allocation_table().
func computeAllocVectors(m *CustomMode) []uint8 {
	alloc := make([]uint8, 11*m.NbEBands)

	// Standard case: copy directly.
	if m.Fs == 400*m.ShortMdctSize {
		for i := 0; i < 11*m.NbEBands; i++ {
			alloc[i] = bandAllocTable[i/m.NbEBands][i%m.NbEBands]
		}
		return alloc
	}

	// Interpolate per-band from the 5ms table.
	// Reference: libopus celt/modes.c compute_allocation_table() lines 191-211.
	// bandHz is computed in int32 arithmetic exactly as libopus:
	//   mode->eBands[j]*(opus_int32)mode->Fs/mode->shortMdctSize
	// (multiply then integer-divide; the products fit in int32 for all valid
	// custom modes). k is the first 5ms band whose 400*eband5ms[k] exceeds
	// bandHz; k is always >=1 because eBands[0]==0 forces 400*eband5ms[0]==0,
	// which is never > bandHz>=0.
	fs := int32(m.Fs)
	smdct := int32(m.ShortMdctSize)
	for i := 0; i < 11; i++ {
		for j := 0; j < m.NbEBands; j++ {
			bandHz := int32(m.EBands[j]) * fs / smdct
			k := 0
			for k < maxBands5ms {
				if int32(400)*int32(eband5ms[k]) > bandHz {
					break
				}
				k++
			}
			if k > maxBands5ms-1 {
				alloc[i*m.NbEBands+j] = bandAllocTable[i][maxBands5ms-1]
			} else {
				a1 := bandHz - int32(400)*int32(eband5ms[k-1])
				a0 := int32(400)*int32(eband5ms[k]) - bandHz
				v := (a0*int32(bandAllocTable[i][k-1]) + a1*int32(bandAllocTable[i][k])) / (a0 + a1)
				alloc[i*m.NbEBands+j] = uint8(v)
			}
		}
	}
	return alloc
}

// CELT bit-allocation constants. Reference: libopus celt/rate.h, celt/arch.h.
const (
	bitRes               = 3
	maxFineBits          = 8
	fineOffset           = 21
	qthetaOffset         = 4
	qthetaOffsetTwoPhase = 16
	maxPseudo            = 40
	celtMaxPulses        = 128
)

// getPulses maps a pseudo-pulse index i to the actual pulse count.
// Reference: libopus celt/rate.h get_pulses().
func getPulses(i int) int {
	if i < 8 {
		return i
	}
	return (8 + (i & 7)) << ((i >> 3) - 1)
}

// fitsIn32 reports whether V(N,K) fits in a 32-bit unsigned integer.
// Reference: libopus celt/rate.c fits_in32().
func fitsIn32(n, k int) bool {
	maxN := [15]int16{32767, 32767, 32767, 1476, 283, 109, 60, 40, 29, 24, 20, 18, 16, 14, 13}
	maxK := [15]int16{32767, 32767, 32767, 32767, 1172, 238, 95, 53, 36, 27, 22, 18, 16, 15, 13}
	if n >= 14 {
		if k >= 14 {
			return false
		}
		return n <= int(maxN[k])
	}
	return k <= int(maxK[n])
}

// getRequiredBits fills bits[1..maxk] with the number of bits (Q-frac) required
// to code a PVQ of dimension n with up to maxk pulses. bits[0] is set to 0.
// Reference: libopus celt/cwrs.c get_required_bits() (CUSTOM_MODES path).
func getRequiredBits(bits []int16, n, maxk, frac int) {
	bits[0] = 0
	if n == 1 {
		for k := 1; k <= maxk; k++ {
			bits[k] = int16(1 << frac)
		}
		return
	}
	u := make([]uint32, maxk+2)
	celt.NcwrsUrowExport(n, maxk, u)
	for k := 1; k <= maxk; k++ {
		bits[k] = int16(log2Frac(int(u[k]+u[k+1]), frac))
	}
}

// computePulseCache builds the per-mode pulse cache (index/bits/caps) for a
// non-standard custom mode. It is a bit-exact port of libopus
// compute_pulse_cache(m, LM) with LM == m.MaxLM.
//
// Reference: libopus celt/rate.c compute_pulse_cache().
func computePulseCache(m *CustomMode) {
	lm := m.MaxLM
	eBands := m.EBands
	nb := m.NbEBands

	cindex := make([]int16, nb*(lm+2))
	var entryN, entryK, entryI [100]int
	nbEntries := 0
	curr := 0

	// Scan for all unique band sizes.
	for i := 0; i <= lm+1; i++ {
		for j := 0; j < nb; j++ {
			N := (int(eBands[j+1]) - int(eBands[j])) << i >> 1
			cindex[i*nb+j] = -1
			// Find other bands that have the same size.
			for k := 0; k <= i; k++ {
				for n := 0; n < nb && (k != i || n < j); n++ {
					if N == (int(eBands[n+1])-int(eBands[n]))<<k>>1 {
						cindex[i*nb+j] = cindex[k*nb+n]
						break
					}
				}
				if cindex[i*nb+j] != -1 {
					break
				}
			}
			if cindex[i*nb+j] == -1 && N != 0 {
				entryN[nbEntries] = N
				K := 0
				for fitsIn32(N, getPulses(K+1)) && K < maxPseudo {
					K++
				}
				entryK[nbEntries] = K
				cindex[i*nb+j] = int16(curr)
				entryI[nbEntries] = curr
				curr += K + 1
				nbEntries++
			}
		}
	}

	bits := make([]uint8, curr)
	// Compute the cache for all unique sizes.
	for i := 0; i < nbEntries; i++ {
		ptr := bits[entryI[i]:]
		var tmp [celtMaxPulses + 1]int16
		getRequiredBits(tmp[:], entryN[i], getPulses(entryK[i]), bitRes)
		for j := 1; j <= entryK[i]; j++ {
			ptr[j] = uint8(tmp[getPulses(j)] - 1)
		}
		ptr[0] = uint8(entryK[i])
	}

	// Compute the maximum rate per band/channel/LM (caps).
	caps := make([]uint8, (lm+1)*2*nb)
	cp := 0
	for i := 0; i <= lm; i++ {
		for C := 1; C <= 2; C++ {
			for j := 0; j < nb; j++ {
				N0 := int(eBands[j+1]) - int(eBands[j])
				var maxBits int
				if N0<<i == 1 {
					maxBits = C * (1 + maxFineBits) << bitRes
				} else {
					LM0 := 0
					if N0 > 2 {
						N0 >>= 1
						LM0--
					} else if N0 <= 1 {
						LM0 = i
						if LM0 > 1 {
							LM0 = 1
						}
						N0 <<= LM0
					}
					pcache := bits[cindex[(LM0+1)*nb+j]:]
					maxBits = int(pcache[pcache[0]]) + 1
					N := N0
					for k := 0; k < i-LM0; k++ {
						maxBits <<= 1
						offset := ((int(m.LogN[j]) + ((LM0 + k) << bitRes)) >> 1) - qthetaOffset
						num := 459 * ((2*N-1)*offset + maxBits)
						den := ((2*N - 1) << 9) - 459
						qb := (num + (den >> 1)) / den
						if qb > 57 {
							qb = 57
						}
						maxBits += qb
						N <<= 1
					}
					if C == 2 {
						maxBits <<= 1
						qoff := qthetaOffset
						if N == 2 {
							qoff = qthetaOffsetTwoPhase
						}
						offset := ((int(m.LogN[j]) + (i << bitRes)) >> 1) - qoff
						ndof := 2*N - 1
						if N == 2 {
							ndof--
						}
						mul := 487
						sub := 487
						qbCap := 61
						if N == 2 {
							mul = 512
							sub = 512
							qbCap = 64
						}
						num := mul * (maxBits + ndof*offset)
						den := (ndof << 9) - sub
						qb := (num + (den >> 1)) / den
						if qb > qbCap {
							qb = qbCap
						}
						maxBits += qb
					}
					ndof := C * N
					if C == 2 && N > 2 {
						ndof++
					}
					offset := ((int(m.LogN[j]) + (i << bitRes)) >> 1) - fineOffset
					if N == 2 {
						offset += 1 << bitRes >> 2
					}
					num := maxBits + ndof*offset
					den := (ndof - 1) << bitRes
					qb := (num + (den >> 1)) / den
					if qb > maxFineBits {
						qb = maxFineBits
					}
					maxBits += C * qb << bitRes
				}
				maxBits = (4*maxBits)/(C*((int(eBands[j+1])-int(eBands[j]))<<i)) - 64
				caps[cp] = uint8(maxBits)
				cp++
			}
		}
	}

	m.CacheIndex = cindex
	m.CacheBits = bits
	m.CacheCaps = caps
}

// log2Frac is a bit-exact port of libopus log2_frac(): an integer fixed-point
// base-2 logarithm in Q(frac), used for mode->logN.
// Reference: libopus celt/cwrs.c log2_frac().
func log2Frac(val, frac int) int {
	if val <= 0 {
		return 0
	}
	v := uint32(val)
	l := ecILOG(v)
	if v&(v-1) != 0 {
		// Round up before the shift; guaranteed to round up.
		if l > 16 {
			v = ((v - 1) >> (l - 16)) + 1
		} else {
			v <<= 16 - l
		}
		l = (l - 1) << frac
		// At least one iteration always runs (frac-- post-decrement).
		for {
			b := int(v >> 16)
			l += b << frac
			v = (v + uint32(b)) >> uint(b)
			v = (v*v + 0x7FFF) >> 15
			if frac <= 0 {
				break
			}
			frac--
		}
		if v > 0x8000 {
			l++
		}
		return l
	}
	// Exact power of two: no rounding.
	return (l - 1) << frac
}

// ecILOG returns the number of bits needed to represent v (EC_ILOG): the
// position of the most-significant set bit plus one, or 0 for v == 0.
// Reference: libopus celt/ecintrin.h EC_ILOG.
func ecILOG(v uint32) int {
	l := 0
	for v != 0 {
		v >>= 1
		l++
	}
	return l
}

// maxNativeBands is the largest per-mode band count the native gopus CELT data
// plane currently supports. The static codec history and energy-prediction
// buffers (oldBandE/oldLogE/energyError and their encoder twins) are sized by
// the static MaxBands (21). A non-standard custom mode whose band layout exceeds
// that count (compute_ebands can yield up to ~24 bands at high Fs with a small
// short-MDCT, e.g. 32000/100 -> 22 bands) would index those buffers out of
// range, so such modes are declined with ErrNonStandard rather than crashing or
// emitting a non-conformant bitstream. libopus sizes every CELTMode buffer by
// the mode's own nbEBands, so this is a gopus-side capacity limit, not a libopus
// constraint.
//
// Reference: libopus celt/modes.c compute_ebands(), celt/celt_encoder.c /
// celt/celt_decoder.c oldBandE[i+c*m->nbEBands] indexing.
const maxNativeBands = 21

// nativeSupported reports whether gopus can drive this mode through the native
// CELT data plane. Standard modes and any non-standard mode whose band count is
// within maxNativeBands are supported; wider band layouts are declined with
// ErrNonStandard.
func (m *CustomMode) nativeSupported() bool {
	if m == nil {
		return false
	}
	if m.isStandard {
		return true
	}
	return m.NbEBands <= maxNativeBands
}

// IsStandard reports whether the mode corresponds to a standard Opus 48 kHz
// static mode (120/240/480/960 samples). When true, encode/decode produces
// output byte-identical to libopus.
func (m *CustomMode) IsStandard() bool {
	if m == nil {
		return false
	}
	return m.isStandard
}

// SampleRate returns the mode's sample rate in Hz.
func (m *CustomMode) SampleRate() int {
	if m == nil {
		return 0
	}
	return m.Fs
}

// Samples returns the mode's frame size in samples per channel.
func (m *CustomMode) Samples() int {
	if m == nil {
		return 0
	}
	return m.FrameSize
}

// PreemphCoef returns the de-emphasis coefficient (mode->preemph[0]).
// This is the value used by the CELT decoder de-emphasis filter.
func (m *CustomMode) PreemphCoef() float32 {
	if m == nil {
		return 0
	}
	return m.Preemph[0]
}

// InScaledBandFamily reports whether this mode belongs to the
// Fs==400*shortMdctSize family. Members of this family share the 48 kHz
// eBands/logN/allocVectors tables (computeEBands returns the 5 ms table for
// them), so the CELT control plane can be parameterized purely by the base
// short-MDCT size, overlap and per-rate pre-emphasis without re-deriving the
// allocation/cache tables.
//
// The four standard 48 kHz modes also satisfy Fs==400*shortMdctSize; they are
// excluded here because they already take the static-mode path.
func (m *CustomMode) InScaledBandFamily() bool {
	if m == nil {
		return false
	}
	return !m.isStandard && m.Fs == 400*m.ShortMdctSize
}

// celtConfig derives the celt.CustomModeConfig that parameterizes the CELT
// control plane for this family member.
func (m *CustomMode) celtConfig() celt.CustomModeConfig {
	return celt.CustomModeConfig{
		Fs:            m.Fs,
		FrameSize:     m.FrameSize,
		ShortMdctSize: m.ShortMdctSize,
		NbShortMdcts:  m.NbShortMdcts,
		LM:            m.MaxLM,
		Overlap:       m.Overlap,
		EffBands:      m.EffEBands,
		Preemph:       m.Preemph,
	}
}
