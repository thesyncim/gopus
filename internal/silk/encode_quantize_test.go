package silk

import "testing"

// TestEncodeQuantizesInputPCM checks that the float input reaches the SILK
// analysis quantized to int16 (RES2INT16 in silk_Encode, then
// silk_short2float_array in silk_encode_frame_FLP).
func TestEncodeQuantizesInputPCM(t *testing.T) {
	if silkFixedEncodeBuild {
		t.Skip("inspects the float x_buf; the FIXED_POINT encode path maintains a separate int16 x_buf")
	}
	config := GetBandwidthConfig(BandwidthWideband)
	frameSamples := config.SampleRate * 20 / 1000

	pcm := make([]float32, frameSamples)
	for i := range pcm {
		pcm[i] = 0.1 // 3276.8 in int16 scale: rounds to 3277
	}
	p := newTestPacketEncoder(BandwidthWideband, 1)
	p.encode(t, pcm)

	// After the frame, x_buf has shifted by one frame: the frame occupies
	// [la_shape+ltp_mem-frame_length, la_shape+ltp_mem). Its first samples are
	// the resampler delay and the one-sample mono buffering; index 100 is past
	// them and not one of the eight anti-denormal positions.
	fsKHz := config.SampleRate / 1000
	frameStart := (ltpMemLengthMs+laShapeMs)*fsKHz - frameSamples
	got := p.enc.state[0].xBuf[frameStart+100]
	want := float32(3277) * (1.0 / silkSampleScale)
	if got != want {
		t.Fatalf("x_buf sample = %v, want %v (RES2INT16(0.1) = 3277)", got, want)
	}
	if in := p.enc.state[0].inputBuf[2+100]; in != 3277 {
		t.Fatalf("inputBuf sample = %d, want 3277", in)
	}
}
