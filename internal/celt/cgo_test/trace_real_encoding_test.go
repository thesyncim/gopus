// Package cgo traces real encoding from both encoders.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestTraceRealEncodingDivergence traces the actual encoding from both encoders.
func TestTraceRealEncodingDivergence(t *testing.T) {
	frameSize := 960
	sampleRate := 48000
	bitrate := 64000

	// Generate 440Hz sine wave
	pcm32 := make([]float32, frameSize)
	pcm64 := make([]float64, frameSize)
	for i := range pcm32 {
		ti := float64(i) / float64(sampleRate)
		val := 0.5 * math.Sin(2*math.Pi*440*ti)
		pcm32[i] = float32(val)
		pcm64[i] = val
	}

	// Encode with libopus
	libEnc, err := NewLibopusEncoder(sampleRate, 1, OpusApplicationAudio)
	if err != nil {
		t.Fatalf("libopus encoder creation failed: %v", err)
	}
	defer libEnc.Destroy()
	libEnc.SetBitrate(bitrate)
	libEnc.SetComplexity(10)
	libEnc.SetBandwidth(OpusBandwidthFullband)
	libEnc.SetVBR(false)

	libPacket, _ := libEnc.EncodeFloat(pcm32, frameSize)
	libPayload := libPacket[1:]

	// Encode with gopus
	goEnc := celt.NewEncoder(1)
	goEnc.Reset()
	goEnc.SetBitrate(bitrate)
	goEnc.SetComplexity(10)
	goEnc.SetVBR(false)

	goPacket, _ := goEnc.EncodeFrame(pcm64, frameSize)

	t.Logf("gopus packet: %d bytes", len(goPacket))
	t.Logf("libopus payload: %d bytes", len(libPayload))

	// Find first divergence
	minLen := len(goPacket)
	if len(libPayload) < minLen {
		minLen = len(libPayload)
	}

	firstDiff := -1
	for i := 0; i < minLen; i++ {
		if goPacket[i] != libPayload[i] {
			firstDiff = i
			break
		}
	}

	if firstDiff < 0 {
		t.Log("Packets match completely!")
		return
	}

	t.Logf("First divergence at byte %d (bit %d)", firstDiff, firstDiff*8)

	// Now decode both packets up to the divergence point
	mode := celt.GetModeConfig(frameSize)
	nbBands := mode.EffBands
	lm := mode.LM
	targetBytes := 159
	targetBits := targetBytes * 8

	t.Log("")
	t.Log("=== Decoding LIBOPUS packet ===")
	rdLib := &rangecoding.Decoder{}
	rdLib.Init(libPayload)

	silenceLib := rdLib.DecodeBit(15)
	postfilterLib := rdLib.DecodeBit(1)
	transientLib := rdLib.DecodeBit(3)
	intraLib := rdLib.DecodeBit(3)
	t.Logf("Header: silence=%d postfilter=%d transient=%d intra=%d (tell=%d)",
		silenceLib, postfilterLib, transientLib, intraLib, rdLib.Tell())

	// Decode coarse energy
	goDecLib := celt.NewDecoder(1)
	coarseLib := goDecLib.DecodeCoarseEnergyWithDecoder(rdLib, nbBands, intraLib == 1, lm)
	t.Logf("Tell after coarse: %d bits", rdLib.Tell())

	// Decode TF using proper differential TF decoding
	tfResLib := make([]int, nbBands)
	celt.TFDecodeForTest(0, nbBands, transientLib == 1, tfResLib, lm, rdLib)
	t.Logf("Tell after TF: %d bits", rdLib.Tell())

	// Decode spread
	spreadICDF := []uint8{25, 23, 2, 0}
	spreadLib := rdLib.DecodeICDF(spreadICDF, 5)
	t.Logf("Spread=%d, Tell after spread: %d bits", spreadLib, rdLib.Tell())

	// Decode dynalloc
	bitRes := 3
	capsLib := celt.InitCaps(nbBands, lm, 1)
	offsetsLib := make([]int, nbBands)
	totalBitsQ3ForDynalloc := targetBits << bitRes
	dynallocLogp := 6
	totalBoost := 0
	tellFracDynalloc := rdLib.TellFrac()

	for i := 0; i < nbBands; i++ {
		width := celt.ScaledBandWidth(i, 120<<lm)
		if width <= 0 {
			width = 1
		}
		innerMax := 6 << bitRes
		if width > innerMax {
			innerMax = width
		}
		quanta := width << bitRes
		if quanta > innerMax {
			quanta = innerMax
		}

		dynallocLoopLogp := dynallocLogp
		boost := 0

		for j := 0; tellFracDynalloc+(dynallocLoopLogp<<bitRes) < totalBitsQ3ForDynalloc-totalBoost && boost < capsLib[i]; j++ {
			flag := rdLib.DecodeBit(uint(dynallocLoopLogp))
			tellFracDynalloc = rdLib.TellFrac()
			if flag == 0 {
				break
			}
			boost += quanta
			totalBoost += quanta
			dynallocLoopLogp = 1
		}

		if boost > 0 && dynallocLogp > 2 {
			dynallocLogp--
		}
		offsetsLib[i] = boost
	}
	t.Logf("Tell after dynalloc: %d bits", rdLib.Tell())
	t.Logf("Dynalloc offsets (lib, Q3 bits): %v", offsetsLib[:nbBands])

	// Decode trim
	trimICDF := []uint8{126, 124, 119, 109, 87, 41, 19, 9, 4, 2, 0}
	trimLib := rdLib.DecodeICDF(trimICDF, 7)
	t.Logf("Trim=%d, Tell after trim: %d bits", trimLib, rdLib.Tell())

	// Compute allocation
	bitsUsedLib := rdLib.TellFrac()
	totalBitsQ3Lib := (targetBits << bitRes) - bitsUsedLib - 1
	antiCollapseRsv := 1 << bitRes
	totalBitsQ3Lib -= antiCollapseRsv

	allocResultLib := celt.ComputeAllocationWithDecoder(
		rdLib, totalBitsQ3Lib>>bitRes,
		nbBands, 1, capsLib, offsetsLib, trimLib,
		nbBands, false, lm,
	)
	t.Logf("Tell after allocation: %d bits", rdLib.Tell())

	// Decode fine energy - uses raw bits from END of buffer
	fineQLib := make([]int, nbBands)
	for i := 0; i < nbBands; i++ {
		fineBits := allocResultLib.FineBits[i]
		if fineBits == 0 {
			continue
		}
		q := rdLib.DecodeRawBits(uint(fineBits))
		fineQLib[i] = int(q)
	}
	t.Logf("Tell after fine energy: %d bits", rdLib.Tell())

	t.Log("")
	t.Log("=== Decoding GOPUS packet ===")
	rdGo := &rangecoding.Decoder{}
	rdGo.Init(goPacket)

	silenceGo := rdGo.DecodeBit(15)
	postfilterGo := rdGo.DecodeBit(1)
	transientGo := rdGo.DecodeBit(3)
	intraGo := rdGo.DecodeBit(3)
	t.Logf("Header: silence=%d postfilter=%d transient=%d intra=%d (tell=%d)",
		silenceGo, postfilterGo, transientGo, intraGo, rdGo.Tell())

	// Decode coarse energy
	goDecGo := celt.NewDecoder(1)
	coarseGo := goDecGo.DecodeCoarseEnergyWithDecoder(rdGo, nbBands, intraGo == 1, lm)
	t.Logf("Tell after coarse: %d bits", rdGo.Tell())

	// Decode TF using proper differential TF decoding
	tfResGo := make([]int, nbBands)
	celt.TFDecodeForTest(0, nbBands, transientGo == 1, tfResGo, lm, rdGo)
	t.Logf("Tell after TF: %d bits", rdGo.Tell())

	// Decode spread
	spreadGo := rdGo.DecodeICDF(spreadICDF, 5)
	t.Logf("Spread=%d, Tell after spread: %d bits", spreadGo, rdGo.Tell())

	// Decode dynalloc
	capsGo := celt.InitCaps(nbBands, lm, 1)
	offsetsGo := make([]int, nbBands)
	dynallocLogpGo := 6
	totalBoostGo := 0
	tellFracDynallocGo := rdGo.TellFrac()

	for i := 0; i < nbBands; i++ {
		width := celt.ScaledBandWidth(i, 120<<lm)
		if width <= 0 {
			width = 1
		}
		innerMax := 6 << bitRes
		if width > innerMax {
			innerMax = width
		}
		quanta := width << bitRes
		if quanta > innerMax {
			quanta = innerMax
		}

		dynallocLoopLogp := dynallocLogpGo
		boost := 0

		for j := 0; tellFracDynallocGo+(dynallocLoopLogp<<bitRes) < totalBitsQ3ForDynalloc-totalBoostGo && boost < capsGo[i]; j++ {
			flag := rdGo.DecodeBit(uint(dynallocLoopLogp))
			tellFracDynallocGo = rdGo.TellFrac()
			if flag == 0 {
				break
			}
			boost += quanta
			totalBoostGo += quanta
			dynallocLoopLogp = 1
		}

		if boost > 0 && dynallocLogpGo > 2 {
			dynallocLogpGo--
		}
		offsetsGo[i] = boost
	}
	t.Logf("Tell after dynalloc: %d bits", rdGo.Tell())
	t.Logf("Dynalloc offsets (go, Q3 bits): %v", offsetsGo[:nbBands])
	for i := 0; i < nbBands; i++ {
		if offsetsLib[i] != offsetsGo[i] {
			t.Logf("Dynalloc offset diff band %d: lib=%d go=%d", i, offsetsLib[i], offsetsGo[i])
		}
	}

	// Decode trim
	trimGo := rdGo.DecodeICDF(trimICDF, 7)
	t.Logf("Trim=%d, Tell after trim: %d bits", trimGo, rdGo.Tell())

	// Compute allocation
	bitsUsedGo := rdGo.TellFrac()
	totalBitsQ3Go := (targetBits << bitRes) - bitsUsedGo - 1
	totalBitsQ3Go -= antiCollapseRsv

	allocResultGo := celt.ComputeAllocationWithDecoder(
		rdGo, totalBitsQ3Go>>bitRes,
		nbBands, 1, capsGo, offsetsGo, trimGo,
		nbBands, false, lm,
	)
	t.Logf("Tell after allocation: %d bits", rdGo.Tell())

	// Decode fine energy - uses raw bits from END of buffer
	fineQGo := make([]int, nbBands)
	for i := 0; i < nbBands; i++ {
		fineBits := allocResultGo.FineBits[i]
		if fineBits == 0 {
			continue
		}
		q := rdGo.DecodeRawBits(uint(fineBits))
		fineQGo[i] = int(q)
	}
	t.Logf("Tell after fine energy: %d bits", rdGo.Tell())

	// Now compare
	t.Log("")
	t.Log("=== Coarse Energy Comparison ===")
	coarseDiffers := false
	for i := 0; i < nbBands; i++ {
		diff := math.Abs(coarseLib[i] - coarseGo[i])
		marker := ""
		if diff > 0.001 {
			marker = " <-- DIFF"
			coarseDiffers = true
		}
		t.Logf("Band %2d: lib=%+.4f go=%+.4f diff=%+.6f%s",
			i, coarseLib[i], coarseGo[i], coarseLib[i]-coarseGo[i], marker)
	}

	if coarseDiffers {
		t.Log("")
		t.Log("COARSE ENERGIES DIFFER - this is the root cause!")
	}

	t.Log("")
	t.Log("=== Fine Quant Comparison ===")
	fineQDiffers := false
	for i := 0; i < nbBands; i++ {
		fbLib := allocResultLib.FineBits[i]
		fbGo := allocResultGo.FineBits[i]
		if fbLib == 0 && fbGo == 0 {
			continue
		}
		marker := ""
		if fineQLib[i] != fineQGo[i] || fbLib != fbGo {
			marker = " <-- DIFF"
			fineQDiffers = true
		}
		t.Logf("Band %2d: lib_q=%d go_q=%d (lib_fb=%d go_fb=%d)%s",
			i, fineQLib[i], fineQGo[i], fbLib, fbGo, marker)
	}

	if fineQDiffers {
		t.Log("")
		t.Log("FINE QUANT DIFFERS!")
	}

	// TF comparison
	t.Log("")
	t.Log("=== TF Comparison ===")
	tfDiffers := false
	for i := 0; i < nbBands; i++ {
		if tfResLib[i] != tfResGo[i] {
			t.Logf("TF band %d: lib=%d go=%d <-- DIFF", i, tfResLib[i], tfResGo[i])
			tfDiffers = true
		}
	}
	if !tfDiffers {
		t.Log("TF values match")
	}

	// Other parameters
	t.Log("")
	t.Log("=== Other Parameters ===")
	t.Logf("Spread: lib=%d go=%d", spreadLib, spreadGo)
	t.Logf("Trim: lib=%d go=%d", trimLib, trimGo)

	// Final range
	t.Log("")
	t.Logf("Final range: gopus=0x%08X libopus=0x%08X", goEnc.FinalRange(), libEnc.GetFinalRange())

	// === Now decode PVQ indices from both packets ===
	t.Log("")
	t.Log("=== PVQ Index Comparison ===")

	// Re-init decoders to decode PVQ
	rdLib2 := &rangecoding.Decoder{}
	rdLib2.Init(libPayload)
	rdGo2 := &rangecoding.Decoder{}
	rdGo2.Init(goPacket)

	// Skip to start of PVQ: decode header, coarse, TF, spread, dynalloc, trim, allocation
	// Header
	rdLib2.DecodeBit(15)
	rdLib2.DecodeBit(1)
	rdLib2.DecodeBit(3)
	rdLib2.DecodeBit(3)
	rdGo2.DecodeBit(15)
	rdGo2.DecodeBit(1)
	rdGo2.DecodeBit(3)
	rdGo2.DecodeBit(3)

	// Coarse energy
	goDecLib2 := celt.NewDecoder(1)
	goDecGo2 := celt.NewDecoder(1)
	goDecLib2.DecodeCoarseEnergyWithDecoder(rdLib2, nbBands, intraLib == 1, lm)
	goDecGo2.DecodeCoarseEnergyWithDecoder(rdGo2, nbBands, intraGo == 1, lm)

	// TF
	for i := 0; i < nbBands; i++ {
		rdLib2.DecodeBit(1)
		rdGo2.DecodeBit(1)
	}
	rdLib2.DecodeBit(1) // tf_select
	rdGo2.DecodeBit(1)

	// Spread
	rdLib2.DecodeICDF(spreadICDF, 5)
	rdGo2.DecodeICDF(spreadICDF, 5)

	// Dynalloc - need to replicate full logic
	capsLib2 := celt.InitCaps(nbBands, lm, 1)
	capsGo2 := celt.InitCaps(nbBands, lm, 1)
	dynallocLogpLib2 := 6
	dynallocLogpGo2 := 6
	totalBoostLib2 := 0
	totalBoostGo2 := 0
	tellFracDynallocLib2 := rdLib2.TellFrac()
	tellFracDynallocGo2 := rdGo2.TellFrac()

	for i := 0; i < nbBands; i++ {
		width := celt.ScaledBandWidth(i, 120<<lm)
		if width <= 0 {
			width = 1
		}
		innerMax := 6 << bitRes
		if width > innerMax {
			innerMax = width
		}
		quanta := width << bitRes
		if quanta > innerMax {
			quanta = innerMax
		}

		// Lib
		dynallocLoopLogpLib := dynallocLogpLib2
		boostLib := 0
		for j := 0; tellFracDynallocLib2+(dynallocLoopLogpLib<<bitRes) < totalBitsQ3ForDynalloc-totalBoostLib2 && boostLib < capsLib2[i]; j++ {
			flag := rdLib2.DecodeBit(uint(dynallocLoopLogpLib))
			tellFracDynallocLib2 = rdLib2.TellFrac()
			if flag == 0 {
				break
			}
			boostLib += quanta
			totalBoostLib2 += quanta
			dynallocLoopLogpLib = 1
		}
		if boostLib > 0 && dynallocLogpLib2 > 2 {
			dynallocLogpLib2--
		}

		// Go
		dynallocLoopLogpGo := dynallocLogpGo2
		boostGo := 0
		for j := 0; tellFracDynallocGo2+(dynallocLoopLogpGo<<bitRes) < totalBitsQ3ForDynalloc-totalBoostGo2 && boostGo < capsGo2[i]; j++ {
			flag := rdGo2.DecodeBit(uint(dynallocLoopLogpGo))
			tellFracDynallocGo2 = rdGo2.TellFrac()
			if flag == 0 {
				break
			}
			boostGo += quanta
			totalBoostGo2 += quanta
			dynallocLoopLogpGo = 1
		}
		if boostGo > 0 && dynallocLogpGo2 > 2 {
			dynallocLogpGo2--
		}
	}

	// Trim
	rdLib2.DecodeICDF(trimICDF, 7)
	rdGo2.DecodeICDF(trimICDF, 7)

	// Allocation
	bitsUsedLib2 := rdLib2.TellFrac()
	bitsUsedGo2 := rdGo2.TellFrac()
	totalBitsQ3Lib2 := (targetBits << bitRes) - bitsUsedLib2 - 1 - antiCollapseRsv
	totalBitsQ3Go2 := (targetBits << bitRes) - bitsUsedGo2 - 1 - antiCollapseRsv

	allocResultLib2 := celt.ComputeAllocationWithDecoder(rdLib2, totalBitsQ3Lib2>>bitRes, nbBands, 1, capsLib2, offsetsLib, trimLib, nbBands, false, lm)
	allocResultGo2 := celt.ComputeAllocationWithDecoder(rdGo2, totalBitsQ3Go2>>bitRes, nbBands, 1, capsGo2, offsetsGo, trimGo, nbBands, false, lm)

	t.Logf("Lib tell after alloc: %d, Go tell: %d", rdLib2.Tell(), rdGo2.Tell())

	// Fine energy (raw bits from end)
	for i := 0; i < nbBands; i++ {
		if allocResultLib2.FineBits[i] > 0 {
			rdLib2.DecodeRawBits(uint(allocResultLib2.FineBits[i]))
		}
		if allocResultGo2.FineBits[i] > 0 {
			rdGo2.DecodeRawBits(uint(allocResultGo2.FineBits[i]))
		}
	}

	// Now decode PVQ for bands until we find a mismatch
	M := 1 << lm
	t.Logf("Tell before PVQ: lib=%d go=%d", rdLib2.Tell(), rdGo2.Tell())

	firstPVQDiff := -1
	for band := 0; band < nbBands; band++ {
		bandStart := celt.EBands[band] * M
		bandEnd := celt.EBands[band+1] * M
		n := bandEnd - bandStart

		bitsLib := allocResultLib2.BandBits[band]
		bitsGo := allocResultGo2.BandBits[band]

		// Get number of pulses from bits
		// bitsToPulses returns pseudo-pulse, getPulses converts to actual K
		qLib := celt.BitsToPulsesExport(band, lm, bitsLib)
		qGo := celt.BitsToPulsesExport(band, lm, bitsGo)
		kLib := celt.GetPulsesExport(qLib)
		kGo := celt.GetPulsesExport(qGo)

		if kLib <= 0 && kGo <= 0 {
			continue
		}

		// Compute PVQ vector size
		vsizeLib := celt.PVQ_V(n, kLib)
		vsizeGo := celt.PVQ_V(n, kGo)

		// Decode PVQ index
		var idxLib, idxGo uint32
		if vsizeLib > 0 {
			idxLib = rdLib2.DecodeUniform(vsizeLib)
		}
		if vsizeGo > 0 {
			idxGo = rdGo2.DecodeUniform(vsizeGo)
		}

		tellLib := rdLib2.Tell()
		tellGo := rdGo2.Tell()
		marker := ""
		if idxLib != idxGo || vsizeLib != vsizeGo || tellLib != tellGo {
			marker = " <-- DIFFERS"
			if firstPVQDiff < 0 {
				firstPVQDiff = band
			}
		}
		t.Logf("Band %d: lib(k=%d, vsize=%d, idx=%d, tell=%d) go(k=%d, vsize=%d, idx=%d, tell=%d)%s",
			band, kLib, vsizeLib, idxLib, tellLib, kGo, vsizeGo, idxGo, tellGo, marker)
		if firstPVQDiff >= 0 {
			break
		}
	}

	if firstPVQDiff >= 0 {
		t.Logf("First PVQ mismatch at band %d", firstPVQDiff)
	}

	// Show bytes around divergence
	t.Log("")
	t.Log("=== Bytes around divergence ===")
	for i := 14; i <= 20; i++ {
		if i < len(libPayload) && i < len(goPacket) {
			marker := ""
			if libPayload[i] != goPacket[i] {
				marker = " <-- DIFFERS"
			}
			t.Logf("Byte %d: lib=0x%02X go=0x%02X%s", i, libPayload[i], goPacket[i], marker)
		}
	}

	// Count differing bytes
	diffCount := 0
	for i := 0; i < minLen; i++ {
		if goPacket[i] != libPayload[i] {
			diffCount++
		}
	}
	t.Logf("Total differing bytes: %d of %d", diffCount, minLen)

	// Show first 20 bytes of both
	t.Log("")
	t.Log("=== First 20 bytes comparison ===")
	for i := 0; i < 20 && i < minLen; i++ {
		marker := ""
		if libPayload[i] != goPacket[i] {
			marker = " <-- DIFFERS"
		}
		t.Logf("Byte %2d: lib=0x%02X go=0x%02X%s", i, libPayload[i], goPacket[i], marker)
	}
}

// TestCompareEncoderStateStepByStep compares gopus and libopus encoder state at each step.
func TestCompareEncoderStateStepByStep(t *testing.T) {
	// Initialize both encoders
	bufSize := 256
	libTracer := NewLibopusEncoderTracer(bufSize)
	defer libTracer.Destroy()

	goBuf := make([]byte, bufSize)
	goEnc := &rangecoding.Encoder{}
	goEnc.Init(goBuf)

	compareState := func(name string, lib ECEncStateTrace, goRng, goVal uint32, goRem int, goExt uint32, goOffs uint32, goTell int) bool {
		match := lib.Rng == goRng && lib.Val == goVal && lib.Rem == goRem && lib.Ext == goExt && lib.Offs == goOffs
		marker := ""
		if !match {
			marker = " <-- DIFFERS"
		}
		t.Logf("%s: lib(rng=%08X val=%08X rem=%d ext=%d offs=%d tell=%d) go(rng=%08X val=%08X rem=%d ext=%d offs=%d tell=%d)%s",
			name,
			lib.Rng, lib.Val, lib.Rem, lib.Ext, lib.Offs, lib.Tell,
			goRng, goVal, goRem, goExt, goOffs, goTell, marker)
		return match
	}

	// Initial state
	libState := libTracer.GetState()
	t.Log("=== Initial State ===")
	if !compareState("Init", libState, goEnc.Range(), goEnc.Val(), goEnc.Rem(), goEnc.Ext(), uint32(goEnc.RangeBytes()), goEnc.Tell()) {
		t.Error("Initial state differs!")
	}

	// Encode header bits - same as CELT frame:
	// silence (logp=15, val=0)
	t.Log("\n=== Encoding silence bit (logp=15, val=0) ===")
	_, libState = libTracer.EncodeBitLogp(0, 15)
	goEnc.EncodeBit(0, 15)
	if !compareState("After silence", libState, goEnc.Range(), goEnc.Val(), goEnc.Rem(), goEnc.Ext(), uint32(goEnc.RangeBytes()), goEnc.Tell()) {
		t.Error("State differs after silence!")
	}

	// postfilter (logp=1, val=0)
	t.Log("\n=== Encoding postfilter bit (logp=1, val=0) ===")
	_, libState = libTracer.EncodeBitLogp(0, 1)
	goEnc.EncodeBit(0, 1)
	if !compareState("After postfilter", libState, goEnc.Range(), goEnc.Val(), goEnc.Rem(), goEnc.Ext(), uint32(goEnc.RangeBytes()), goEnc.Tell()) {
		t.Error("State differs after postfilter!")
	}

	// transient (logp=3, val=1) -- assuming transient frame for this test
	t.Log("\n=== Encoding transient bit (logp=3, val=1) ===")
	_, libState = libTracer.EncodeBitLogp(1, 3)
	goEnc.EncodeBit(1, 3)
	if !compareState("After transient", libState, goEnc.Range(), goEnc.Val(), goEnc.Rem(), goEnc.Ext(), uint32(goEnc.RangeBytes()), goEnc.Tell()) {
		t.Error("State differs after transient!")
	}

	// intra (logp=3, val=0)
	t.Log("\n=== Encoding intra bit (logp=3, val=0) ===")
	_, libState = libTracer.EncodeBitLogp(0, 3)
	goEnc.EncodeBit(0, 3)
	if !compareState("After intra", libState, goEnc.Range(), goEnc.Val(), goEnc.Rem(), goEnc.Ext(), uint32(goEnc.RangeBytes()), goEnc.Tell()) {
		t.Error("State differs after intra!")
	}

	// Now encode some uniform values (simulating PVQ indices)
	// Use the same ft and val from our earlier analysis
	t.Log("\n=== Encoding uniform value (band 0 PVQ index) ===")
	ft := uint32(4066763520)
	val := uint32(1108958327)
	_, libState = libTracer.EncodeUniform(val, ft)
	goEnc.EncodeUniform(val, ft)
	if !compareState("After PVQ band0", libState, goEnc.Range(), goEnc.Val(), goEnc.Rem(), goEnc.Ext(), uint32(goEnc.RangeBytes()), goEnc.Tell()) {
		t.Error("State differs after PVQ band0!")
	}

	// Add a second uniform value (band 1)
	t.Log("\n=== Encoding uniform value (band 1 PVQ index) ===")
	ft2 := uint32(2878677914) // Different value for band 1
	val2 := uint32(1234567)   // Some sample value
	_, libState = libTracer.EncodeUniform(val2, ft2)
	goEnc.EncodeUniform(val2, ft2)
	if !compareState("After PVQ band1", libState, goEnc.Range(), goEnc.Val(), goEnc.Rem(), goEnc.Ext(), uint32(goEnc.RangeBytes()), goEnc.Tell()) {
		t.Error("State differs after PVQ band1!")
	}
}
