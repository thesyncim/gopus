//go:build gopus_dred || gopus_osce

package silk

import "testing"

func TestDecoderStateSnapshotPreservesIntegerLowBits(t *testing.T) {
	d := NewDecoder()
	d.SetAPISampleRate(48000)
	const value int32 = 1<<25 + 1
	if float32(value) != float32(value-1) {
		t.Fatal("test values must share a float32 representation")
	}
	d.state[0].sLPCQ14Buf[0] = value
	d.state[0].excQ14[0] = -value
	d.GetResampler(BandwidthWideband).sIIR[0] = value
	s := d.SnapshotDecoderState(BandwidthWideband, 0)
	if s.SLPCQ14[0] != value || s.ExcQ14[0] != -value || s.ResamplerIIR[0] != value {
		t.Fatalf("snapshot loses integer bits: LPC=%d excitation=%d IIR=%d", s.SLPCQ14[0], s.ExcQ14[0], s.ResamplerIIR[0])
	}
	d.state[0].sLPCQ14Buf[0]--
	d.state[0].excQ14[0]++
	d.GetResampler(BandwidthWideband).sIIR[0]--
	next := d.SnapshotDecoderState(BandwidthWideband, 0)
	if s.SLPCQ14 == next.SLPCQ14 || s.ExcQ14 == next.ExcQ14 || s.ResamplerIIR == next.ResamplerIIR {
		t.Fatal("snapshot masks a one-bit integer-state mutation")
	}
}
