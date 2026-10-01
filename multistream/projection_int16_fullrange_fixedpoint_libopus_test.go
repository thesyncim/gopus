//go:build gopus_fixed_point

package multistream

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/libopustest"
)

// TestProjectionInt16FullRangeMatchesLibopus keeps the fixed projection mix
// above normal sample range, where a float32-only bridge can clip the exact
// opus_res produced by mapping_matrix_multiply_channel_in_short.
func TestProjectionInt16FullRangeMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	rates := []int{48000}
	if extsupport.QEXT {
		rates = append(rates, 96000)
	}
	const baseFrameSize, frameCount = 960, 6
	for _, rate := range rates {
		frameSize := baseFrameSize * rate / 48000
		for _, channels := range []int{9, 16} {
			t.Run(fmt.Sprintf("rate%d_ch%d", rate, channels), func(t *testing.T) {
				enc, err := NewProjectionEncoder(rate, channels)
				if err != nil {
					t.Fatal(err)
				}
				bitrate := 256000
				if channels == 16 {
					bitrate = 384000
				}
				pcm := make([]int16, frameSize*frameCount*channels)
				for sample := range frameSize * frameCount {
					polarity := (sample / 120) % 2
					for col := range channels {
						value := int32(32767)
						if enc.projectionMixing[col*channels] < 0 {
							value = -32768
						}
						if polarity != 0 {
							if value < 0 {
								value = 32767
							} else {
								value = -32768
							}
						}
						pcm[sample*channels+col] = int16(value)
					}
				}
				ref, err := encodeLibopusProjection(rate, channels, 2049, bitrate, true, true,
					10, -1000, frameSize, frameCount, 4000, 1, nil, pcm)
				if err != nil {
					t.Fatal(err)
				}
				enc.SetBitrate(bitrate)
				enc.SetVBR(true)
				enc.SetVBRConstraint(true)
				enc.SetComplexity(10)
				enc.SetBandwidthAuto()
				out := make([]byte, 4000)
				for frame := range frameCount {
					input := pcm[frame*frameSize*channels : (frame+1)*frameSize*channels]
					n, err := enc.EncodeInt16WithAnalysis(input, frameSize, input, out)
					if err != nil {
						t.Fatal(err)
					}
					if !bytes.Equal(out[:n], ref.packets[frame]) || enc.GetFinalRange() != ref.ranges[frame] {
						gotStreams, _ := parseMultistreamPacket(out[:n], enc.Streams())
						wantStreams, _ := parseMultistreamPacket(ref.packets[frame], ref.streams)
						for stream := range min(len(gotStreams), len(wantStreams)) {
							if !bytes.Equal(gotStreams[stream], wantStreams[stream]) {
								first := firstByteMismatch(gotStreams[stream], wantStreams[stream])
								end := min(first+16, min(len(gotStreams[stream]), len(wantStreams[stream])))
								t.Logf("stream %d Go=%x C=%x", stream, gotStreams[stream][first:end], wantStreams[stream][first:end])
								t.Logf("stream %d firstByte=%d lengths=%d/%d configs=%d/%d", stream,
									firstByteMismatch(gotStreams[stream], wantStreams[stream]),
									len(gotStreams[stream]), len(wantStreams[stream]),
									gotStreams[stream][0]>>3, wantStreams[stream][0]>>3)
							}
						}
						t.Fatalf("frame %d byte=%d lengths=%d/%d ranges=%08x/%08x", frame,
							firstByteMismatch(out[:n], ref.packets[frame]), n, len(ref.packets[frame]),
							enc.GetFinalRange(), ref.ranges[frame])
					}
				}
			})
		}
	}
}
