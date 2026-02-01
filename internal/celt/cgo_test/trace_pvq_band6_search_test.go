// Package cgo traces PVQ search input/output for a specific band.
package cgo

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestTracePVQSearchBand2 compares gopus vs libopus PVQ search on the same input.
func TestTracePVQSearchBand2(t *testing.T) {
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

	var captured []celt.PVQDebugInfo
	const targetBand = 2
	celt.PVQDebugHook = func(info celt.PVQDebugInfo) {
		if info.Band == targetBand {
			captured = append(captured, info)
		}
	}
	defer func() { celt.PVQDebugHook = nil }()

	// Encode with gopus to trigger PVQDebugHook.
	goEnc := celt.NewEncoder(1)
	goEnc.Reset()
	goEnc.SetBitrate(bitrate)
	goEnc.SetComplexity(10)
	goEnc.SetVBR(false)
	goPacket, err := goEnc.EncodeFrame(pcm64, frameSize)
	if err != nil {
		t.Fatalf("gopus encode failed: %v", err)
	}

	if len(captured) == 0 || captured[len(captured)-1].N == 0 || captured[len(captured)-1].K == 0 || len(captured[len(captured)-1].X) == 0 {
		t.Fatalf("PVQDebugHook did not capture band 6 input")
	}

	t.Logf("Captured %d PVQ entries for band %d", len(captured), targetBand)
	for i, info := range captured {
		// Run libopus PVQ search on the same input.
		libPulses, _ := LibopusPVQSearch(info.X, info.K)
		libIndex := celt.EncodePulses(libPulses, info.N, info.K)

		// Run gopus PVQ search on the same input.
		goPulses, _ := celt.OpPVQSearchExport(append([]float64(nil), info.X...), info.K)
		goIndex := celt.EncodePulses(goPulses, info.N, info.K)

		t.Logf("Entry %d: n=%d k=%d spread=%d B=%d vsize=%d captured_idx=%d go_idx=%d lib_idx=%d",
			i, info.N, info.K, info.Spread, info.B, info.VSize, info.Index, goIndex, libIndex)

		if goIndex != libIndex {
			t.Log("PVQ search indices differ on identical input")
			t.Logf("First 10 pulses (gopus): %v", goPulses[:minIntLocal(10, len(goPulses))])
			t.Logf("First 10 pulses (libopus): %v", libPulses[:minIntLocal(10, len(libPulses))])
			t.Fatalf("PVQ search mismatch for band %d", info.Band)
		}
	}

	// Decode gopus packet and compare decoded PVQ indices for the band.
	decTracer := &pvqCaptureTracer{}
	if err := decodePVQWithTracer(goPacket, frameSize, 0, decTracer); err != nil {
		t.Fatalf("decode gopus failed: %v", err)
	}
	var decoded []pvqEntry
	for _, e := range decTracer.entries {
		if e.band == targetBand {
			decoded = append(decoded, e)
		}
	}
	t.Logf("Decoded %d PVQ entries for band %d", len(decoded), targetBand)
	minLen := len(decoded)
	if len(captured) < minLen {
		minLen = len(captured)
	}
	for i := 0; i < minLen; i++ {
		t.Logf("Enc entry %d: k=%d idx=%d | Dec entry %d: k=%d idx=%d",
			i, captured[i].K, captured[i].Index, i, decoded[i].k, decoded[i].index)
	}
}

// TestTracePVQSearchBand6 compares gopus vs libopus PVQ search on the same input for band 6.
func TestTracePVQSearchBand6(t *testing.T) {
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

	var captured []celt.PVQDebugInfo
	const targetBand = 6
	celt.PVQDebugHook = func(info celt.PVQDebugInfo) {
		if info.Band == targetBand {
			captured = append(captured, info)
		}
	}
	defer func() { celt.PVQDebugHook = nil }()

	// Encode with gopus to trigger PVQDebugHook.
	goEnc := celt.NewEncoder(1)
	goEnc.Reset()
	goEnc.SetBitrate(bitrate)
	goEnc.SetComplexity(10)
	goEnc.SetVBR(false)
	goPacket, err := goEnc.EncodeFrame(pcm64, frameSize)
	if err != nil {
		t.Fatalf("gopus encode failed: %v", err)
	}

	if len(captured) == 0 || captured[len(captured)-1].N == 0 || captured[len(captured)-1].K == 0 || len(captured[len(captured)-1].X) == 0 {
		t.Fatalf("PVQDebugHook did not capture band %d input", targetBand)
	}

	t.Logf("Captured %d PVQ entries for band %d", len(captured), targetBand)
	for i, info := range captured {
		vSize := info.VSize
		// Run libopus PVQ search on the same input.
		libPulses, _ := LibopusPVQSearch(info.X, info.K)
		libIndex := celt.EncodePulses(libPulses, info.N, info.K)

		// Run gopus PVQ search on the same input.
		goPulses, _ := celt.OpPVQSearchExport(append([]float64(nil), info.X...), info.K)
		goIndex := celt.EncodePulses(goPulses, info.N, info.K)

		t.Logf("Entry %d: n=%d k=%d spread=%d B=%d vsize=%d captured_idx=%d go_idx=%d lib_idx=%d",
			i, info.N, info.K, info.Spread, info.B, vSize, info.Index, goIndex, libIndex)

		if goIndex != libIndex {
			t.Log("PVQ search indices differ on identical input")
			t.Logf("First 10 pulses (gopus): %v", goPulses[:minIntLocal(10, len(goPulses))])
			t.Logf("First 10 pulses (libopus): %v", libPulses[:minIntLocal(10, len(libPulses))])
			t.Fatalf("PVQ search mismatch for band %d", info.Band)
		}

		// Verify EncodeUniform byte output matches libopus for this index/vsize.
		goEnc := &rangecoding.Encoder{}
		goBuf := make([]byte, 32)
		goEnc.Init(goBuf)
		goEnc.EncodeUniform(goIndex, vSize)
		goBytes := goEnc.Done()

		// Round-trip with gopus decoder to verify uniform symmetry.
		goDec := &rangecoding.Decoder{}
		goDec.Init(goBytes)
		decodedIdx := goDec.DecodeUniform(vSize)
		if decodedIdx != goIndex {
			t.Logf("Encode/DecodeUniform mismatch: got %d want %d (vsize=%d)", decodedIdx, goIndex, vSize)
		}

		libBytes, _ := LibopusEncodeUniformSequence([]uint32{goIndex}, []uint32{vSize})
		if len(goBytes) != len(libBytes) {
			t.Logf("EncodeUniform length mismatch: go=%d lib=%d", len(goBytes), len(libBytes))
		} else {
			for b := range goBytes {
				if goBytes[b] != libBytes[b] {
					t.Logf("EncodeUniform bytes differ at %d: go=0x%02X lib=0x%02X", b, goBytes[b], libBytes[b])
					break
				}
			}
		}

		// Verify pulse->index mapping matches libopus encode_pulses.
		libPulseBytes, _ := LibopusEncodePulsesToBytes(goPulses, info.N, info.K)
		if len(goBytes) != len(libPulseBytes) {
			t.Logf("EncodePulses length mismatch: go=%d lib=%d", len(goBytes), len(libPulseBytes))
		} else {
			for b := range goBytes {
				if goBytes[b] != libPulseBytes[b] {
					t.Logf("EncodePulses bytes differ at %d: go=0x%02X lib=0x%02X", b, goBytes[b], libPulseBytes[b])
					break
				}
			}
		}
	}

	// Decode libopus packet and log PVQ entries for the band.
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

	libTracer := &pvqCaptureTracer{}
	if err := decodePVQWithTracer(libPayload, frameSize, 0, libTracer); err != nil {
		t.Fatalf("decode libopus failed: %v", err)
	}
	goTracer := &pvqCaptureTracer{}
	if err := decodePVQWithTracer(goPacket, frameSize, 0, goTracer); err != nil {
		t.Fatalf("decode gopus failed: %v", err)
	}

	t.Logf("Decode PVQ entries for band %d (libopus packet):", targetBand)
	for _, e := range libTracer.entries {
		if e.band == targetBand {
			t.Logf("  lib band=%d k=%d n=%d idx=%d", e.band, e.k, e.n, e.index)
		}
	}
	t.Logf("Decode PVQ entries for band %d (gopus packet):", targetBand)
	for _, e := range goTracer.entries {
		if e.band == targetBand {
			t.Logf("  go  band=%d k=%d n=%d idx=%d", e.band, e.k, e.n, e.index)
		}
	}
}

// TestQuantDebugBand6EncodeDecode compares quantPartition decisions for band 6 between encoder and decoder.
func TestQuantDebugBand6EncodeDecode(t *testing.T) {
	frameSize := 960
	sampleRate := 48000
	bitrate := 64000

	// Generate 440Hz sine wave
	pcm64 := make([]float64, frameSize)
	for i := range pcm64 {
		ti := float64(i) / float64(sampleRate)
		val := 0.5 * math.Sin(2*math.Pi*440*ti)
		pcm64[i] = val
	}

	var encInfos []celt.QuantDebugInfo
	var decInfos []celt.QuantDebugInfo
	var encBandInfos []celt.BandDebugInfo
	var decBandInfos []celt.BandDebugInfo
	var encOffsets []int
	var decOffsets []int
	var encTrim *celt.TrimDebugInfo
	var decTrim *celt.TrimDebugInfo
	celt.QuantDebugHook = func(info celt.QuantDebugInfo) {
		if info.Encode {
			encInfos = append(encInfos, info)
		} else {
			decInfos = append(decInfos, info)
		}
	}
	celt.BandDebugHook = func(info celt.BandDebugInfo) {
		if info.Encode {
			encBandInfos = append(encBandInfos, info)
		} else {
			decBandInfos = append(decBandInfos, info)
		}
	}
	celt.DynallocDebugHook = func(info celt.DynallocDebugInfo) {
		if info.Encode {
			encOffsets = info.Offsets
		} else {
			decOffsets = info.Offsets
		}
	}
	celt.TrimDebugHook = func(info celt.TrimDebugInfo) {
		if info.Encode {
			tmp := info
			encTrim = &tmp
		} else {
			tmp := info
			decTrim = &tmp
		}
	}
	defer func() {
		celt.QuantDebugHook = nil
		celt.BandDebugHook = nil
		celt.DynallocDebugHook = nil
		celt.TrimDebugHook = nil
	}()

	goEnc := celt.NewEncoder(1)
	goEnc.Reset()
	goEnc.SetBitrate(bitrate)
	goEnc.SetComplexity(10)
	goEnc.SetVBR(false)
	goPacket, err := goEnc.EncodeFrame(pcm64, frameSize)
	if err != nil {
		t.Fatalf("gopus encode failed: %v", err)
	}

	dec := celt.NewDecoder(1)
	if _, err := dec.DecodeFrame(goPacket, frameSize); err != nil {
		t.Fatalf("gopus decode failed: %v", err)
	}

	const targetBand = 6
	filter := func(infos []celt.QuantDebugInfo) []celt.QuantDebugInfo {
		var out []celt.QuantDebugInfo
		for _, info := range infos {
			if info.Band == targetBand && !info.Split {
				out = append(out, info)
			}
		}
		return out
	}
	encBand := filter(encInfos)
	decBand := filter(decInfos)
	if len(encBand) == 0 || len(decBand) == 0 {
		t.Fatalf("missing quant debug entries for band %d (enc=%d dec=%d)", targetBand, len(encBand), len(decBand))
	}
	enc := encBand[0]
	decInfo := decBand[0]
	t.Logf("Band %d enc: bits=%d q=%d k=%d currBits=%d rem=%d n=%d B=%d",
		targetBand, enc.Bits, enc.Q, enc.K, enc.CurrBits, enc.RemainingBits, enc.N, enc.B)
	t.Logf("Band %d dec: bits=%d q=%d k=%d currBits=%d rem=%d n=%d B=%d",
		targetBand, decInfo.Bits, decInfo.Q, decInfo.K, decInfo.CurrBits, decInfo.RemainingBits, decInfo.N, decInfo.B)

	filterBand := func(infos []celt.BandDebugInfo) []celt.BandDebugInfo {
		var out []celt.BandDebugInfo
		for _, info := range infos {
			if info.Band == targetBand {
				out = append(out, info)
			}
		}
		return out
	}
	encBandState := filterBand(encBandInfos)
	decBandState := filterBand(decBandInfos)
	if len(encBandState) > 0 && len(decBandState) > 0 {
		e := encBandState[0]
		d := decBandState[0]
		t.Logf("Band %d enc alloc: tell=%d balance=%d remaining=%d bits=%d currBal=%d pulses=%d coded=%d",
			targetBand, e.TellFrac, e.Balance, e.RemainingBits, e.Bits, e.CurrBalance, e.Pulses, e.CodedBands)
		t.Logf("Band %d dec alloc: tell=%d balance=%d remaining=%d bits=%d currBal=%d pulses=%d coded=%d",
			targetBand, d.TellFrac, d.Balance, d.RemainingBits, d.Bits, d.CurrBalance, d.Pulses, d.CodedBands)
	}

	minLen := len(encBandInfos)
	if len(decBandInfos) < minLen {
		minLen = len(decBandInfos)
	}
	firstDiff := -1
	for i := 0; i < minLen; i++ {
		encB := encBandInfos[i]
		decB := decBandInfos[i]
		if encB.Band != decB.Band || encB.TellFrac != decB.TellFrac || encB.Bits != decB.Bits || encB.Balance != decB.Balance {
			firstDiff = i
			break
		}
	}
	if firstDiff >= 0 {
		encB := encBandInfos[firstDiff]
		decB := decBandInfos[firstDiff]
		t.Logf("First band alloc mismatch at idx %d (band %d): enc tell=%d bits=%d balance=%d remaining=%d currBal=%d pulses=%d coded=%d | dec tell=%d bits=%d balance=%d remaining=%d currBal=%d pulses=%d coded=%d",
			firstDiff, encB.Band,
			encB.TellFrac, encB.Bits, encB.Balance, encB.RemainingBits, encB.CurrBalance, encB.Pulses, encB.CodedBands,
			decB.TellFrac, decB.Bits, decB.Balance, decB.RemainingBits, decB.CurrBalance, decB.Pulses, decB.CodedBands)
	}

	if len(encOffsets) > 0 && len(decOffsets) > 0 {
		minOff := len(encOffsets)
		if len(decOffsets) < minOff {
			minOff = len(decOffsets)
		}
		offDiff := -1
		for i := 0; i < minOff; i++ {
			if encOffsets[i] != decOffsets[i] {
				offDiff = i
				break
			}
		}
		if offDiff >= 0 {
			t.Logf("Dynalloc offset mismatch at band %d: enc=%d dec=%d", offDiff, encOffsets[offDiff], decOffsets[offDiff])
		}
	}

	if encTrim != nil && decTrim != nil {
		t.Logf("Alloc trim enc: trim=%d encoded=%v | dec: trim=%d encoded=%v",
			encTrim.Trim, encTrim.Encoded, decTrim.Trim, decTrim.Encoded)
	}
	if enc.Bits != decInfo.Bits || enc.Q != decInfo.Q || enc.K != decInfo.K || enc.CurrBits != decInfo.CurrBits || enc.N != decInfo.N || enc.B != decInfo.B {
		t.Fatalf("quant debug mismatch for band %d", targetBand)
	}
}
