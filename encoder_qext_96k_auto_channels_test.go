//go:build gopus_qext && !gopus_fixed_point

package gopus

import (
	"bytes"
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestQEXT96kAutoChannelTransitionsMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		frameSize  = 1920
		maxPacket  = 4000
		complexity = 10
		lsbDepth   = 24
	)
	bitrates := []int{15000, 25000, 14000, 17000, 23000}
	pcm := makeQEXT96kAutoPCM(frameSize, len(bitrates))
	frames := make([]libopustest.QEXT96kAutoFrame, len(bitrates))
	for i, bitrate := range bitrates {
		lo := i * frameSize * 2
		frames[i] = libopustest.QEXT96kAutoFrame{Bitrate: bitrate, PCM: pcm[lo : lo+frameSize*2]}
	}
	for _, qext := range []bool{false, true} {
		t.Run(fmt.Sprintf("qext_%t", qext), func(t *testing.T) {
			want, err := libopustest.ProbeQEXT96kAutoChannels(frameSize, maxPacket, complexity, lsbDepth, qext, frames)
			if err != nil {
				libopustest.HelperUnavailable(t, "QEXT 96 kHz auto-channel sequence", err)
				return
			}
			enc, err := NewEncoder(EncoderConfig{SampleRate: 96000, Channels: 2, Application: ApplicationAudio})
			if err != nil {
				t.Fatal(err)
			}
			for _, set := range []func() error{
				func() error { return enc.SetMode(EncoderModeCELT) },
				func() error { return enc.SetBandwidth(BandwidthFullband) },
				func() error { return enc.SetMaxBandwidth(BandwidthFullband) },
				func() error { return enc.SetFrameSize(frameSize) },
				func() error { return enc.SetComplexity(complexity) },
				func() error { return enc.SetBitrateMode(BitrateModeVBR) },
				func() error { return enc.SetLSBDepth(lsbDepth) },
				func() error { return enc.SetQEXT(qext) },
			} {
				if err := set(); err != nil {
					t.Fatal(err)
				}
			}
			if enc.ForceChannels() != -1 {
				t.Fatalf("force channels=%d, want auto (-1)", enc.ForceChannels())
			}
			packet := make([]byte, maxPacket)
			wantStereo := [...]bool{false, true, false, false, true}
			for frame := range frames {
				if err := enc.SetBitrate(frames[frame].Bitrate); err != nil {
					t.Fatalf("frame %d SetBitrate: %v", frame, err)
				}
				n, err := enc.Encode(pcm[frame*frameSize*2:(frame+1)*frameSize*2], packet)
				if err != nil {
					t.Fatalf("frame %d Encode: %v", frame, err)
				}
				got := packet[:n]
				if want[frame].Status != 0 || n != len(want[frame].Packet) ||
					enc.FinalRange() != want[frame].FinalRange || !bytes.Equal(got, want[frame].Packet) {
					t.Fatalf("frame %d bitrate=%d packet diff=%s range=%08x/%08x",
						frame, bitrates[frame], qext96AutoPacketDiff(got, want[frame].Packet), enc.FinalRange(), want[frame].FinalRange)
				}
				if len(want[frame].Packet) == 0 || (want[frame].Packet[0]&0x04 != 0) != wantStereo[frame] {
					t.Fatalf("C frame %d bitrate=%d stereo TOC=%t, want %t", frame, bitrates[frame],
						len(want[frame].Packet) > 0 && want[frame].Packet[0]&0x04 != 0, wantStereo[frame])
				}
				channels := uint32(1)
				if wantStereo[frame] {
					channels = 2
				}
				if want[frame].CELTCalls != 1 || want[frame].CELTFrameSize != frameSize ||
					want[frame].CELTStream != channels || want[frame].CELTBitrate != int32(bitrates[frame]) ||
					want[frame].CELTLSBDepth != lsbDepth || want[frame].CELTMaxBytes < 2 ||
					len(want[frame].CELTInput) != frameSize*2 {
					t.Fatalf("C frame %d CELT trace: calls=%d size=%d channels=%d bitrate=%d lsb=%d max=%d samples=%d",
						frame, want[frame].CELTCalls, want[frame].CELTFrameSize, want[frame].CELTStream,
						want[frame].CELTBitrate, want[frame].CELTLSBDepth, want[frame].CELTMaxBytes, len(want[frame].CELTInput))
				}
			}
		})
	}
}

func qext96AutoPacketDiff(got, want []byte) string {
	limit := min(len(got), len(want))
	diff := limit
	for i := range limit {
		if got[i] != want[i] {
			diff = i
			break
		}
	}
	return fmt.Sprintf("len=%d/%d first=%d", len(got), len(want), diff)
}

func makeQEXT96kAutoPCM(frameSize, frameCount int) []float32 {
	pcm := make([]float32, frameSize*2*frameCount)
	for frame := range frameCount {
		for i := range frameSize {
			low := 0.34 * math.Sin(2*math.Pi*6000*float64(i)/96000)
			high := 0.21 * math.Sin(2*math.Pi*30000*float64(i)/96000)
			for ch := range 2 {
				v := low + high*float64(ch+1)/2
				sample := int16(math.Round(v * 32767))
				pcm[(frame*frameSize+i)*2+ch] = float32(sample) / 32768
			}
		}
	}
	return pcm
}
