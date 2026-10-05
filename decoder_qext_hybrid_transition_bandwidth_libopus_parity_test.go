//go:build gopus_qext && !gopus_fixed_point

package gopus

import (
	"encoding/hex"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestQEXTHybridTransitionPLCUsesPreviousCELTBandwidth(t *testing.T) {
	libopustest.RequireOracle(t)

	// The sequence retains a CELT Fullband state through a long PLC and FEC
	// fallback before a 10 ms Hybrid SWB packet triggers recursive CELT PLC.
	// libopus applies the new end band after that recursive PLC frame.
	packet := func(encoded string) []byte {
		decoded, err := hex.DecodeString(encoded)
		if err != nil {
			t.Fatal(err)
		}
		return decoded
	}
	celtPacket := packet("e49efa537fb4e25b1cc490cabe23eaa3b73fd972543b0ce936b94abb777cf2f22fc700e6ad1b4c7d3e40d0ff0da354f36697")
	hybridPacket := packet("6083c9198b547c7503685baa52e07a356f46b0047917aee341febfb4e7122e25a263b34067906156af03471b0742430c39ddd6cf0e3a8292ea96a6e0")
	packets := [][]byte{celtPacket, nil, hybridPacket, hybridPacket}
	decodeFEC := []bool{false, true, true, false}
	wantStatus := []int32{240, 5760, 5760, 960}
	wantRanges := []uint32{0x1306a200, 0, 0, 0x0ad35400}

	for _, channels := range []int{1, 2} {
		t.Run(map[int]string{1: "mono", 2: "stereo"}[channels], func(t *testing.T) {
			ref, err := libopustest.ProbeQEXTDecodeFECSequence(libopustest.QEXTDecode96kParams{
				SampleFormat:           libopustest.QEXTDecode96kFormatFloat32,
				Channels:               channels,
				SampleRate:             96000,
				PhaseInversionDisabled: false,
				MaxFrameSize:           5760,
				Packets:                packets,
				DecodeFEC:              decodeFEC,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "native 96 kHz QEXT FEC sequence", err)
			}
			if len(ref.Status) != len(wantStatus) || len(ref.FinalRanges) != len(wantRanges) {
				t.Fatalf("libopus returned %d statuses and %d ranges", len(ref.Status), len(ref.FinalRanges))
			}
			for i := range wantStatus {
				if ref.Status[i] != wantStatus[i] || ref.FinalRanges[i] != wantRanges[i] {
					t.Fatalf("libopus step %d=(status %d, range %08x), want (%d, %08x)",
						i, ref.Status[i], ref.FinalRanges[i], wantStatus[i], wantRanges[i])
				}
			}

			cfg := DefaultDecoderConfig(96000, channels)
			cfg.MaxPacketBytes = 8192
			dec, err := NewDecoder(cfg)
			if err != nil {
				t.Fatal(err)
			}
			dec.SetPhaseInversionDisabled(false)
			out := make([]float32, 5760*channels)
			offset := 0
			for i, packet := range packets {
				var n int
				if decodeFEC[i] {
					n, err = dec.DecodeWithFEC(packet, out, true)
				} else {
					n, err = dec.Decode(packet, out)
				}
				if err != nil || int32(n) != wantStatus[i] || int32(n) != ref.Status[i] {
					t.Fatalf("step %d Go=(%d,%v), C=%d", i, n, err, ref.Status[i])
				}
				if got := dec.FinalRange(); got != wantRanges[i] || got != ref.FinalRanges[i] {
					t.Fatalf("step %d final range=%08x, C=%08x", i, got, ref.FinalRanges[i])
				}
				count := n * channels
				if offset+count > len(ref.PCM) {
					t.Fatalf("step %d consumes past C PCM: offset=%d count=%d length=%d", i, offset, count, len(ref.PCM))
				}
				for j := 0; j < count; j++ {
					if got, want := math.Float32bits(out[j]), math.Float32bits(ref.PCM[offset+j]); got != want {
						t.Fatalf("step %d float32[%d]=%08x, C=%08x", i, j, got, want)
					}
				}
				offset += count
			}
			if offset != len(ref.PCM) {
				t.Fatalf("consumed %d C PCM samples, returned %d", offset, len(ref.PCM))
			}
		})
	}
}
