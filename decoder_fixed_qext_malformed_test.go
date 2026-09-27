//go:build gopus_fixed_point && gopus_qext

package gopus_test

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/benchutil"
	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestPublicFixedQEXTStructuralMalformedPacketPreservesCELTState(t *testing.T) {
	opusDemo, err := benchutil.QEXTOpusDemoPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "QEXT-enabled opus_demo", err)
		return
	}
	const channels = 1
	const sampleRate = 48000
	const frameSize = 960
	packets := encodeNative48kQEXTPackets(t, opusDemo, channels, 2)
	// CELT config 16, code-3 CBR with M=3 and five payload bytes. The parser
	// rejects the uneven frame split before entering CELT decode.
	bad := []byte{0x83, 3, 1, 2, 3, 4, 5}
	sequence := [][]byte{packets[0], bad, packets[1]}

	for _, format := range []uint32{
		libopustest.QEXTDecode96kFormatFloat32,
		libopustest.QEXTDecode96kFormatInt16,
		libopustest.QEXTDecode96kFormatInt24,
	} {
		t.Run(map[uint32]string{
			libopustest.QEXTDecode96kFormatFloat32: "float32",
			libopustest.QEXTDecode96kFormatInt16:   "int16",
			libopustest.QEXTDecode96kFormatInt24:   "int24",
		}[format], func(t *testing.T) {
			want, err := libopustest.ProbeQEXTDecodeFixedSequence(libopustest.QEXTDecode96kParams{
				SampleFormat: format,
				Channels:     channels,
				SampleRate:   sampleRate,
				MaxFrameSize: frameSize,
				Packets:      sequence,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "selected fixed-QEXT decode sequence", err)
				return
			}
			if len(want.Status) != len(sequence) || want.Status[0] != frameSize || want.Status[1] != -4 || want.Status[2] != frameSize {
				t.Fatalf("selected C sequence statuses=%v, want [%d -4 %d]", want.Status, frameSize, frameSize)
			}
			cfg := gopus.DefaultDecoderConfig(sampleRate, channels)
			dec, err := gopus.NewDecoder(cfg)
			if err != nil {
				t.Fatal(err)
			}
			f32 := make([]float32, frameSize*channels)
			i16 := make([]int16, len(f32))
			i24 := make([]int32, len(f32))
			decode := func(packet []byte) (int, error) {
				switch format {
				case libopustest.QEXTDecode96kFormatInt16:
					return dec.DecodeInt16(packet, i16)
				case libopustest.QEXTDecode96kFormatInt24:
					return dec.DecodeInt24(packet, i24)
				default:
					return dec.Decode(packet, f32)
				}
			}
			compareFrame := func(frame int) {
				for i := 0; i < frameSize*channels; i++ {
					switch format {
					case libopustest.QEXTDecode96kFormatInt16:
						if got, expected := i16[i], want.Int16[i+frame*frameSize*channels]; got != expected {
							t.Fatalf("frame %d int16[%d]=%d C=%d", frame, i, got, expected)
						}
					case libopustest.QEXTDecode96kFormatInt24:
						if got, expected := i24[i], want.Int24[i+frame*frameSize*channels]; got != expected {
							t.Fatalf("frame %d int24[%d]=%d C=%d", frame, i, got, expected)
						}
					default:
						if got, expected := math.Float32bits(f32[i]), math.Float32bits(want.PCM[frame*frameSize*channels+i]); got != expected {
							t.Fatalf("frame %d float32[%d]=%08x C=%08x", frame, i, got, expected)
						}
					}
				}
			}

			if n, err := decode(packets[0]); err != nil || n != frameSize {
				t.Fatalf("first received frame samples=%d err=%v", n, err)
			}
			if got, expected := dec.FinalRange(), want.FinalRanges[0]; got != expected {
				t.Fatalf("first range=%08x C=%08x", got, expected)
			}
			compareFrame(0)
			if n, err := decode(bad); n != 0 || err != gopus.ErrInvalidPacket {
				t.Fatalf("malformed code-3 packet samples=%d err=%v, want 0/%v", n, err, gopus.ErrInvalidPacket)
			}
			if got, expected := dec.FinalRange(), want.FinalRanges[1]; got != expected {
				t.Fatalf("malformed packet changed range=%08x, want prior %08x", got, expected)
			}
			if n, err := decode(packets[1]); err != nil || n != frameSize {
				t.Fatalf("received frame after malformed packet samples=%d err=%v", n, err)
			}
			if got, expected := dec.FinalRange(), want.FinalRanges[2]; got != expected {
				t.Fatalf("resumed range=%08x C=%08x", got, expected)
			}
			compareFrame(1)
		})
	}
}
