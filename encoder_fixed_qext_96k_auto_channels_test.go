//go:build gopus_fixed_point && gopus_qext

package gopus

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestPublicFixedQEXT96kAutoChannelTransitionsMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		frameSize     = 1920
		maxPacket     = 4000
		complexity    = 10
		configuredLSB = 24
	)
	bitrates := []int{15000, 25000, 14000, 17000, 23000}
	pcm := makeFixedQEXTInventoryPCM(96000, 2, frameSize, len(bitrates))
	frames := make([]libopustest.FixedQEXTAutoChannelFrame, len(bitrates))
	for i, bitrate := range bitrates {
		lo := i * frameSize * 2
		frames[i] = libopustest.FixedQEXTAutoChannelFrame{
			Bitrate: bitrate,
			PCM:     pcm[lo : lo+frameSize*2],
		}
	}
	for _, qext := range []bool{false, true} {
		t.Run(fmt.Sprintf("qext_%t", qext), func(t *testing.T) {
			want, err := libopustest.ProbeOpusEncodeFixedQEXTAutoChannelRecords(
				frameSize, maxPacket, complexity, configuredLSB, qext, frames)
			if err != nil {
				libopustest.HelperUnavailable(t, "fixed-QEXT 96 kHz auto-channel sequence", err)
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
				func() error { return enc.SetLSBDepth(configuredLSB) },
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
			for frame := range frames {
				if err := enc.SetBitrate(frames[frame].Bitrate); err != nil {
					t.Fatalf("frame %d SetBitrate: %v", frame, err)
				}
				n, err := enc.EncodeInt16(frames[frame].PCM, packet)
				if err != nil {
					t.Fatalf("frame %d EncodeInt16: %v", frame, err)
				}
				got := packet[:n]
				if want[frame].Status != 0 || n != len(want[frame].Packet) ||
					enc.FinalRange() != want[frame].FinalRange || !bytes.Equal(got, want[frame].Packet) {
					t.Fatalf("frame %d bitrate=%d packet{%s} range=%08x want=%08x",
						frame, frames[frame].Bitrate, fixedQEXTBytesDiff(got, want[frame].Packet),
						enc.FinalRange(), want[frame].FinalRange)
				}
			}

			// At these VBR rates, the source's 96 kHz audio thresholds select mono,
			// stereo, mono, mono, then stereo. Check C also exercises both hysteresis
			// directions before packet equality can pass vacuously.
			wantStereo := [...]bool{false, true, false, false, true}
			for frame, stereo := range wantStereo {
				if len(want[frame].Packet) == 0 || (want[frame].Packet[0]&0x04 != 0) != stereo {
					t.Fatalf("C frame %d bitrate=%d stereo TOC=%t, want %t",
						frame, frames[frame].Bitrate, len(want[frame].Packet) > 0 && want[frame].Packet[0]&0x04 != 0, stereo)
				}
			}
		})
	}
}

func TestPublicFixedQEXT96kAutoChannelTransitionStateMatchesCELT(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		frameSize     = 1920
		maxPacket     = 4000
		complexity    = 10
		configuredLSB = 24
	)
	bitrates := []int{15000, 25000, 14000, 17000, 23000}
	pcm := makeFixedQEXTInventoryPCM(96000, 2, frameSize, len(bitrates))
	frames := make([]libopustest.FixedQEXTAutoChannelFrame, len(bitrates))
	for i, bitrate := range bitrates {
		lo := i * frameSize * 2
		frames[i] = libopustest.FixedQEXTAutoChannelFrame{
			Bitrate: bitrate,
			PCM:     pcm[lo : lo+frameSize*2],
		}
	}

	for _, qext := range []bool{false, true} {
		t.Run(fmt.Sprintf("qext_%t", qext), func(t *testing.T) {
			cPublic, err := libopustest.ProbeOpusEncodeFixedQEXTAutoChannelRecords(
				frameSize, maxPacket, complexity, configuredLSB, qext, frames)
			if err != nil {
				libopustest.HelperUnavailable(t, "fixed-QEXT 96 kHz auto-channel sequence", err)
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
				func() error { return enc.SetLSBDepth(configuredLSB) },
				func() error { return enc.SetQEXT(qext) },
			} {
				if err := set(); err != nil {
					t.Fatal(err)
				}
			}

			goFrames := make([]libopustest.CELTFixedQ8Frame, len(frames))
			goStates := make([]libopustest.CELTFixedQ8EncoderState, len(frames))
			goPackets := make([][]byte, len(frames))
			packet := make([]byte, maxPacket)
			for frame := range frames {
				if err := enc.SetBitrate(frames[frame].Bitrate); err != nil {
					t.Fatalf("frame %d SetBitrate: %v", frame, err)
				}
				lo := frame * frameSize * 2
				n, err := enc.EncodeInt16(pcm[lo:lo+frameSize*2], packet)
				if err != nil {
					t.Fatalf("frame %d EncodeInt16: %v", frame, err)
				}
				goPackets[frame] = append([]byte(nil), packet[:n]...)
				input := enc.enc.LastFixedCELTInputQ8()
				bitrate, maxBytes, lsbDepth := enc.enc.LastFixedCELTControls()
				if len(input) != frameSize*2 || maxBytes < 2 || bitrate <= 0 || lsbDepth != 16 {
					t.Fatalf("frame %d raw-Q8 controls/input: samples=%d bitrate=%d maxBytes=%d LSB=%d",
						frame, len(input), bitrate, maxBytes, lsbDepth)
				}
				streamChannels := int32(1)
				if goPackets[frame][0]&0x04 != 0 {
					streamChannels = 2
				}
				goFrames[frame] = libopustest.CELTFixedQ8Frame{
					PCM:            append([]int32(nil), input...),
					MaxBytes:       maxBytes,
					StreamChannels: streamChannels,
					Bitrate:        int32(bitrate),
					Analysis:       fixedQEXTOracleAnalysis(enc.enc.LastFixedCELTAnalysis()),
					SetPrediction:  true,
					Prediction:     2,
				}
				goStates[frame] = fixedQEXTGoStateSnapshot(enc, 2, 96000)
			}

			cFrames := make([]libopustest.CELTFixedQ8Frame, len(frames))
			sameInput := make([]bool, len(frames))
			for frame := range frames {
				c := cPublic[frame]
				goFrame := goFrames[frame]
				if c.CELTCalls != 1 || c.CELTFrameSize != frameSize || len(c.CELTInputQ8) != frameSize*2 {
					t.Fatalf("frame %d selected C CELT trace shape: calls=%d frameSize=%d samples=%d",
						frame, c.CELTCalls, c.CELTFrameSize, len(c.CELTInputQ8))
				}
				goEffectiveBytes := goFrame.MaxBytes
				cEffectiveBytes := c.CELTMaxBytes
				if qext {
					goEffectiveBytes = min(goEffectiveBytes, 3825)
					cEffectiveBytes = min(cEffectiveBytes, 3825)
				} else {
					goEffectiveBytes = min(goEffectiveBytes, 1275)
					cEffectiveBytes = min(cEffectiveBytes, 1275)
				}
				if c.CELTStreamChannels != goFrame.StreamChannels || c.CELTBitrate != goFrame.Bitrate ||
					c.CELTLSBDepth != 16 || cEffectiveBytes != goEffectiveBytes {
					t.Errorf("frame %d selected C/Go CELT controls: C{channels=%d bitrate=%d lsb=%d max=%d} Go{channels=%d bitrate=%d lsb=16 max=%d}",
						frame, c.CELTStreamChannels, c.CELTBitrate, c.CELTLSBDepth, c.CELTMaxBytes,
						goFrame.StreamChannels, goFrame.Bitrate, goFrame.MaxBytes)
				}
				inputDiff := -1
				for i := range c.CELTInputQ8 {
					if c.CELTInputQ8[i] != goFrame.PCM[i] {
						inputDiff = i
						break
					}
				}
				if inputDiff >= 0 {
					raw16 := pcm[frame*frameSize*2+inputDiff]
					t.Errorf("frame %d public CELT Q8 input differs at sample %d: Go=%d C=%d input16=%d inputQ8=%d",
						frame, inputDiff, goFrame.PCM[inputDiff], c.CELTInputQ8[inputDiff], raw16, int32(raw16)<<8)
				}
				sameInput[frame] = inputDiff < 0
				cFrames[frame] = goFrame
				cFrames[frame].PCM = append([]int32(nil), c.CELTInputQ8...)
				cFrames[frame].MaxBytes = c.CELTMaxBytes
				cFrames[frame].StreamChannels = c.CELTStreamChannels
				cFrames[frame].Bitrate = c.CELTBitrate
			}

			cTrace, err := libopustest.ProbeCELTFixedQEXTQ8State(libopustest.CELTFixedQ8Params{
				SampleRate: 96000, Channels: 2, StreamChannels: 2, FrameSize: frameSize,
				Start: 0, End: 21, Bitrate: int(cFrames[0].Bitrate), Complexity: complexity,
				LSBDepth: 16, VBR: true, QEXTEnabled: qext, Frames: cFrames,
			})
			if err != nil {
				t.Fatalf("selected-C per-frame channel state replay: %v", err)
			}
			for frame := range frames {
				cPacket := cTrace[frame].Packet
				if cPublic[frame].Packet[0]&0x03 == 3 {
					cMain, cSide := fixedQEXTPacketParts(t, append([]byte{cPublic[frame].Packet[0]}, cPacket...))
					publicMain, publicSide := fixedQEXTPacketParts(t, cPublic[frame].Packet)
					if !bytes.Equal(cMain, publicMain) || !bytes.Equal(cSide, publicSide) {
						t.Errorf("frame %d raw C vs public C: main{%s} side{%s}", frame,
							fixedQEXTBytesDiff(cMain, publicMain), fixedQEXTBytesDiff(cSide, publicSide))
					}
				} else {
					if !bytes.Equal(cPacket, cPublic[frame].Packet[1:]) {
						t.Errorf("frame %d raw C vs public C: %s", frame,
							fixedQEXTBytesDiff(cPacket, cPublic[frame].Packet[1:]))
					}
				}
				if cFrames[frame].StreamChannels == goFrames[frame].StreamChannels &&
					cFrames[frame].Bitrate == goFrames[frame].Bitrate &&
					min(cFrames[frame].MaxBytes, 3825) == min(goFrames[frame].MaxBytes, 3825) &&
					sameInput[frame] {
					stateDiff := fixedQEXTStateDifference(cTrace[frame].State, goStates[frame])
					if cTrace[frame].FinalRange != goStates[frame].RNG || stateDiff != "equal" {
						t.Errorf("frame %d same-input raw state: range=%08x/%08x state{%s}",
							frame, goStates[frame].RNG, cTrace[frame].FinalRange, stateDiff)
					}
				}
			}
		})
	}
}
