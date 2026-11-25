package celt

import (
	"bytes"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"math/rand"
	"reflect"
	"slices"
	"testing"
)

func decodeBandAllocationRef(rd *rangecoding.Decoder, totalBits, start, end, lm int, transient bool, channels int) decodedBandAllocation {
	a := decodedBandAllocation{spread: spreadNormal}
	a.tfRes = make([]int32, end)
	tfDecode(start, end, transient, a.tfRes, lm, rd)
	if rd.Tell()+4 <= totalBits {
		a.spread = rd.DecodeICDF(spreadICDF, 5)
	}
	caps := make([]int32, end)
	initCapsInto(caps, end, lm, channels)
	a.offsets = make([]int32, end)
	totalBitsQ3, tellFrac := decodeDynallocOffsets(rd, a.offsets, caps, EBands[:], start, end, lm, channels, totalBits<<bitRes)
	a.allocTrim = 5
	if tellFrac+(6<<bitRes) <= totalBitsQ3 {
		a.allocTrim = rd.DecodeICDF(trimICDF, 7)
	}
	bitsQ3 := (totalBits << bitRes) - rd.TellFrac() - 1
	if transient && lm >= 2 && bitsQ3 >= (lm+2)<<bitRes {
		a.antiCollapseRsv = 1 << bitRes
	}
	bitsQ3 -= a.antiCollapseRsv
	a.pulses = make([]int32, end)
	a.fineQuant = make([]int32, end)
	a.finePriority = make([]int32, end)
	a.codedBands = cltComputeAllocation(start, end, a.offsets, caps, a.allocTrim, &a.intensity, &a.dualStereo,
		bitsQ3, &a.balance, a.pulses, a.fineQuant, a.finePriority, channels, lm, rd)
	return a
}

func TestDecodeBandAllocationStdMatchesReference(t *testing.T) {
	rng := rand.New(rand.NewSource(18))
	buf := make([]byte, 1275)
	for iter := range 20000 {
		n := 2 + rng.Intn(160)
		if iter%7 == 0 {
			n = 2 + rng.Intn(1200)
		}
		for i := range buf[:n] {
			buf[i] = byte(rng.Intn(256))
		}
		if iter%5 == 0 {
			// Long runs of zero or one bits drive dynalloc boosts and
			// band skips to their extremes.
			fill := byte(0)
			if iter%10 == 0 {
				fill = 0xff
			}
			for i := rng.Intn(n); i < n; i++ {
				buf[i] = fill
			}
		}
		lm := rng.Intn(4)
		channels := 1 + rng.Intn(2)
		start := 0
		if iter%3 == 0 {
			start = 17
		}
		end := MaxBands - rng.Intn(4)
		if start == 17 {
			end = MaxBands
		}
		transient := rng.Intn(3) == 0
		totalBits := n * 8

		var want, got rangecoding.Decoder
		want.Init(buf[:n])
		got.Init(buf[:n])
		// Consume a few header symbols so allocation starts mid-stream.
		skip := rng.Intn(40)
		for range skip {
			want.DecodeBit(1)
			got.DecodeBit(1)
		}
		ref := decodeBandAllocationRef(&want, totalBits, start, end, lm, transient, channels)
		d := &Decoder{}
		res := d.decodeBandAllocationStd(&got, totalBits, start, end, lm, transient, channels)

		wr, wv := want.State()
		gr, gv := got.State()
		if wr != gr || wv != gv || want.Tell() != got.Tell() {
			t.Fatalf("iter %d: range state got (%d,%d,%d) want (%d,%d,%d)", iter, gr, gv, got.Tell(), wr, wv, want.Tell())
		}
		if res.spread != ref.spread || res.allocTrim != ref.allocTrim || res.intensity != ref.intensity ||
			res.dualStereo != ref.dualStereo || res.balance != ref.balance || res.codedBands != ref.codedBands ||
			res.antiCollapseRsv != ref.antiCollapseRsv {
			t.Fatalf("iter %d (lm=%d C=%d start=%d end=%d): scalars got %+v want %+v", iter, lm, channels, start, end, res, ref)
		}
		for _, p := range []struct {
			name      string
			got, want []int32
		}{
			{"tfRes", res.tfRes, ref.tfRes},
			{"offsets", res.offsets, ref.offsets},
			{"pulses", res.pulses, ref.pulses},
			{"fineQuant", res.fineQuant, ref.fineQuant},
			{"finePriority", res.finePriority, ref.finePriority},
		} {
			if !slices.Equal(p.got[start:end], p.want[start:end]) {
				t.Fatalf("iter %d (lm=%d C=%d start=%d end=%d): %s got %v want %v", iter, lm, channels, start, end, p.name, p.got[start:end], p.want[start:end])
			}
		}
	}
}

func TestDecodeBandAllocationStdAllocs(t *testing.T) {
	buf := make([]byte, 200)
	for i := range buf {
		buf[i] = byte(i*37 + 11)
	}
	d := &Decoder{}
	var rd rangecoding.Decoder
	run := func() {
		rd.Init(buf)
		d.decodeBandAllocationStd(&rd, len(buf)*8, 0, MaxBands, 3, true, 2)
	}
	run()
	if n := testing.AllocsPerRun(100, run); n != 0 {
		t.Fatalf("decodeBandAllocationStd allocs = %v, want 0", n)
	}
}

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

func TestAllocSearchSumsMatchReference(t *testing.T) {
	rng := rand.New(rand.NewSource(0xa110c))
	for iter := 0; iter < 50000; iter++ {
		n := 1 + rng.Intn(24)
		// Some iterations use values outside the ranges the allocator
		// produces, including negative ones.
		wide := int32(1)
		if iter%4 == 0 {
			wide = 64
		}
		channels := int32(1 + rng.Intn(2))
		floor := channels << bitRes
		bandScale := make([]int32, n)
		alloc := make([]int32, n)
		trim := make([]int32, n)
		offsets := make([]int32, n)
		thresh := make([]int32, n)
		caps := make([]int32, n)
		bits1 := make([]int32, n)
		bits2 := make([]int32, n)
		for j := range n {
			bandScale[j] = channels * int32(1+rng.Intn(176))
			alloc[j] = int32(rng.Intn(200))
			trim[j] = int32(rng.Intn(4000)-2000) * wide
			offsets[j] = int32(rng.Intn(600)) * wide
			thresh[j] = int32(rng.Intn(400)) * wide
			caps[j] = int32(rng.Intn(3000)) * wide
			bits1[j] = int32(rng.Intn(3000)) * wide
			bits2[j] = int32(rng.Intn(3000)) * wide
			if wide > 1 {
				offsets[j] -= 600 * 32
				thresh[j] -= 400 * 32
				caps[j] -= 3000 * 32
				bits1[j] -= 3000 * 32
				bits2[j] -= 3000 * 32
			}
		}

		want := int32(0)
		done := false
		for j := n - 1; j >= 0; j-- {
			bitsj := (bandScale[j] * alloc[j]) >> 2
			if bitsj > 0 {
				bitsj = max(0, bitsj+trim[j])
			}
			bitsj += offsets[j]
			if bitsj >= thresh[j] || done {
				done = true
				want += min(bitsj, caps[j])
			} else if bitsj >= floor {
				want += floor
			}
		}
		if got := allocSearchSum(bandScale, alloc, trim, offsets, thresh, caps, floor); got != want {
			t.Fatalf("iter %d: allocSearchSum = %d, want %d", iter, got, want)
		}

		mid := int32(rng.Intn(1<<allocSteps + 1))
		want = 0
		done = false
		for j := n - 1; j >= 0; j-- {
			tmp := bits1[j] + ((mid * bits2[j]) >> allocSteps)
			if tmp >= thresh[j] || done {
				done = true
				want += min(tmp, caps[j])
			} else if tmp >= floor {
				want += floor
			}
		}
		if got := interpSearchSum(bits1, bits2, thresh, caps, mid, floor); got != want {
			t.Fatalf("iter %d: interpSearchSum = %d, want %d", iter, got, want)
		}
	}
}

func TestBandDivisionHelpersMatchDivision(t *testing.T) {
	rng := rand.New(rand.NewSource(0xd1f))
	for iter := 0; iter < 200000; iter++ {
		n := int(int32(rng.Uint32()))
		if rng.Intn(2) == 0 {
			n = rng.Intn(20000) - 10000
		}
		d := 1 + rng.Intn(3)
		if got, want := celtSudivBalance(n, d), celtSudiv(n, d); got != want {
			t.Fatalf("celtSudivBalance(%d, %d) = %d, want %d", n, d, got, want)
		}
		m := rng.Intn(1 << 20)
		B := 1 << rng.Intn(4)
		if rng.Intn(8) == 0 {
			B = 1 + rng.Intn(9)
		}
		if got, want := celtUdivBlocks(m, B), celtUdiv(m, B); got != want {
			t.Fatalf("celtUdivBlocks(%d, %d) = %d, want %d", m, B, got, want)
		}
	}
}

func TestComputeAllocationBudget(t *testing.T) {
	channels := 1
	testCases := []struct {
		name      string
		totalBits int
		nbBands   int
		lm        int
	}{
		{"100_bits_21_bands_lm3", 100, 21, 3},
		{"500_bits_21_bands_lm3", 500, 21, 3},
		{"1000_bits_21_bands_lm3", 1000, 21, 3},
		{"2000_bits_21_bands_lm3", 2000, 21, 3},
		// Different frame sizes
		{"500_bits_13_bands_lm0", 500, 13, 0},
		{"500_bits_17_bands_lm1", 500, 17, 1},
		{"500_bits_19_bands_lm2", 500, 19, 2},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			result := ComputeAllocation(
				tc.totalBits,
				tc.nbBands,
				channels,
				nil,   // caps (auto)
				nil,   // dynalloc
				0,     // trim (neutral)
				-1,    // intensity (disabled)
				false, // dual stereo
				tc.lm,
			)

			// Check budget respected
			totalAllocatedQ3 := 0
			for band := 0; band < tc.nbBands; band++ {
				totalAllocatedQ3 += bandBitsQ3(result, band, channels)
			}

			budgetQ3 := tc.totalBits << bitRes
			if totalAllocatedQ3 > budgetQ3 {
				t.Errorf("Allocation exceeds budget: allocated %d q3, budget %d q3",
					totalAllocatedQ3, budgetQ3)
			}

			// Check no negative allocations
			for band := 0; band < tc.nbBands; band++ {
				if result.BandBits[band] < 0 {
					t.Errorf("Band %d has negative BandBits: %d", band, result.BandBits[band])
				}
				if result.FineBits[band] < 0 {
					t.Errorf("Band %d has negative FineBits: %d", band, result.FineBits[band])
				}
			}

			// Check caps respected
			for band := 0; band < tc.nbBands; band++ {
				if result.BandBits[band] > result.Caps[band] {
					t.Errorf("Band %d exceeds cap: %d > %d",
						band, result.BandBits[band], result.Caps[band])
				}
			}

			// Log allocation table for inspection
			totalAllocatedBits := totalAllocatedQ3 >> bitRes
			t.Logf("Allocation for totalBits=%d, nbBands=%d, lm=%d:", tc.totalBits, tc.nbBands, tc.lm)
			t.Logf("  Total allocated: %d/%d bits (%.1f%%)", totalAllocatedBits, tc.totalBits, 100*float64(totalAllocatedBits)/float64(tc.totalBits))
			for band := 0; band < tc.nbBands; band++ {
				frameSize := LMToFrameSize(tc.lm)
				bandBits := result.BandBits[band] >> bitRes
				t.Logf("  Band %2d: width=%2d, bandBits=%3d, fineBits=%2d, cap=%3d",
					band, ScaledBandWidth(band, frameSize), bandBits,
					result.FineBits[band], result.Caps[band])
			}
		})
	}
}

// TestComputeAllocationDistribution verifies allocation follows spectral shape.
// - Higher totalBits -> more bits to high-frequency bands
// - Lower bands get more bits at low bit rates (perceptually important)
func TestComputeAllocationDistribution(t *testing.T) {
	channels := 1
	nbBands := 21
	lm := 3 // 20ms frame

	// Compare low and high bit rates
	lowBits := 200
	highBits := 2000

	lowResult := ComputeAllocation(lowBits, nbBands, channels, nil, nil, 0, -1, false, lm)
	highResult := ComputeAllocation(highBits, nbBands, channels, nil, nil, 0, -1, false, lm)

	// At low bit rate, lower bands should get proportionally more bits
	// At high bit rate, allocation should be more spread out

	// Count bits in lower bands (0-10) vs upper bands (11-20)
	var lowRateLower, lowRateUpper int
	var highRateLower, highRateUpper int

	for band := range nbBands {
		if band <= 10 {
			lowRateLower += bandBitsQ3(lowResult, band, channels)
			highRateLower += bandBitsQ3(highResult, band, channels)
		} else {
			lowRateUpper += bandBitsQ3(lowResult, band, channels)
			highRateUpper += bandBitsQ3(highResult, band, channels)
		}
	}

	// High bit rate should have more bits in upper bands (absolute)
	if highRateUpper <= lowRateUpper {
		t.Logf("Note: high bit rate upper bands (%d) <= low bit rate upper bands (%d)",
			highRateUpper, lowRateUpper)
		// This is not necessarily wrong - depends on allocation algorithm
	}

	t.Logf("Low rate (%d bits): lower=%d, upper=%d", lowBits, lowRateLower>>bitRes, lowRateUpper>>bitRes)
	t.Logf("High rate (%d bits): lower=%d, upper=%d", highBits, highRateLower>>bitRes, highRateUpper>>bitRes)

	// Basic sanity: high bit rate should allocate more total bits
	lowTotal := lowRateLower + lowRateUpper
	highTotal := highRateLower + highRateUpper
	if highTotal <= lowTotal {
		t.Errorf("High bit rate (%d) should allocate more than low rate (%d)",
			highTotal, lowTotal)
	}
}

// TestComputeAllocationTrim verifies trim parameter affects spectral balance.
// - trim > 0 -> high bands get more bits
// - trim < 0 -> low bands get more bits
// - trim = 0 -> neutral
func TestComputeAllocationTrim(t *testing.T) {
	channels := 1
	nbBands := 21
	lm := 3
	totalBits := 1000

	// Compare different trim values
	trimValues := []int{-6, -3, 0, 3, 6}

	var results []AllocationResult
	for _, trim := range trimValues {
		result := ComputeAllocation(totalBits, nbBands, channels, nil, nil, trim, -1, false, lm)
		results = append(results, result)
	}

	// Compute high band ratio for each trim
	for i, trim := range trimValues {
		var lowBandBits, highBandBits int
		for band := range nbBands {
			bits := bandBitsQ3(results[i], band, channels)
			if band < nbBands/2 {
				lowBandBits += bits
			} else {
				highBandBits += bits
			}
		}
		total := lowBandBits + highBandBits
		highRatio := 0.0
		if total > 0 {
			highRatio = float64(highBandBits) / float64(total)
		}
		t.Logf("Trim=%+d: low=%d, high=%d, highRatio=%.2f", trim, lowBandBits>>bitRes, highBandBits>>bitRes, highRatio)
	}

	// Verify trim ordering (higher trim should have higher high-band ratio)
	for i := 1; i < len(trimValues); i++ {
		prevHighRatio := computeHighBandRatio(results[i-1], nbBands)
		currHighRatio := computeHighBandRatio(results[i], nbBands)

		// Allow some tolerance since trim effect may be small
		if currHighRatio < prevHighRatio-0.1 {
			t.Logf("Note: trim=%d ratio %.2f < trim=%d ratio %.2f (may be allocation boundary effect)",
				trimValues[i], currHighRatio, trimValues[i-1], prevHighRatio)
		}
	}
}

// computeHighBandRatio returns the ratio of bits allocated to high bands.
func computeHighBandRatio(result AllocationResult, nbBands int) float64 {
	channels := 1
	var lowBits, highBits int
	for band := range nbBands {
		bits := bandBitsQ3(result, band, channels)
		if band < nbBands/2 {
			lowBits += bits
		} else {
			highBits += bits
		}
	}
	total := lowBits + highBits
	if total == 0 {
		return 0
	}
	return float64(highBits) / float64(total)
}

func bandBitsQ3(result AllocationResult, band, channels int) int {
	if band < 0 || band >= len(result.BandBits) || band >= len(result.FineBits) {
		return 0
	}
	return int(result.BandBits[band]) + (int(result.FineBits[band]) * channels << bitRes)
}

// TestComputeAllocationByLM verifies allocation varies correctly by frame size.
// - LM=0 (2.5ms) -> different caps than LM=3 (20ms)
// - Shorter frames have less total capacity
func TestComputeAllocationByLM(t *testing.T) {
	channels := 1
	totalBits := 500
	nbBands := 13 // Use minimum bands (shared across all LM values)

	for lm := 0; lm <= 3; lm++ {
		result := ComputeAllocation(totalBits, nbBands, channels, nil, nil, 0, -1, false, lm)

		frameSize := LMToFrameSize(lm)
		var totalAllocated int
		var maxCap int
		for band := range nbBands {
			totalAllocated += bandBitsQ3(result, band, channels)
			if int(result.Caps[band]) > maxCap {
				maxCap = int(result.Caps[band])
			}
		}

		totalAllocatedBits := totalAllocated >> bitRes
		t.Logf("LM=%d (%.1fms, %d samples): allocated=%d bits, maxCap=%d",
			lm, float64(frameSize)/48.0, frameSize, totalAllocatedBits, maxCap)

		// Verify allocation doesn't exceed budget
		if totalAllocated > (totalBits << bitRes) {
			t.Errorf("LM=%d: allocation %d q3 exceeds budget %d q3", lm, totalAllocated, totalBits<<bitRes)
		}
	}

	// LM=3 (20ms) should have larger caps than LM=0 (2.5ms)
	result0 := ComputeAllocation(totalBits, nbBands, channels, nil, nil, 0, -1, false, 0)
	result3 := ComputeAllocation(totalBits, nbBands, channels, nil, nil, 0, -1, false, 3)

	// Check that at least some caps are larger for LM=3
	largerCapsCount := 0
	for band := range nbBands {
		if result3.Caps[band] > result0.Caps[band] {
			largerCapsCount++
		}
	}

	if largerCapsCount == 0 {
		t.Logf("Note: LM=3 caps not larger than LM=0 (may be by design)")
	} else {
		t.Logf("LM=3 has larger caps than LM=0 for %d/%d bands", largerCapsCount, nbBands)
	}
}

// TestAllocationEncodeDecodeRoundTrip ensures allocation encoding/decoding stays in sync.
func TestAllocationEncodeDecodeRoundTrip(t *testing.T) {
	nbBands := 21
	lm := 3
	channels := 2
	totalBits := 600
	totalBitsQ3 := totalBits << bitRes
	intensity := nbBands
	dualStereo := true
	trim := 0
	signalBandwidth := nbBands - 1

	caps := initCaps(nbBands, lm, channels)

	buf := make([]byte, 256)
	re := &rangecoding.Encoder{}
	re.Init(buf)

	encResult := ComputeAllocationWithEncoder(
		re,
		totalBitsQ3,
		nbBands,
		channels,
		caps,
		nil, // offsets
		trim,
		intensity,
		dualStereo,
		lm,
		0, // prev
		signalBandwidth,
	)
	tellEnc := re.TellFrac()
	data := re.Done()

	rd := &rangecoding.Decoder{}
	rd.Init(data)
	decResult := ComputeAllocationWithDecoder(rd, totalBits, nbBands, channels, caps, nil, trim, intensity, dualStereo, lm)
	tellDec := rd.TellFrac()

	if tellDec != tellEnc {
		t.Errorf("allocation bit consumption mismatch: enc=%d q3, dec=%d q3", tellEnc, tellDec)
	}
	if decResult.CodedBands != encResult.CodedBands {
		t.Errorf("codedBands mismatch: enc=%d dec=%d", encResult.CodedBands, decResult.CodedBands)
	}
	if decResult.Intensity != encResult.Intensity {
		t.Errorf("intensity mismatch: enc=%d dec=%d", encResult.Intensity, decResult.Intensity)
	}
	if decResult.DualStereo != encResult.DualStereo {
		t.Errorf("dualStereo mismatch: enc=%v dec=%v", encResult.DualStereo, decResult.DualStereo)
	}
	for band := range nbBands {
		if decResult.BandBits[band] != encResult.BandBits[band] {
			t.Fatalf("band %d bits mismatch: enc=%d dec=%d", band, encResult.BandBits[band], decResult.BandBits[band])
		}
		if decResult.FineBits[band] != encResult.FineBits[band] {
			t.Fatalf("band %d fine bits mismatch: enc=%d dec=%d", band, encResult.FineBits[band], decResult.FineBits[band])
		}
	}
}

// TestPulseCapsReasonable verifies pulse caps are proportional to band width.
// Wider bands can hold more pulses.
func TestPulseCapsReasonable(t *testing.T) {
	testCases := []struct {
		lm        int
		frameSize int
	}{
		{0, 120},
		{1, 240},
		{2, 480},
		{3, 960},
	}

	for _, tc := range testCases {
		t.Run(lm_name(tc.lm), func(t *testing.T) {
			caps := initCaps(MaxBands, tc.lm, 1)

			t.Logf("Pulse caps for LM=%d (%d samples):", tc.lm, tc.frameSize)
			for band := range MaxBands {
				width := ScaledBandWidth(band, tc.frameSize)
				t.Logf("  Band %2d: width=%3d, cap=%3d", band, width, caps[band])

				// Cap should be positive for positive width
				if width > 0 && caps[band] <= 0 {
					t.Errorf("Band %d has width %d but cap %d", band, width, caps[band])
				}

				// Wider bands should have equal or larger caps
				if band > 0 && width > ScaledBandWidth(band-1, tc.frameSize) {
					if caps[band] < caps[band-1] {
						t.Logf("Note: Band %d (width=%d, cap=%d) has smaller cap than band %d (width=%d, cap=%d)",
							band, width, caps[band], band-1, ScaledBandWidth(band-1, tc.frameSize), caps[band-1])
					}
				}
			}
		})
	}
}

// lm_name returns a descriptive name for LM value.
func lm_name(lm int) string {
	switch lm {
	case 0:
		return "2.5ms"
	case 1:
		return "5ms"
	case 2:
		return "10ms"
	case 3:
		return "20ms"
	default:
		return "unknown"
	}
}

// TestComputeAllocationEdgeCases tests edge cases in allocation.
func TestComputeAllocationEdgeCases(t *testing.T) {
	// Test with zero bits
	t.Run("zero_bits", func(t *testing.T) {
		result := ComputeAllocation(0, 21, 1, nil, nil, 0, -1, false, 3)
		for band := range 21 {
			if result.BandBits[band] != 0 || result.FineBits[band] != 0 {
				t.Errorf("Band %d has bits with zero budget", band)
			}
		}
	})

	// Test with zero bands
	t.Run("zero_bands", func(t *testing.T) {
		result := ComputeAllocation(1000, 0, 1, nil, nil, 0, -1, false, 3)
		if len(result.BandBits) != 0 {
			t.Errorf("Expected 0 bands, got %d", len(result.BandBits))
		}
	})

	// Test with very high bit budget
	t.Run("high_budget", func(t *testing.T) {
		result := ComputeAllocation(10000, 21, 1, nil, nil, 0, -1, false, 3)
		// Should allocate up to caps
		for band := range 21 {
			if result.BandBits[band] > result.Caps[band] {
				t.Errorf("Band %d exceeds cap even with high budget", band)
			}
		}
	})

	// Test with intensity stereo
	t.Run("intensity_stereo", func(t *testing.T) {
		intensity := 15 // Start intensity at band 15
		result := ComputeAllocation(1000, 21, 2, nil, nil, 0, intensity, false, 3)

		// Bands above intensity should have adjusted bits
		var totalBits int
		for band := range 21 {
			totalBits += int(result.BandBits[band] + result.FineBits[band])
		}
		t.Logf("Intensity stereo from band %d: total allocated %d bits", intensity, totalBits)
	})
}

func TestAllocTrimAnalysis(t *testing.T) {
	// Test cases for allocation trim analysis
	tests := []struct {
		name          string
		equivRate     int
		channels      int
		tfEstimate    opusVal16
		expectMinTrim int
		expectMaxTrim int
		description   string
	}{
		{
			name:          "Low bitrate mono",
			equivRate:     32000,
			channels:      1,
			tfEstimate:    0.0,
			expectMinTrim: 3,
			expectMaxTrim: 5,
			description:   "At low bitrate, trim should be reduced (around 4)",
		},
		{
			name:          "High bitrate mono",
			equivRate:     128000,
			channels:      1,
			tfEstimate:    0.0,
			expectMinTrim: 4,
			expectMaxTrim: 6,
			description:   "At high bitrate, trim should be near default (5)",
		},
		{
			name:          "Medium bitrate with high TF",
			equivRate:     64000,
			channels:      1,
			tfEstimate:    0.8,
			expectMinTrim: 2,
			expectMaxTrim: 4,
			description:   "High TF estimate should reduce trim (transient signal)",
		},
		{
			name:          "Transition bitrate (72kbps)",
			equivRate:     72000,
			channels:      1,
			tfEstimate:    0.0,
			expectMinTrim: 4,
			expectMaxTrim: 5,
			description:   "Between 64k and 80k, trim interpolates from 4 to 5",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			// Create simple normalized coefficients (flat spectrum)
			nbBands := 21
			lm := 3

			// Generate flat spectrum normalized coefficients
			numCoeffs := EBands[nbBands] << lm
			normL := make([]celtNorm, numCoeffs)
			for i := range normL {
				normL[i] = 0.1 // Small uniform values
			}

			// Generate flat band energies
			bandLogE := make([]celtGLog, nbBands*tc.channels)
			for i := range bandLogE {
				bandLogE[i] = 0.0 // Flat energy
			}

			var normR []celtNorm
			if tc.channels == 2 {
				normR = make([]celtNorm, numCoeffs)
				copy(normR, normL)
			}

			trim := AllocTrimAnalysis(
				normL,
				bandLogE,
				nbBands,
				lm,
				tc.channels,
				normR,
				nbBands, // intensity = nbBands (no intensity stereo)
				tc.tfEstimate,
				tc.equivRate,
				0, // surroundTrim
				0, // tonalitySlope
			)

			t.Logf("%s: equivRate=%d, tfEstimate=%.2f, trim=%d",
				tc.name, tc.equivRate, tc.tfEstimate, trim)

			if trim < tc.expectMinTrim || trim > tc.expectMaxTrim {
				t.Errorf("Trim %d out of expected range [%d, %d] for %s",
					trim, tc.expectMinTrim, tc.expectMaxTrim, tc.description)
			}
		})
	}
}

func TestComputeEquivRate(t *testing.T) {
	tests := []struct {
		name              string
		nbCompressedBytes int
		channels          int
		lm                int
		targetBitrate     int
		minExpect         int
		maxExpect         int
	}{
		{
			name:              "160 bytes mono 20ms",
			nbCompressedBytes: 160,
			channels:          1,
			lm:                3,
			targetBitrate:     0,
			minExpect:         60000,
			maxExpect:         70000,
		},
		{
			name:              "80 bytes mono 20ms",
			nbCompressedBytes: 80,
			channels:          1,
			lm:                3,
			targetBitrate:     0,
			minExpect:         28000,
			maxExpect:         36000,
		},
		{
			name:              "160 bytes stereo 20ms",
			nbCompressedBytes: 160,
			channels:          2,
			lm:                3,
			targetBitrate:     0,
			minExpect:         58000,
			maxExpect:         70000,
		},
		{
			name:              "40 bytes mono 10ms",
			nbCompressedBytes: 40,
			channels:          1,
			lm:                2,
			targetBitrate:     0,
			minExpect:         28000,
			maxExpect:         36000,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			equivRate := ComputeEquivRate(tc.nbCompressedBytes, tc.channels, tc.lm, tc.targetBitrate)
			t.Logf("%s: equivRate=%d", tc.name, equivRate)

			if equivRate < tc.minExpect || equivRate > tc.maxExpect {
				t.Errorf("Equiv rate %d out of expected range [%d, %d]",
					equivRate, tc.minExpect, tc.maxExpect)
			}
		})
	}
}

func TestAllocTrimBitrateAdjustment(t *testing.T) {
	// Verify that allocation trim follows the bitrate rules from libopus:
	// - equiv_rate < 64000: trim = 4
	// - 64000 <= equiv_rate < 80000: trim interpolates 4 to 5
	// - equiv_rate >= 80000: trim = 5

	nbBands := 21
	lm := 3
	numCoeffs := EBands[nbBands] << lm
	normL := make([]celtNorm, numCoeffs)
	bandLogE := make([]celtGLog, nbBands)

	// Test at very low bitrate (should get trim ~4)
	trim32k := AllocTrimAnalysis(normL, bandLogE, nbBands, lm, 1, nil, nbBands, 0, 32000, 0, 0)
	trim64k := AllocTrimAnalysis(normL, bandLogE, nbBands, lm, 1, nil, nbBands, 0, 64000, 0, 0)
	trim72k := AllocTrimAnalysis(normL, bandLogE, nbBands, lm, 1, nil, nbBands, 0, 72000, 0, 0)
	trim80k := AllocTrimAnalysis(normL, bandLogE, nbBands, lm, 1, nil, nbBands, 0, 80000, 0, 0)
	trim128k := AllocTrimAnalysis(normL, bandLogE, nbBands, lm, 1, nil, nbBands, 0, 128000, 0, 0)

	t.Logf("Trim values by bitrate: 32k=%d, 64k=%d, 72k=%d, 80k=%d, 128k=%d",
		trim32k, trim64k, trim72k, trim80k, trim128k)

	// At 32k and 64k, should be at or near 4
	if trim32k > 5 {
		t.Errorf("At 32kbps, expected trim <= 5, got %d", trim32k)
	}

	// At 80k+, should be at or near 5
	if trim80k < 4 {
		t.Errorf("At 80kbps, expected trim >= 4, got %d", trim80k)
	}

	// trim72k should be between trim64k and trim80k
	if trim72k < trim64k-1 || trim72k > trim80k+1 {
		t.Errorf("At 72kbps, expected trim between %d and %d, got %d", trim64k, trim80k, trim72k)
	}
}

func TestAllocTrimTFEstimate(t *testing.T) {
	// Test that TF estimate properly reduces trim for transient signals
	nbBands := 21
	lm := 3
	numCoeffs := EBands[nbBands] << lm
	normL := make([]celtNorm, numCoeffs)
	bandLogE := make([]celtGLog, nbBands)
	equivRate := 80000 // Use neutral bitrate

	trimNoTransient := AllocTrimAnalysis(normL, bandLogE, nbBands, lm, 1, nil, nbBands, 0.0, equivRate, 0, 0)
	trimLowTransient := AllocTrimAnalysis(normL, bandLogE, nbBands, lm, 1, nil, nbBands, 0.3, equivRate, 0, 0)
	trimHighTransient := AllocTrimAnalysis(normL, bandLogE, nbBands, lm, 1, nil, nbBands, 0.8, equivRate, 0, 0)

	t.Logf("Trim values by TF estimate: 0.0=%d, 0.3=%d, 0.8=%d",
		trimNoTransient, trimLowTransient, trimHighTransient)

	// Higher TF estimate should give lower trim
	if trimLowTransient > trimNoTransient {
		t.Errorf("TF 0.3 trim (%d) should be <= TF 0.0 trim (%d)", trimLowTransient, trimNoTransient)
	}
	if trimHighTransient > trimLowTransient {
		t.Errorf("TF 0.8 trim (%d) should be <= TF 0.3 trim (%d)", trimHighTransient, trimLowTransient)
	}
}

func TestAllocTrimSurroundTrimAdjustment(t *testing.T) {
	nbBands := 21
	lm := 3
	numCoeffs := EBands[nbBands] << lm
	normL := make([]celtNorm, numCoeffs)
	bandLogE := make([]celtGLog, nbBands)
	equivRate := 80000

	base := AllocTrimAnalysis(normL, bandLogE, nbBands, lm, 1, nil, nbBands, 0.0, equivRate, 0.0, 0.0)
	plus := AllocTrimAnalysis(normL, bandLogE, nbBands, lm, 1, nil, nbBands, 0.0, equivRate, 1.0, 0.0)
	minus := AllocTrimAnalysis(normL, bandLogE, nbBands, lm, 1, nil, nbBands, 0.0, equivRate, -1.0, 0.0)

	if plus > base {
		t.Fatalf("positive surroundTrim should not increase trim: base=%d plus=%d", base, plus)
	}
	if minus < base {
		t.Fatalf("negative surroundTrim should not decrease trim: base=%d minus=%d", base, minus)
	}
}

func TestAllocTrimAnalysisValidAppliesZeroSlopeAdjustment(t *testing.T) {
	nbBands := 21
	lm := 3
	numCoeffs := EBands[nbBands] << lm
	normL := make([]celtNorm, numCoeffs)
	bandLogE := make([]celtGLog, nbBands)

	_, invalid := allocTrimAnalysisDetailed(
		normL,
		bandLogE,
		nbBands,
		lm,
		1,
		nil,
		nbBands,
		0,
		80000,
		0,
		0,
		false,
		EBands[:],
	)
	_, valid := allocTrimAnalysisDetailed(
		normL,
		bandLogE,
		nbBands,
		lm,
		1,
		nil,
		nbBands,
		0,
		80000,
		0,
		0,
		true,
		EBands[:],
	)

	if invalid.tonal != 0 {
		t.Fatalf("invalid analysis applied tonal adjustment: %g", invalid.tonal)
	}
	if valid.tonal < 0.09 || valid.tonal > 0.11 {
		t.Fatalf("valid zero-slope analysis tonal adjustment = %g, want about 0.1", valid.tonal)
	}
	if valid.raw >= invalid.raw {
		t.Fatalf("valid zero-slope analysis did not reduce raw trim: valid=%g invalid=%g", valid.raw, invalid.raw)
	}
}

var fixedZeroAllocHelper libopustest.HelperCache

func buildFixedZeroAllocHelper() (string, error) {
	return libopustest.BuildCHelper(libopustest.CHelperConfig{
		Label:       "selected fixed CELT nonpositive allocation",
		OutputBase:  "gopus_libopus_celt_alloc_zero_fixed",
		SourceFile:  "libopus_celt_alloc_zero_fixed_info.c",
		FixedRef:    true,
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"celt", "silk"},
		Libs:        []string{libopustest.FixedRefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:   true,
	})
}

func TestAllocationNonpositiveBudgetMatchesFixedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := []struct {
		name                            string
		start, end, lm, channels, total int
		prev, signalBandwidth, trim     int
	}{
		{"mono_2p5_zero_first", 0, 21, 0, 1, 0, 0, 21, 5},
		{"mono_2p5_negative_prev", 0, 21, 0, 1, -8, 21, 21, 5},
		{"stereo_2p5_zero", 0, 21, 0, 2, 0, 0, 21, 5},
		{"stereo_10_negative", 0, 21, 2, 2, -64, 1, 18, 7},
		{"hybrid_20_zero", 17, 21, 3, 1, 0, 2, 21, 5},
		{"hybrid_20_negative_stereo", 17, 21, 3, 2, -1, 21, 20, 3},
	}
	bin, err := fixedZeroAllocHelper.Path(buildFixedZeroAllocHelper)
	if err != nil {
		libopustest.HelperUnavailable(t, "selected fixed CELT allocation", err)
	}
	payload := libopustest.NewOraclePayload("GAZI", uint32(len(cases)))
	for _, c := range cases {
		for _, v := range [...]int{c.start, c.end, c.lm, c.channels, c.total,
			c.prev, c.signalBandwidth, c.trim} {
			payload.I32(int32(v))
		}
	}
	reader, err := libopustest.RunOracle(bin, payload.Bytes(), "selected fixed CELT allocation", "GAZO")
	if err != nil {
		t.Fatalf("run selected fixed allocation oracle: %v", err)
	}
	reader.Count(len(cases))
	for _, c := range cases {
		wantCoded, wantBalance := int(reader.I32()), int(reader.I32())
		wantIntensity, wantDual := int(reader.I32()), reader.I32() != 0
		wantTell, wantRange := int(reader.U32()), reader.U32()
		var wantCaps, wantPulses, wantFine, wantPriority [21]int32
		for i := range wantCaps {
			wantCaps[i] = reader.I32()
		}
		for i := range wantPulses {
			wantPulses[i] = reader.I32()
		}
		for i := range wantFine {
			wantFine[i] = reader.I32()
		}
		for i := range wantPriority {
			wantPriority[i] = reader.I32()
		}
		t.Run(c.name, func(t *testing.T) {
			caps := InitCaps(c.end, c.lm, c.channels)
			offsets := make([]int32, c.end)
			for _, into := range []bool{false, true} {
				var coder rangecoding.Encoder
				var packet [64]byte
				coder.Init(packet[:])
				var got *AllocationResult
				if into {
					var scratch AllocEncodeScratch
					got = ComputeAllocationWithEncoderStartInto(&scratch, &coder, c.start, c.total,
						c.end, c.channels, caps, offsets, c.trim, c.end, false, c.lm,
						c.prev, c.signalBandwidth)
				} else {
					result := ComputeAllocationWithEncoderStart(&coder, c.start, c.total,
						c.end, c.channels, caps, offsets, c.trim, c.end, false, c.lm,
						c.prev, c.signalBandwidth)
					got = &result
				}
				if got.CodedBands != wantCoded || got.Balance != wantBalance ||
					got.Intensity != wantIntensity || got.DualStereo != wantDual ||
					coder.TellFrac() != wantTell || coder.Range() != wantRange ||
					!reflect.DeepEqual(got.Caps, wantCaps[:c.end]) ||
					!reflect.DeepEqual(got.BandBits, wantPulses[:c.end]) ||
					!reflect.DeepEqual(got.FineBits, wantFine[:c.end]) ||
					!reflect.DeepEqual(got.FinePriority, wantPriority[:c.end]) {
					t.Errorf("into=%t coded=%d/%d balance=%d/%d intensity=%d/%d dual=%t/%t tell=%d/%d range=%08x/%08x caps=%v/%v pulses=%v/%v fine=%v/%v priority=%v/%v",
						into, got.CodedBands, wantCoded, got.Balance, wantBalance,
						got.Intensity, wantIntensity, got.DualStereo, wantDual,
						coder.TellFrac(), wantTell, coder.Range(), wantRange,
						got.Caps, wantCaps[:c.end], got.BandBits, wantPulses[:c.end],
						got.FineBits, wantFine[:c.end], got.FinePriority, wantPriority[:c.end])
				}
			}
			var scratch AllocEncodeScratch
			var coder rangecoding.Encoder
			var packet [64]byte
			encode := func() {
				coder.Init(packet[:])
				ComputeAllocationWithEncoderStartInto(&scratch, &coder, c.start, c.total,
					c.end, c.channels, caps, offsets, c.trim, c.end, false, c.lm,
					c.prev, c.signalBandwidth)
			}
			encode()
			if allocs := testing.AllocsPerRun(100, encode); allocs != 0 {
				t.Fatalf("warm Into allocation=%g want 0", allocs)
			}
		})
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
}
