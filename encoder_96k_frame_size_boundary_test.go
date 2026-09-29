//go:build gopus_qext

package gopus

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// Native frame validation precedes the wrapper's 48 kHz bookkeeping conversion.
// Rejected sizes leave both the wrapper and the core encoder history intact.
func TestNative96kEncoderFrameSizeBoundariesMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, channels := range []int{1, 2} {
		for _, size := range []int{240, 480, 960, 1920, 3840, 5760, 7680, 9600, 11520} {
			t.Run(fmt.Sprintf("ch%d/samples%d", channels, size), func(t *testing.T) {
				params := libopustest.EncodeDiffParams{
					SampleRate: 96000, Channels: channels,
					Application: libopustest.EncodeDiffApplicationAudio,
					Bitrate:     64000, Complexity: 0, Signal: libopustest.EncodeDiffSignalAuto,
					VBR: true, VBRConstraint: true, FrameSize: size, FrameCount: 2,
					PCM: make([]float32, 2*size*channels),
				}
				for i := range params.PCM {
					params.PCM[i] = float32((i*37)%8192-4096) / 32768
				}
				want, err := libopustest.ProbeEncodeDiff(params)
				if err != nil {
					libopustest.HelperUnavailable(t, "96 kHz frame boundary", err)
				}
				if len(want) != 2 {
					t.Fatalf("oracle frames=%d, want 2", len(want))
				}
				enc, err := NewEncoder(EncoderConfig{SampleRate: 96000, Channels: channels, Application: ApplicationAudio})
				if err != nil {
					t.Fatal(err)
				}
				for _, err := range []error{enc.SetBitrate(64000), enc.SetComplexity(0), enc.SetFrameSize(size)} {
					if err != nil {
						t.Fatal(err)
					}
				}
				enc.SetVBR(true)
				enc.SetVBRConstraint(true)
				out := make([]byte, 4000)
				for frame := range 2 {
					if frame == 1 {
						for _, invalid := range []int{size - 1, size + 1} {
							bad := params
							bad.FrameSize, bad.FrameCount = invalid, 1
							bad.PCM = make([]float32, invalid*channels)
							records, err := libopustest.ProbeEncodeDiff(bad)
							if err != nil {
								t.Fatal(err)
							}
							if len(records) != 1 || records[0].Ret != -1 {
								t.Fatalf("libopus frame size %d: records=%+v, want OPUS_BAD_ARG", invalid, records)
							}
							rng := enc.FinalRange()
							if err := enc.SetFrameSize(invalid); err != ErrInvalidFrameSize {
								t.Fatalf("SetFrameSize(%d)=%v, want ErrInvalidFrameSize", invalid, err)
							}
							if enc.FrameSize() != size || enc.FinalRange() != rng {
								t.Fatal("rejected frame size changes encoder state")
							}
						}
					}
					pcm := params.PCM[frame*size*channels : (frame+1)*size*channels]
					n, err := enc.Encode(pcm, out)
					if err != nil || n != want[frame].Ret || !bytes.Equal(out[:n], want[frame].Packet) || enc.FinalRange() != want[frame].FinalRange {
						t.Fatalf("frame%d: Go bytes=%d range=%08x error=%v; C bytes=%d range=%08x", frame, n, enc.FinalRange(), err, want[frame].Ret, want[frame].FinalRange)
					}
				}
			})
		}
	}
}
