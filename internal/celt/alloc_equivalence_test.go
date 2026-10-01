package celt

import (
	"bytes"
	"math/rand"
	"slices"
	"testing"

	"github.com/thesyncim/gopus/internal/rangecoding"
)

// allocRefCoder is the entropy coder side of the reference allocation: exactly
// one of enc and dec is set.
type allocRefCoder struct {
	enc *rangecoding.Encoder
	dec *rangecoding.Decoder
}

// allocRefResult is everything clt_compute_allocation produces.
type allocRefResult struct {
	codedBands, balance, intensity, dualStereo int
	pulses, ebits, finePriority                []int32
}

// cltComputeAllocationRef is libopus celt/rate.c clt_compute_allocation() and
// interp_bits2pulses() for the static mode, written literally with the
// encoder's intensity clamp of cltComputeAllocationEncode.
func cltComputeAllocationRef(ec allocRefCoder, start, end int, offsets, cap []int32, allocTrim, intensity, dualStereo,
	total, channels, lm, prev, signalBandwidth int) allocRefResult {
	encode := ec.enc != nil
	C := channels
	total = max(total, 0)
	skipStart := start
	skipRsv := 0
	if total >= 1<<bitRes {
		skipRsv = 1 << bitRes
	}
	total -= skipRsv
	intensityRsv, dualStereoRsv := 0, 0
	if C == 2 {
		intensityRsv = int(log2FracTable[end-start])
		if intensityRsv > total {
			intensityRsv = 0
		} else {
			total -= intensityRsv
			if total >= 1<<bitRes {
				dualStereoRsv = 1 << bitRes
			}
			total -= dualStereoRsv
		}
	}
	bits1 := make([]int, MaxBands)
	bits2 := make([]int, MaxBands)
	thresh := make([]int, MaxBands)
	trimOffset := make([]int, MaxBands)
	for j := start; j < end; j++ {
		n := EBands[j+1] - EBands[j]
		thresh[j] = max(C<<bitRes, (3*n<<lm<<bitRes)>>4)
		trimOffset[j] = C * n * (allocTrim - 5 - lm) * (end - j - 1) * (1 << (lm + bitRes)) >> 6
		if n<<lm == 1 {
			trimOffset[j] -= C << bitRes
		}
	}
	lo, hi := 1, len(BandAlloc)-1
	for {
		done := false
		psum := 0
		mid := (lo + hi) >> 1
		for j := end - 1; j >= start; j-- {
			n := EBands[j+1] - EBands[j]
			bitsj := C * n * int(BandAlloc[mid][j]) << lm >> 2
			if bitsj > 0 {
				bitsj = max(0, bitsj+trimOffset[j])
			}
			bitsj += int(offsets[j])
			if bitsj >= thresh[j] || done {
				done = true
				psum += min(bitsj, int(cap[j]))
			} else if bitsj >= C<<bitRes {
				psum += C << bitRes
			}
		}
		if psum > total {
			hi = mid - 1
		} else {
			lo = mid + 1
		}
		if lo > hi {
			break
		}
	}
	hi = lo
	lo--
	for j := start; j < end; j++ {
		n := EBands[j+1] - EBands[j]
		bits1j := C * n * int(BandAlloc[lo][j]) << lm >> 2
		bits2j := int(cap[j])
		if hi < len(BandAlloc) {
			bits2j = C * n * int(BandAlloc[hi][j]) << lm >> 2
		}
		if bits1j > 0 {
			bits1j = max(0, bits1j+trimOffset[j])
		}
		if bits2j > 0 {
			bits2j = max(0, bits2j+trimOffset[j])
		}
		if lo > 0 {
			bits1j += int(offsets[j])
		}
		bits2j += int(offsets[j])
		if offsets[j] > 0 {
			skipStart = j
		}
		bits2j = max(0, bits2j-bits1j)
		bits1[j] = bits1j
		bits2[j] = bits2j
	}

	// interp_bits2pulses
	bits := make([]int, MaxBands)
	ebits := make([]int, MaxBands)
	finePriority := make([]int, MaxBands)
	allocFloor := C << bitRes
	stereo := 0
	if C > 1 {
		stereo = 1
	}
	logM := lm << bitRes
	ilo, ihi := 0, 1<<allocSteps
	for range allocSteps {
		mid := (ilo + ihi) >> 1
		psum := 0
		done := false
		for j := end - 1; j >= start; j-- {
			tmp := bits1[j] + int(int32(mid)*int32(bits2[j])>>allocSteps)
			if tmp >= thresh[j] || done {
				done = true
				psum += min(tmp, int(cap[j]))
			} else if tmp >= allocFloor {
				psum += allocFloor
			}
		}
		if psum > total {
			ihi = mid
		} else {
			ilo = mid
		}
	}
	psum := 0
	done := false
	for j := end - 1; j >= start; j-- {
		tmp := bits1[j] + int(int32(ilo)*int32(bits2[j])>>allocSteps)
		if tmp < thresh[j] && !done {
			if tmp >= allocFloor {
				tmp = allocFloor
			} else {
				tmp = 0
			}
		} else {
			done = true
		}
		tmp = min(tmp, int(cap[j]))
		bits[j] = tmp
		psum += tmp
	}
	codedBands := end
	for ; ; codedBands-- {
		j := codedBands - 1
		if j <= skipStart {
			total += skipRsv
			break
		}
		left := total - psum
		percoeff := celtUdiv(left, EBands[codedBands]-EBands[start])
		left -= (EBands[codedBands] - EBands[start]) * percoeff
		rem := max(left-(EBands[j]-EBands[start]), 0)
		bandWidth := EBands[codedBands] - EBands[j]
		bandBits := bits[j] + percoeff*bandWidth + rem
		if bandBits >= max(thresh[j], allocFloor+(1<<bitRes)) {
			if encode {
				depthThreshold := 0
				if codedBands > 17 {
					depthThreshold = 9
					if j < prev {
						depthThreshold = 7
					}
				}
				if codedBands <= start+2 || (bandBits > (depthThreshold*bandWidth<<lm<<bitRes)>>4 && j <= signalBandwidth) {
					ec.enc.EncodeBit(1, 1)
					break
				}
				ec.enc.EncodeBit(0, 1)
			} else if ec.dec.DecodeBit(1) != 0 {
				break
			}
			psum += 1 << bitRes
			bandBits -= 1 << bitRes
		}
		psum -= bits[j] + intensityRsv
		if intensityRsv > 0 {
			intensityRsv = int(log2FracTable[j-start])
		}
		psum += intensityRsv
		if bandBits >= allocFloor {
			psum += allocFloor
			bits[j] = allocFloor
		} else {
			bits[j] = 0
		}
	}
	if intensityRsv > 0 {
		if encode {
			intensity = max(min(intensity, codedBands), start)
			ec.enc.EncodeUniform(uint32(intensity-start), uint32(codedBands+1-start))
		} else {
			intensity = start + int(ec.dec.DecodeUniform(uint32(codedBands+1-start)))
		}
	} else {
		intensity = 0
	}
	if intensity <= start {
		total += dualStereoRsv
		dualStereoRsv = 0
	}
	if dualStereoRsv > 0 {
		if encode {
			ec.enc.EncodeBit(dualStereo, 1)
		} else {
			dualStereo = ec.dec.DecodeBit(1)
		}
	} else {
		dualStereo = 0
	}
	left := total - psum
	percoeff := celtUdiv(left, EBands[codedBands]-EBands[start])
	left -= (EBands[codedBands] - EBands[start]) * percoeff
	for j := start; j < codedBands; j++ {
		bits[j] += percoeff * (EBands[j+1] - EBands[j])
	}
	for j := start; j < codedBands; j++ {
		tmp := min(left, EBands[j+1]-EBands[j])
		bits[j] += tmp
		left -= tmp
	}
	balance := 0
	j := start
	for ; j < codedBands; j++ {
		n0 := EBands[j+1] - EBands[j]
		n := n0 << lm
		bit := bits[j] + balance
		var excess int
		if n > 1 {
			excess = max(bit-int(cap[j]), 0)
			bits[j] = bit - excess
			den := C * n
			if C == 2 && n > 2 && dualStereo == 0 && j < intensity {
				den++
			}
			nClogN := den * (int(LogN[j]) + logM)
			offset := (nClogN >> 1) - den*fineOffset
			if n == 2 {
				offset += den << bitRes >> 2
			}
			if bits[j]+offset < den*2<<bitRes {
				offset += nClogN >> 2
			} else if bits[j]+offset < den*3<<bitRes {
				offset += nClogN >> 3
			}
			ebits[j] = max(0, bits[j]+offset+(den<<(bitRes-1)))
			ebits[j] = celtUdiv(ebits[j], den) >> bitRes
			if C*ebits[j] > bits[j]>>bitRes {
				ebits[j] = bits[j] >> stereo >> bitRes
			}
			ebits[j] = min(ebits[j], maxFineBits)
			finePriority[j] = boolToInt(ebits[j]*(den<<bitRes) >= bits[j]+offset)
			bits[j] -= C * ebits[j] << bitRes
		} else {
			excess = max(0, bit-(C<<bitRes))
			bits[j] = bit - excess
			ebits[j] = 0
			finePriority[j] = 1
		}
		if excess > 0 {
			extraFine := min(excess>>(stereo+bitRes), maxFineBits-ebits[j])
			ebits[j] += extraFine
			extraBits := extraFine * C << bitRes
			finePriority[j] = boolToInt(extraBits >= excess-balance)
			excess -= extraBits
		}
		balance = excess
	}
	for ; j < end; j++ {
		ebits[j] = bits[j] >> stereo >> bitRes
		bits[j] = 0
		finePriority[j] = boolToInt(ebits[j] < 1)
	}
	res := allocRefResult{codedBands: codedBands, balance: balance, intensity: intensity, dualStereo: dualStereo}
	for j := range end {
		res.pulses = append(res.pulses, int32(bits[j]))
		res.ebits = append(res.ebits, int32(ebits[j]))
		res.finePriority = append(res.finePriority, int32(finePriority[j]))
	}
	return res
}

func allocResultEqual(a, b allocRefResult, start int) bool {
	return a.codedBands == b.codedBands && a.balance == b.balance && a.intensity == b.intensity &&
		a.dualStereo == b.dualStereo && slices.Equal(a.pulses[start:], b.pulses[start:]) &&
		slices.Equal(a.ebits[start:], b.ebits[start:]) && slices.Equal(a.finePriority[start:], b.finePriority[start:])
}

// TestCltComputeAllocationMatchesReference checks the encoder and decoder
// allocation against the literal clt_compute_allocation()/interp_bits2pulses()
// for random band ranges, trims, boosts and budgets, including the entropy
// coded skip, intensity and dual-stereo decisions.
func TestCltComputeAllocationMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(0xa110ca7e))
	for iter := 0; iter < 40000; iter++ {
		channels := 1 + rng.Intn(2)
		lm := rng.Intn(4)
		start := 0
		if rng.Intn(4) == 0 {
			start = 17
		}
		end := start + 1 + rng.Intn(MaxBands-start)
		if rng.Intn(2) == 0 {
			end = MaxBands - rng.Intn(4)
			if end <= start {
				end = start + 1
			}
		}
		caps := make([]int32, end)
		initCapsInto(caps, end, lm, channels)
		offsets := make([]int32, end)
		for j := start; j < end; j++ {
			if rng.Intn(6) == 0 {
				offsets[j] = int32(rng.Intn(4)) * int32(max(channels*(EBands[j+1]-EBands[j])<<lm<<bitRes, 48))
			}
		}
		allocTrim := rng.Intn(11)
		total := rng.Intn(1+[]int{80, 800, 4000, 12000}[rng.Intn(4)]) - 10
		intensity := start + rng.Intn(end-start+1)
		dualIn := rng.Intn(2)
		prev := rng.Intn(MaxBands + 1)
		signalBandwidth := rng.Intn(MaxBands)

		// Encoder.
		bufRef := make([]byte, 1275)
		bufGot := make([]byte, 1275)
		var encRef, encGot rangecoding.Encoder
		encRef.Init(bufRef)
		encGot.Init(bufGot)
		want := cltComputeAllocationRef(allocRefCoder{enc: &encRef}, start, end, offsets, caps, allocTrim, intensity, dualIn,
			total, channels, lm, prev, signalBandwidth)
		got := allocRefResult{intensity: intensity, dualStereo: dualIn,
			pulses: make([]int32, end), ebits: make([]int32, end), finePriority: make([]int32, end)}
		got.codedBands = cltComputeAllocationEncode(&encGot, start, end, offsets, caps, allocTrim, &got.intensity, &got.dualStereo,
			total, &got.balance, got.pulses, got.ebits, got.finePriority, channels, lm, prev, signalBandwidth)
		if !allocResultEqual(got, want, start) || encGot.TellFrac() != encRef.TellFrac() ||
			!bytes.Equal(encGot.Done(), encRef.Done()) || encGot.Range() != encRef.Range() {
			t.Fatalf("encode iter %d (C=%d LM=%d start=%d end=%d trim=%d total=%d):\n got %+v\nwant %+v",
				iter, channels, lm, start, end, allocTrim, total, got, want)
		}

		// Decoder, over random packet bytes.
		payload := make([]byte, 64)
		rng.Read(payload)
		var decRef, decGot rangecoding.Decoder
		decRef.Init(payload)
		decGot.Init(payload)
		want = cltComputeAllocationRef(allocRefCoder{dec: &decRef}, start, end, offsets, caps, allocTrim, 0, 0,
			total, channels, lm, 0, 0)
		got = allocRefResult{pulses: make([]int32, end), ebits: make([]int32, end), finePriority: make([]int32, end)}
		got.codedBands = cltComputeAllocation(start, end, offsets, caps, allocTrim, &got.intensity, &got.dualStereo,
			total, &got.balance, got.pulses, got.ebits, got.finePriority, channels, lm, &decGot)
		if !allocResultEqual(got, want, start) || decGot.TellFrac() != decRef.TellFrac() || decGot.Range() != decRef.Range() {
			t.Fatalf("decode iter %d (C=%d LM=%d start=%d end=%d trim=%d total=%d):\n got %+v\nwant %+v",
				iter, channels, lm, start, end, allocTrim, total, got, want)
		}
	}
}

// TestCltComputeAllocationAllocs checks the allocation stays allocation-free.
func TestCltComputeAllocationAllocs(t *testing.T) {
	const end, lm, channels = MaxBands, 2, 2
	caps := make([]int32, end)
	initCapsInto(caps, end, lm, channels)
	offsets := make([]int32, end)
	pulses := make([]int32, end)
	ebits := make([]int32, end)
	prio := make([]int32, end)
	payload := bytes.Repeat([]byte{0x5a, 0xc3}, 32)
	buf := make([]byte, 256)
	var rd rangecoding.Decoder
	var re rangecoding.Encoder
	var intensity, dual, balance int
	allocs := testing.AllocsPerRun(200, func() {
		rd.Init(payload)
		cltComputeAllocation(0, end, offsets, caps, 5, &intensity, &dual, 1200, &balance, pulses, ebits, prio, channels, lm, &rd)
		re.Init(buf)
		intensity, dual = 12, 0
		cltComputeAllocationEncode(&re, 0, end, offsets, caps, 5, &intensity, &dual, 1200, &balance, pulses, ebits, prio, channels, lm, 18, 20)
	})
	if allocs != 0 {
		t.Fatalf("allocations = %g, want 0", allocs)
	}
}
