//go:build gopus_qext && !gopus_fixed_point

package encoder

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/types"
)

func TestNativeHD96kFloatCELTInputMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		frameSize = 1920
		maxBytes  = 4000
	)
	bitrates := []int{15000, 25000, 14000, 17000, 23000}
	pcm := makeNativeHD96kFloatTestPCM(frameSize, len(bitrates))
	frames := make([]libopustest.QEXT96kAutoFrame, len(bitrates))
	for i, bitrate := range bitrates {
		lo := i * frameSize * 2
		frames[i] = libopustest.QEXT96kAutoFrame{Bitrate: bitrate, PCM: pcm[lo : lo+frameSize*2]}
	}
	for _, qext := range []bool{false, true} {
		t.Run(map[bool]string{false: "qext_off", true: "qext_on"}[qext], func(t *testing.T) {
			want, err := libopustest.ProbeQEXT96kAutoChannels(frameSize, maxBytes, 10, 24, qext, frames)
			if err != nil {
				libopustest.HelperUnavailable(t, "native 96 kHz float CELT input", err)
				return
			}
			e := NewEncoder(48000, 2)
			e.SetMode(ModeCELT)
			e.SetBandwidth(types.BandwidthFullband)
			e.SetBitrateMode(ModeVBR)
			e.SetComplexity(10)
			e.SetLSBDepth(24)
			e.SetQEXT(qext)
			packet := make([]byte, maxBytes)
			for frame := range frames {
				e.SetBitrate(bitrates[frame])
				n, err := e.EncodeNativeHD96k(frames[frame].PCM, frameSize, packet)
				if err != nil {
					t.Fatalf("frame %d encode: %v", frame, err)
				}
				ref := want[frame]
				if ref.CELTCalls != 1 || len(ref.CELTInput) != frameSize*2 {
					t.Fatalf("frame %d selected C CELT trace: calls=%d samples=%d", frame, ref.CELTCalls, len(ref.CELTInput))
				}
				wantMaxBytes := maxBytes - 1
				if !qext {
					wantMaxBytes = min(wantMaxBytes, 1275)
				}
				if ref.CELTStream != uint32(e.streamChannels) || ref.CELTBitrate != int32(e.celtEncoder.Bitrate()) ||
					ref.CELTLSBDepth != int32(e.celtEncoder.LSBDepth()) || int(ref.CELTMaxBytes) != wantMaxBytes {
					t.Fatalf("frame %d CELT controls Go{stream=%d bitrate=%d lsb=%d max=%d} C{stream=%d bitrate=%d lsb=%d max=%d}",
						frame, e.streamChannels, e.celtEncoder.Bitrate(), e.celtEncoder.LSBDepth(), wantMaxBytes,
						ref.CELTStream, ref.CELTBitrate, ref.CELTLSBDepth, ref.CELTMaxBytes)
				}
				got := e.scratchInputPCM[:frameSize*2]
				for i := range got {
					if math.Float32bits(got[i]) != math.Float32bits(ref.CELTInput[i]) {
						t.Fatalf("frame %d CELT input first diff sample=%d Go=%08x C=%08x values=%g/%g",
							frame, i, math.Float32bits(got[i]), math.Float32bits(ref.CELTInput[i]), got[i], ref.CELTInput[i])
					}
				}
				if n != len(ref.Packet) || e.FinalRange() != ref.FinalRange {
					t.Fatalf("frame %d packet/range len=%d/%d range=%08x/%08x",
						frame, n, len(ref.Packet), e.FinalRange(), ref.FinalRange)
				}
			}
		})
	}
}

func makeNativeHD96kFloatTestPCM(frameSize, frameCount int) []float32 {
	pcm := make([]float32, frameSize*2*frameCount)
	for frame := range frameCount {
		for i := range frameSize {
			low := 0.34 * math.Sin(2*math.Pi*6000*float64(i)/96000)
			high := 0.21 * math.Sin(2*math.Pi*30000*float64(i)/96000)
			for ch := range 2 {
				v := low + high*float64(ch+1)/2
				pcm[(frame*frameSize+i)*2+ch] = float32(int16(math.Round(v*32767))) / 32768
			}
		}
	}
	return pcm
}
