package silk

import (
	"testing"

	"github.com/thesyncim/gopus/rangecoding"
)

func TestBandwidthConfig(t *testing.T) {
	tests := []struct {
		bw       Bandwidth
		wantLPC  int
		wantRate int
		wantMin  int
		wantMax  int
	}{
		{BandwidthNarrowband, 10, 8000, 16, 144},
		{BandwidthMediumband, 10, 12000, 24, 216},
		{BandwidthWideband, 16, 16000, 32, 288},
	}
	for _, tt := range tests {
		cfg := GetBandwidthConfig(tt.bw)
		if cfg.LPCOrder != tt.wantLPC {
			t.Errorf("LPCOrder for %v: got %d, want %d", tt.bw, cfg.LPCOrder, tt.wantLPC)
		}
		if cfg.SampleRate != tt.wantRate {
			t.Errorf("SampleRate for %v: got %d, want %d", tt.bw, cfg.SampleRate, tt.wantRate)
		}
		if cfg.PitchLagMin != tt.wantMin {
			t.Errorf("PitchLagMin for %v: got %d, want %d", tt.bw, cfg.PitchLagMin, tt.wantMin)
		}
		if cfg.PitchLagMax != tt.wantMax {
			t.Errorf("PitchLagMax for %v: got %d, want %d", tt.bw, cfg.PitchLagMax, tt.wantMax)
		}
	}
}

func TestFrameTypeInactive(t *testing.T) {
	// Encode an inactive frame type (typeOffset=0) and decode it.
	var enc rangecoding.Encoder
	buf := make([]byte, 32)
	enc.Init(buf)
	enc.EncodeICDF16(0, ICDFFrameTypeVADInactive, 8)
	raw := enc.Done()

	d := NewDecoder()
	rd := &rangecoding.Decoder{}
	rd.Init(raw)
	d.SetRangeDecoder(rd)
	sig, quant := d.DecodeFrameType(false)
	if sig != 0 || quant != 0 {
		t.Errorf("Inactive frame: got (%d, %d), want (0, 0)", sig, quant)
	}
}

func TestFrameTypeInactiveHighOffsetRoundtrip(t *testing.T) {
	e := NewEncoder(BandwidthWideband)
	var enc rangecoding.Encoder
	buf := make([]byte, 32)
	enc.Init(buf)
	e.SetRangeEncoder(&enc)

	// VAD inactive with typeOffset=1 (signal=0, quantOffset=1).
	e.encodeFrameType(false, typeNoVoiceActivity, 1)
	raw := enc.Done()

	d := NewDecoder()
	rd := &rangecoding.Decoder{}
	rd.Init(raw)
	d.SetRangeDecoder(rd)
	sig, quant := d.DecodeFrameType(false)
	if sig != typeNoVoiceActivity || quant != 1 {
		t.Errorf("Inactive frame typeOffset=1: got (%d, %d), want (%d, %d)", sig, quant, typeNoVoiceActivity, 1)
	}
}

func TestFrameTypeActiveVoicedHighOffsetRoundtrip(t *testing.T) {
	e := NewEncoder(BandwidthWideband)
	var enc rangecoding.Encoder
	buf := make([]byte, 32)
	enc.Init(buf)
	e.SetRangeEncoder(&enc)

	// VAD active with typeOffset=5 (signal=2, quantOffset=1).
	e.encodeFrameType(true, typeVoiced, 1)
	raw := enc.Done()

	d := NewDecoder()
	rd := &rangecoding.Decoder{}
	rd.Init(raw)
	d.SetRangeDecoder(rd)
	sig, quant := d.DecodeFrameType(true)
	if sig != typeVoiced || quant != 1 {
		t.Errorf("Active frame typeOffset=5: got (%d, %d), want (%d, %d)", sig, quant, typeVoiced, 1)
	}
}

func TestFrameParamsStruct(t *testing.T) {
	// Verify FrameParams can be instantiated with expected fields
	params := FrameParams{
		SignalType:     2, // Voiced
		QuantOffset:    1, // High
		NumSubframes:   4,
		Gains:          make([]int32, 4),
		LPCOrder:       10,
		LPCCoeffs:      make([]int16, 10),
		PitchLags:      make([]int, 4),
		LTPCoeffs:      make([][]int8, 4),
		LTPPeriodicity: 1,
	}

	if params.SignalType != 2 {
		t.Errorf("SignalType: got %d, want 2", params.SignalType)
	}
	if params.NumSubframes != 4 {
		t.Errorf("NumSubframes: got %d, want 4", params.NumSubframes)
	}
	if params.LPCOrder != 10 {
		t.Errorf("LPCOrder: got %d, want 10", params.LPCOrder)
	}
}

func TestGainDequantTableRange(t *testing.T) {
	// Verify GainDequantTable has 64 entries and values are in increasing order
	if len(GainDequantTable) != 64 {
		t.Errorf("GainDequantTable length: got %d, want 64", len(GainDequantTable))
	}

	// Values should be monotonically increasing
	for i := 1; i < len(GainDequantTable); i++ {
		if GainDequantTable[i] <= GainDequantTable[i-1] {
			t.Errorf("GainDequantTable not increasing at index %d: %d <= %d",
				i, GainDequantTable[i], GainDequantTable[i-1])
		}
	}

	// First value should be around 81920 (Q16 gain ~1.25), last around 1.68 billion (Q16 gain ~25700)
	// These are Q16 values, so 65536 = gain of 1.0
	if GainDequantTable[0] < 75000 || GainDequantTable[0] > 90000 {
		t.Errorf("GainDequantTable[0] unexpected: %d (expected ~81920)", GainDequantTable[0])
	}
	if GainDequantTable[63] < 1500000000 {
		t.Errorf("GainDequantTable[63] too small: %d (expected ~1686110208)", GainDequantTable[63])
	}
}

func TestLSFMinSpacingLength(t *testing.T) {
	// Verify minimum spacing tables have correct length
	// NB/MB: 10 coefficients + 1 = 11
	if len(LSFMinSpacingNBMB) != 11 {
		t.Errorf("LSFMinSpacingNBMB length: got %d, want 11", len(LSFMinSpacingNBMB))
	}

	// WB: 16 coefficients + 1 = 17
	if len(LSFMinSpacingWB) != 17 {
		t.Errorf("LSFMinSpacingWB length: got %d, want 17", len(LSFMinSpacingWB))
	}
}

func TestCosineTableRange(t *testing.T) {
	// CosineTable should have 129 entries for [0, pi]
	if len(CosineTable) != 129 {
		t.Errorf("CosineTable length: got %d, want 129", len(CosineTable))
	}

	// cos(0) = 1.0 = 4096 in Q12
	if CosineTable[0] != 4096 {
		t.Errorf("CosineTable[0]: got %d, want 4096", CosineTable[0])
	}

	// cos(pi) = -1.0 = -4096 in Q12
	if CosineTable[128] != -4096 {
		t.Errorf("CosineTable[128]: got %d, want -4096", CosineTable[128])
	}

	// cos(pi/2) = 0
	if CosineTable[64] != 0 {
		t.Errorf("CosineTable[64]: got %d, want 0", CosineTable[64])
	}
}

func TestLTPFilterCodebookSizes(t *testing.T) {
	// Verify LTP filter codebook sizes
	if len(LTPFilterLow) != 8 {
		t.Errorf("LTPFilterLow length: got %d, want 8", len(LTPFilterLow))
	}
	if len(LTPFilterMid) != 16 {
		t.Errorf("LTPFilterMid length: got %d, want 16", len(LTPFilterMid))
	}
	if len(LTPFilterHigh) != 32 {
		t.Errorf("LTPFilterHigh length: got %d, want 32", len(LTPFilterHigh))
	}

	// Each entry should have 5 taps
	for i, taps := range LTPFilterLow {
		if len(taps) != 5 {
			t.Errorf("LTPFilterLow[%d] taps: got %d, want 5", i, len(taps))
		}
	}
}

func TestPitchContourSizes(t *testing.T) {
	// Verify pitch contour table sizes
	if len(PitchContourNB10ms) != 16 {
		t.Errorf("PitchContourNB10ms length: got %d, want 16", len(PitchContourNB10ms))
	}
	if len(PitchContourNB20ms) != 16 {
		t.Errorf("PitchContourNB20ms length: got %d, want 16", len(PitchContourNB20ms))
	}

	// 10ms has 2 subframes, 20ms has 4 subframes
	if len(PitchContourNB10ms[0]) != 2 {
		t.Errorf("PitchContourNB10ms[0] subframes: got %d, want 2", len(PitchContourNB10ms[0]))
	}
	if len(PitchContourNB20ms[0]) != 4 {
		t.Errorf("PitchContourNB20ms[0] subframes: got %d, want 4", len(PitchContourNB20ms[0]))
	}
}

func TestStabilizeLSF(t *testing.T) {
	// Test that stabilizeLSF enforces minimum spacing
	lsf := []int16{100, 100, 100, 100, 100, 100, 100, 100, 100, 100}

	// NB/MB: 10 coefficients
	stabilizeLSF(lsf, false)

	// After stabilization, values should be strictly increasing
	for i := 1; i < len(lsf); i++ {
		if lsf[i] <= lsf[i-1] {
			t.Errorf("LSF not increasing after stabilize at index %d: %d <= %d",
				i, lsf[i], lsf[i-1])
		}
	}

	// Values should be in valid Q15 range
	for i, v := range lsf {
		if v < 0 || v > 32767 {
			t.Errorf("LSF[%d] out of Q15 range: %d", i, v)
		}
	}
}

func TestDecoderState(t *testing.T) {
	d := NewDecoder()

	// Initial state should be clean
	if d.HaveDecoded() {
		t.Error("New decoder should not have decoded")
	}
	if d.PreviousLogGain() != 0 {
		t.Errorf("Initial previousLogGain: got %d, want 0", d.PreviousLogGain())
	}

	// Test state updates
	d.MarkDecoded()
	if !d.HaveDecoded() {
		t.Error("Decoder should be marked as decoded")
	}

	d.SetPreviousLogGain(42)
	if d.PreviousLogGain() != 42 {
		t.Errorf("previousLogGain: got %d, want 42", d.PreviousLogGain())
	}

	// Test reset
	d.Reset()
	if d.HaveDecoded() {
		t.Error("Reset decoder should not have decoded")
	}
	if d.PreviousLogGain() != 0 {
		t.Errorf("Reset previousLogGain: got %d, want 0", d.PreviousLogGain())
	}
}
