//go:build gopus_qext
// +build gopus_qext

package celt

import (
	"bytes"
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/rangecoding"
)

func TestComputeQEXTModeConfig48k(t *testing.T) {
	cfg, ok := computeQEXTModeConfig(48000, 120)
	if !ok {
		t.Fatal("computeQEXTModeConfig(48000,120)=false want true")
	}
	if cfg.ShortMDCTSize != 120 {
		t.Fatalf("ShortMDCTSize=%d want 120", cfg.ShortMDCTSize)
	}
	if cfg.EffBands != 2 {
		t.Fatalf("EffBands=%d want 2", cfg.EffBands)
	}
	if len(cfg.EBands) != len(qextEBands240) {
		t.Fatalf("len(EBands)=%d want %d", len(cfg.EBands), len(qextEBands240))
	}
	if len(cfg.LogN) != nbQEXTBands {
		t.Fatalf("len(LogN)=%d want %d", len(cfg.LogN), nbQEXTBands)
	}
	if cfg.EBands[0] != 100 || cfg.EBands[2] != 120 || cfg.EBands[14] != 240 {
		t.Fatalf("unexpected qext eBands: first=%d third=%d last=%d", cfg.EBands[0], cfg.EBands[2], cfg.EBands[14])
	}
	for i, got := range cfg.LogN {
		if got != 27 {
			t.Fatalf("LogN[%d]=%d want 27", i, got)
		}
	}
	if len(cfg.CacheIndex) != len(qextCacheIndex50) || len(cfg.CacheBits) != len(qextCacheBits50) || len(cfg.CacheCaps) != len(qextCacheCaps50) {
		t.Fatalf("unexpected cache lengths: index=%d bits=%d caps=%d", len(cfg.CacheIndex), len(cfg.CacheBits), len(cfg.CacheCaps))
	}
}

func TestComputeQEXTModeConfig96k(t *testing.T) {
	cfg, ok := computeQEXTModeConfig(96000, 240)
	if !ok {
		t.Fatal("computeQEXTModeConfig(96000,240)=false want true")
	}
	if cfg.EffBands != nbQEXTBands {
		t.Fatalf("EffBands=%d want %d", cfg.EffBands, nbQEXTBands)
	}
	if cfg.EBands[0] != 100 || cfg.EBands[14] != 240 {
		t.Fatalf("unexpected qext eBands for 96k mode: first=%d last=%d", cfg.EBands[0], cfg.EBands[14])
	}
}

func TestComputeQEXTModeConfig180Path(t *testing.T) {
	cfg, ok := computeQEXTModeConfig(96000, 180)
	if !ok {
		t.Fatal("computeQEXTModeConfig(96000,180)=false want true")
	}
	if cfg.EffBands != nbQEXTBands {
		t.Fatalf("EffBands=%d want %d", cfg.EffBands, nbQEXTBands)
	}
	if cfg.EBands[0] != 74 || cfg.EBands[14] != 180 {
		t.Fatalf("unexpected qext eBands 180-path: first=%d last=%d", cfg.EBands[0], cfg.EBands[14])
	}
	if got, want := cfg.LogN[0], 24; got != want {
		t.Fatalf("LogN[0]=%d want %d", got, want)
	}
	if got, want := cfg.LogN[len(cfg.LogN)-1], 21; got != want {
		t.Fatalf("LogN[last]=%d want %d", got, want)
	}
}

func TestComputeQEXTModeConfigRejectsUnsupportedMode(t *testing.T) {
	if _, ok := computeQEXTModeConfig(48000, 96); ok {
		t.Fatal("computeQEXTModeConfig(48000,96)=true want false")
	}
}

func TestQEXTDepthRoundTrip(t *testing.T) {
	depths := []int{0, 7, 7, 48, 12, 0, 48, 3, 3, 0}

	var enc rangecoding.Encoder
	buf := make([]byte, 64)
	enc.Init(buf)
	lastEnc := 0
	for _, depth := range depths {
		encodeQEXTDepth(&enc, depth, 48, &lastEnc)
	}
	packet := append([]byte(nil), enc.Done()...)

	var dec rangecoding.Decoder
	dec.Init(packet)
	lastDec := 0
	for i, want := range depths {
		if got := decodeQEXTDepth(&dec, 48, &lastDec); got != want {
			t.Fatalf("decodeQEXTDepth[%d]=%d want %d", i, got, want)
		}
	}
}

func TestQEXTHeaderRoundTrip(t *testing.T) {
	tests := []struct {
		name     string
		channels int
		hdr      qextHeader
	}{
		{
			name:     "mono_2band_inter",
			channels: 1,
			hdr: qextHeader{
				EndBands: 2,
			},
		},
		{
			name:     "stereo_2band_intensity0",
			channels: 2,
			hdr: qextHeader{
				EndBands:   2,
				Intensity:  0,
				DualStereo: true, // ignored when intensity=0
			},
		},
		{
			name:     "stereo_fullband_dual_intra",
			channels: 2,
			hdr: qextHeader{
				EndBands:   nbQEXTBands,
				Intensity:  nbQEXTBands,
				DualStereo: true,
			},
		},
	}

	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			var enc rangecoding.Encoder
			buf := make([]byte, 16)
			enc.Init(buf)
			encodeQEXTHeader(&enc, tc.channels, tc.hdr)
			payload := append([]byte(nil), enc.Done()...)

			var dec rangecoding.Decoder
			dec.Init(payload)
			got := decodeQEXTHeader(&dec, tc.channels, len(payload))

			if got.EndBands != tc.hdr.EndBands {
				t.Fatalf("EndBands=%d want %d", got.EndBands, tc.hdr.EndBands)
			}
			if tc.channels == 2 && got.Intensity != tc.hdr.Intensity {
				t.Fatalf("Intensity=%d want %d", got.Intensity, tc.hdr.Intensity)
			}
			wantDual := tc.hdr.DualStereo && tc.hdr.Intensity != 0
			if got.DualStereo != wantDual {
				t.Fatalf("DualStereo=%v want %v", got.DualStereo, wantDual)
			}
		})
	}
}

func TestComputeThetaExtUsesQEXTForMonoSplit(t *testing.T) {
	x := []float64{0.99, 0.10, 0.05, 0.02}
	y := []float64{0.10, 0.05, 0.02, 0.01}

	var mainEnc rangecoding.Encoder
	mainBuf := make([]byte, 32)
	mainEnc.Init(mainBuf)
	var extEnc rangecoding.Encoder
	extBuf := make([]byte, 32)
	extEnc.Init(extBuf)

	fillEnc := 1
	bitsEnc := 80
	extBitsEnc := 80
	encCtx := &bandCtx{
		re:            &mainEnc,
		extEnc:        &extEnc,
		encode:        true,
		band:          0,
		intensity:     MaxBands,
		remainingBits: 1 << 20,
		extTotalBits:  len(extBuf) * 8 << bitRes,
	}
	var encSplit splitCtx
	computeThetaExt(encCtx, &encSplit, append([]float64(nil), x...), append([]float64(nil), y...), len(x), &bitsEnc, &extBitsEnc, 1, 1, 0, false, &fillEnc)

	if got := extEnc.TellFrac(); got <= 0 {
		t.Fatal("mono split did not emit any QEXT theta refinement bits")
	}
	if extBitsEnc >= 80 {
		t.Fatalf("mono split ext budget not consumed: got %d want < 80", extBitsEnc)
	}

	mainPayload := append([]byte(nil), mainEnc.Done()...)
	extPayload := append([]byte(nil), extEnc.Done()...)
	if len(extPayload) == 0 {
		t.Fatal("mono split produced empty extension payload")
	}

	var mainDec rangecoding.Decoder
	mainDec.Init(mainPayload)
	var extDec rangecoding.Decoder
	extDec.Init(extPayload)

	fillDec := 1
	bitsDec := 80
	extBitsDec := 80
	decCtx := &bandCtx{
		rd:            &mainDec,
		extDec:        &extDec,
		encode:        false,
		band:          0,
		intensity:     MaxBands,
		remainingBits: 1 << 20,
		extTotalBits:  len(extPayload) * 8 << bitRes,
	}
	var decSplit splitCtx
	computeThetaExt(decCtx, &decSplit, make([]float64, len(x)), make([]float64, len(y)), len(x), &bitsDec, &extBitsDec, 1, 1, 0, false, &fillDec)

	if decSplit.itheta != encSplit.itheta {
		t.Fatalf("itheta=%d want %d", decSplit.itheta, encSplit.itheta)
	}
	if got := extDec.TellFrac(); got <= 0 {
		t.Fatal("mono split decode did not consume any QEXT theta refinement bits")
	}
	if extBitsDec >= 80 {
		t.Fatalf("mono split decode ext budget not consumed: got %d want < 80", extBitsDec)
	}
}

func TestAlgUnquantIntoQEXTN2LargeEnergyUsesWideAccumulator(t *testing.T) {
	const (
		n         = 2
		k         = 128
		extraBits = 12
		gain      = 1.0
	)
	basePulses := []int{-3, -125}
	up := (1 << extraBits) - 1
	refine := -1436

	index := EncodePulses(basePulses, n, k)
	if want := k; abs(basePulses[0])+abs(basePulses[1]) != want {
		t.Fatalf("invalid base pulses: %v", basePulses)
	}

	var mainEnc rangecoding.Encoder
	mainBuf := make([]byte, 32)
	mainEnc.Init(mainBuf)
	mainEnc.EncodeUniform(index, PVQ_V(n, k))
	mainPayload := append([]byte(nil), mainEnc.Done()...)

	var extEnc rangecoding.Encoder
	extBuf := make([]byte, 32)
	extEnc.Init(extBuf)
	extEnc.EncodeUniform(uint32(refine+(up-1)/2), uint32(up))
	extPayload := append([]byte(nil), extEnc.Done()...)

	var mainDec rangecoding.Decoder
	mainDec.Init(mainPayload)
	var extDec rangecoding.Decoder
	extDec.Init(extPayload)

	got := make([]float64, n)
	collapse := algUnquantInto(got, &mainDec, 0, n, k, spreadNone, 1, gain, &extDec, extraBits, nil)
	if collapse != 1 {
		t.Fatalf("collapse=%d want 1", collapse)
	}

	wantPulses := []float64{-10849, -513311}
	energy := wantPulses[0]*wantPulses[0] + wantPulses[1]*wantPulses[1]
	scale := gain / math.Sqrt(energy)
	want0 := wantPulses[0] * scale
	want1 := wantPulses[1] * scale

	if diff := math.Abs(got[0] - want0); diff > 1e-12 {
		t.Fatalf("got[0]=%.15f want %.15f diff %.3e", got[0], want0, diff)
	}
	if diff := math.Abs(got[1] - want1); diff > 1e-12 {
		t.Fatalf("got[1]=%.15f want %.15f diff %.3e", got[1], want1, diff)
	}

	norm := got[0]*got[0] + got[1]*got[1]
	if math.Abs(norm-1.0) > 1e-12 {
		t.Fatalf("norm=%.15f want 1", norm)
	}
}

func TestComputeQEXTBandLogEInto(t *testing.T) {
	cfg, ok := computeQEXTModeConfig(48000, 120)
	if !ok {
		t.Fatal("computeQEXTModeConfig(48000,120)=false want true")
	}

	coeffs := make([]float64, 960)
	for i := cfg.EBands[0] << 3; i < cfg.EBands[1]<<3; i++ {
		coeffs[i] = 0.5
	}
	for i := cfg.EBands[1] << 3; i < cfg.EBands[2]<<3; i++ {
		coeffs[i] = 0.25
	}

	bandE := make([]float64, cfg.EffBands)
	bandLogE := make([]float64, cfg.EffBands)
	computeQEXTBandLogEInto(coeffs, &cfg, cfg.EffBands, 3, bandE, bandLogE)

	if bandE[0] <= bandE[1] {
		t.Fatalf("bandE=%v want band 0 > band 1", bandE)
	}
	if bandLogE[0] <= bandLogE[1] {
		t.Fatalf("bandLogE=%v want band 0 > band 1", bandLogE)
	}
}

func TestNormalizeQEXTBandsInto(t *testing.T) {
	cfg, ok := computeQEXTModeConfig(48000, 120)
	if !ok {
		t.Fatal("computeQEXTModeConfig(48000,120)=false want true")
	}

	coeffs := make([]float64, 960)
	for i := cfg.EBands[0] << 3; i < cfg.EBands[1]<<3; i++ {
		coeffs[i] = 0.5
	}
	for i := cfg.EBands[1] << 3; i < cfg.EBands[2]<<3; i++ {
		coeffs[i] = 0.25
	}

	bandE := make([]float64, cfg.EffBands)
	computeQEXTBandAmplitudesInto(coeffs, &cfg, cfg.EffBands, 3, bandE)

	norm := make([]float64, len(coeffs))
	normalizeQEXTBandsInto(coeffs, &cfg, cfg.EffBands, 3, bandE, norm)

	firstIdx := cfg.EBands[0] << 3
	secondIdx := cfg.EBands[1] << 3
	if norm[firstIdx] <= 0 || norm[secondIdx] <= 0 {
		t.Fatalf("normalized coeffs not populated: first=%.6f second=%.6f", norm[firstIdx], norm[secondIdx])
	}
	if norm[0] != 0 {
		t.Fatalf("unexpected lowband write before qext range: norm[0]=%.6f", norm[0])
	}
}

func TestEnsureScratchSkipsQEXTBuffersWhenDisabled(t *testing.T) {
	enc := NewEncoder(2)
	enc.EnsureScratch(960)

	if enc.scratch.qext != nil {
		t.Fatalf("qext scratch allocated while disabled: %+v", enc.scratch.qext)
	}

	enc.SetQEXTEnabled(true)
	enc.EnsureScratch(960)

	if enc.scratch.qext == nil {
		t.Fatal("qext scratch not allocated when enabled")
	}
	if got := len(enc.scratch.qext.buf); got == 0 {
		t.Fatal("qextBuf not allocated when enabled")
	}
	if got := len(enc.scratch.qext.normL); got == 0 {
		t.Fatal("qextNormL not allocated when enabled")
	}
	if got := len(enc.scratch.qext.extraBits); got == 0 {
		t.Fatal("qextExtraBits not allocated when enabled")
	}
}

func TestCELTDecoderQEXTStateStaysDormantUntilPayload(t *testing.T) {
	dec := NewDecoder(1)
	if dec.qext != nil && len(dec.qext.oldBandE) != 0 {
		t.Fatalf("NewDecoder allocated qextOldBandE=%d want dormant", len(dec.qext.oldBandE))
	}

	var mainRD rangecoding.Decoder
	mainRD.Init([]byte{0xff})
	if qext := dec.prepareQEXTDecode(nil, &mainRD, MaxBands, 0, 120); qext != nil {
		t.Fatal("prepareQEXTDecode(nil) returned QEXT state")
	}
	if dec.qext != nil && len(dec.qext.oldBandE) != 0 {
		t.Fatalf("empty QEXT payload allocated qextOldBandE=%d want dormant", len(dec.qext.oldBandE))
	}
}

func TestComputeQEXTExtraAllocationEncodeZeroBudget(t *testing.T) {
	cfg, ok := computeQEXTModeConfig(48000, 120)
	if !ok {
		t.Fatal("computeQEXTModeConfig(48000,120)=false want true")
	}

	extraPulses := make([]int, MaxBands+nbQEXTBands)
	extraQuant := make([]int, MaxBands+nbQEXTBands)
	computeQEXTExtraAllocationEncode(0, MaxBands, 2, 0, 2, 0, make([]float64, MaxBands*2), make([]float64, nbQEXTBands*2), &cfg, 0, 0, nil, extraPulses, extraQuant)

	for i := range extraPulses {
		if extraPulses[i] != 0 || extraQuant[i] != 0 {
			t.Fatalf("zero-budget extra allocation[%d]=(%d,%d) want (0,0)", i, extraPulses[i], extraQuant[i])
		}
	}
}

func TestComputeQEXTExtraAllocationEncodeFixture(t *testing.T) {
	cfg, ok := computeQEXTModeConfig(48000, 120)
	if !ok {
		t.Fatal("computeQEXTModeConfig(48000,120)=false want true")
	}

	mainLogE := make([]float64, MaxBands*2)
	for i := 0; i < MaxBands; i++ {
		mainLogE[i] = 0.25 + 0.05*float64(i)
		mainLogE[MaxBands+i] = 0.15 + 0.04*float64(i)
	}
	qextLogE := make([]float64, nbQEXTBands*2)
	for i := 0; i < nbQEXTBands; i++ {
		qextLogE[i] = -0.20 + 0.08*float64(i)
		qextLogE[nbQEXTBands+i] = -0.10 + 0.05*float64(i)
	}

	var enc rangecoding.Encoder
	buf := make([]byte, 64)
	enc.Init(buf)

	extraPulses := make([]int, MaxBands+nbQEXTBands)
	extraQuant := make([]int, MaxBands+nbQEXTBands)
	computeQEXTExtraAllocationEncode(0, MaxBands, 2, 96<<bitRes, 2, 0, mainLogE, qextLogE, &cfg, 0.9, 0.42, &enc, extraPulses, extraQuant)

	wantQuant := []int{2, 2, 2, 1, 1, 1, 1, 1, 1, 1, 1, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 2, 2}
	wantPulses := []int{0, 0, 0, 0, 0, 0, 0, 0, 8, 4, 4, 0, 0, 0, 0, 0, 0, 0, 0, 0, 0, 288, 252}
	wantExt := []byte{0xab, 0xae, 0x86, 0x59, 0x2f, 0x77, 0xd7, 0x05, 0xd3, 0xf7}

	if got := extraQuant[:MaxBands+2]; !intSliceEqual(got, wantQuant) {
		t.Fatalf("extraQuant=%v want %v", got, wantQuant)
	}
	if got := extraPulses[:MaxBands+2]; !intSliceEqual(got, wantPulses) {
		t.Fatalf("extraPulses=%v want %v", got, wantPulses)
	}
	if got := enc.Done(); !bytes.Equal(got, wantExt) {
		t.Fatalf("ext bytes=%x want %x", got, wantExt)
	}

	nonZero := 0
	for i := 0; i < MaxBands+2; i++ {
		if extraQuant[i] > 0 || extraPulses[i] > 0 {
			nonZero++
		}
	}
	if nonZero == 0 {
		t.Fatal("fixture extra allocation produced all zeros")
	}
}

func TestComputeQEXTExtraAllocationDecodeRoundTrip(t *testing.T) {
	for _, channels := range []int{1, 2} {
		channels := channels
		t.Run(fmt.Sprintf("%dch", channels), func(t *testing.T) {
			mainLogE := make([]float64, MaxBands*channels)
			for i := 0; i < MaxBands; i++ {
				mainLogE[i] = 0.25 + 0.05*float64(i)
				if channels == 2 {
					mainLogE[MaxBands+i] = 0.15 + 0.04*float64(i)
				}
			}

			var enc rangecoding.Encoder
			buf := make([]byte, 64)
			enc.Init(buf)

			extraPulsesEnc := make([]int, MaxBands+nbQEXTBands)
			extraQuantEnc := make([]int, MaxBands+nbQEXTBands)
			computeQEXTExtraAllocationEncode(0, MaxBands, 0, 96<<bitRes, channels, 0, mainLogE, nil, nil, 0, 0, &enc, extraPulsesEnc, extraQuantEnc)

			enc.Done()
			payload := append([]byte(nil), enc.Buffer()[:enc.Storage()]...)
			if len(payload) == 0 {
				t.Fatal("encode-side QEXT extra allocation emitted empty payload")
			}

			var dec rangecoding.Decoder
			dec.Init(payload)

			extraPulsesDec := make([]int, MaxBands)
			extraQuantDec := make([]int, MaxBands)
			computeQEXTExtraAllocationDecode(0, MaxBands, len(payload)*8<<bitRes, channels, 0, &dec, extraPulsesDec, extraQuantDec)

			if got, want := extraQuantDec, extraQuantEnc[:MaxBands]; !intSliceEqual(got, want) {
				t.Fatalf("decode extraQuant=%v want %v", got, want)
			}
			if got, want := extraPulsesDec, extraPulsesEnc[:MaxBands]; !intSliceEqual(got, want) {
				t.Fatalf("decode extraPulses=%v want %v", got, want)
			}
		})
	}
}

func TestComputeQEXTExtraAllocationDecodeWithModeRoundTrip(t *testing.T) {
	cfg, ok := computeQEXTModeConfig(48000, 120)
	if !ok {
		t.Fatal("computeQEXTModeConfig(48000,120)=false want true")
	}

	for _, channels := range []int{1, 2} {
		channels := channels
		t.Run(fmt.Sprintf("%dch", channels), func(t *testing.T) {
			mainLogE := make([]float64, MaxBands*channels)
			for i := 0; i < MaxBands; i++ {
				mainLogE[i] = 0.25 + 0.05*float64(i)
				if channels == 2 {
					mainLogE[MaxBands+i] = 0.15 + 0.04*float64(i)
				}
			}
			qextLogE := make([]float64, nbQEXTBands*channels)
			for i := 0; i < nbQEXTBands; i++ {
				qextLogE[i] = -0.20 + 0.08*float64(i)
				if channels == 2 {
					qextLogE[nbQEXTBands+i] = -0.10 + 0.05*float64(i)
				}
			}

			var enc rangecoding.Encoder
			buf := make([]byte, 128)
			enc.Init(buf)

			extraPulsesEnc := make([]int, MaxBands+nbQEXTBands)
			extraQuantEnc := make([]int, MaxBands+nbQEXTBands)
			qextEnd := 2
			computeQEXTExtraAllocationEncode(0, MaxBands, qextEnd, 96<<bitRes, channels, 0, mainLogE, qextLogE, &cfg, 0.9, 0.42, &enc, extraPulsesEnc, extraQuantEnc)

			enc.Done()
			payload := append([]byte(nil), enc.Buffer()[:enc.Storage()]...)
			if len(payload) == 0 {
				t.Fatal("encode-side QEXT extra allocation with mode emitted empty payload")
			}

			var dec rangecoding.Decoder
			dec.Init(payload)

			extraPulsesDec := make([]int, MaxBands+nbQEXTBands)
			extraQuantDec := make([]int, MaxBands+nbQEXTBands)
			computeQEXTExtraAllocationDecodeWithMode(0, MaxBands, qextEnd, len(payload)*8<<bitRes, channels, 0, &dec, extraPulsesDec, extraQuantDec, &cfg)

			if got, want := extraQuantDec[:MaxBands+qextEnd], extraQuantEnc[:MaxBands+qextEnd]; !intSliceEqual(got, want) {
				t.Fatalf("decode extraQuant=%v want %v", got, want)
			}
			if got, want := extraPulsesDec[:MaxBands+qextEnd], extraPulsesEnc[:MaxBands+qextEnd]; !intSliceEqual(got, want) {
				t.Fatalf("decode extraPulses=%v want %v", got, want)
			}
		})
	}
}

func TestEncodeFrameRetainsQEXTPayload(t *testing.T) {
	pcm := generateSineWave(440.0, 960)

	encA := NewEncoder(1)
	encA.SetQEXTEnabled(true)
	encA.SetBitrate(256000)

	packetA, err := encA.EncodeFrame(pcm, 960)
	if err != nil {
		t.Fatalf("EncodeFrame(qext A) failed: %v", err)
	}
	if len(packetA) == 0 {
		t.Fatal("EncodeFrame(qext A) returned empty packet")
	}
	payloadA := encA.LastQEXTPayload()
	if len(payloadA) == 0 {
		t.Fatal("EncodeFrame(qext A) produced empty retained payload")
	}

	encB := NewEncoder(1)
	encB.SetQEXTEnabled(true)
	encB.SetBitrate(256000)

	packetB, err := encB.EncodeFrame(pcm, 960)
	if err != nil {
		t.Fatalf("EncodeFrame(qext B) failed: %v", err)
	}
	if !bytes.Equal(packetA, packetB) {
		t.Fatalf("main packet mismatch:\nA=%x\nB=%x", packetA, packetB)
	}
	payloadB := encB.LastQEXTPayload()
	if !bytes.Equal(payloadA, payloadB) {
		t.Fatalf("retained qext payload mismatch:\nA=%x\nB=%x", payloadA, payloadB)
	}
}

func TestEncodeFrameRetainedQEXTPayloadCarriesHeader(t *testing.T) {
	for _, channels := range []int{1, 2} {
		channels := channels
		t.Run(fmt.Sprintf("%dch", channels), func(t *testing.T) {
			pcm := make([]float64, 960*channels)
			for i := 0; i < 960; i++ {
				pcm[i*channels] = 0.45
				if channels == 2 {
					pcm[i*channels+1] = 0.30
				}
			}

			enc := NewEncoder(channels)
			enc.SetQEXTEnabled(true)
			enc.SetBitrate(256000)

			if _, err := enc.EncodeFrame(pcm, 960); err != nil {
				t.Fatalf("EncodeFrame(qext) failed: %v", err)
			}
			payload := enc.LastQEXTPayload()
			if len(payload) == 0 {
				t.Fatal("EncodeFrame(qext) produced empty retained payload")
			}

			var dec rangecoding.Decoder
			dec.Init(payload)
			hdr := decodeQEXTHeader(&dec, channels, len(payload))
			if hdr.EndBands != 2 {
				t.Fatalf("EndBands=%d want 2", hdr.EndBands)
			}
			if channels == 2 && hdr.Intensity != hdr.EndBands {
				t.Fatalf("Intensity=%d want %d", hdr.Intensity, hdr.EndBands)
			}
		})
	}
}

func TestEncodeFrameClearsQEXTPayloadWhenDisabled(t *testing.T) {
	pcm := generateSineWave(440.0, 960)

	enc := NewEncoder(1)
	enc.SetQEXTEnabled(true)
	enc.SetBitrate(256000)

	if _, err := enc.EncodeFrame(pcm, 960); err != nil {
		t.Fatalf("EncodeFrame(qext on) failed: %v", err)
	}
	if len(enc.LastQEXTPayload()) == 0 {
		t.Fatal("EncodeFrame(qext on) produced empty retained payload")
	}

	enc.SetQEXTEnabled(false)
	if _, err := enc.EncodeFrame(pcm, 960); err != nil {
		t.Fatalf("EncodeFrame(qext off) failed: %v", err)
	}
	if payload := enc.LastQEXTPayload(); len(payload) != 0 {
		t.Fatalf("EncodeFrame(qext off) retained stale payload: %x", payload)
	}
}

func intSliceEqual(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}
