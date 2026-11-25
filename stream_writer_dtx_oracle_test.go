//go:build !gopus_fixed_point

package gopus

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"github.com/thesyncim/gopus/internal/libopustest"
	"math"
	"testing"
)

func TestWriter_DTX(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		sampleRate    = 48000
		channels      = 2
		frameSize     = 960
		speechFrames  = 10
		silenceFrames = 30 // 600 ms exceeds the encoder's 200 ms DTX threshold.
		resumeFrames  = 10
	)
	frameCount := speechFrames + silenceFrames + resumeFrames
	frameSamples := frameSize * channels
	pcm := make([]float32, frameCount*frameSamples)
	silenceEnd := speechFrames + silenceFrames
	for frame := range frameCount {
		if frame >= speechFrames && frame < silenceEnd {
			continue
		}
		for sample := range frameSize {
			index := frame*frameSize + sample
			value := float32(0.5 * math.Sin(2*math.Pi*440*float64(index)/sampleRate))
			for channel := range channels {
				pcm[(frame*frameSize+sample)*channels+channel] = value
			}
		}
	}

	sink := &slicePacketSink{}
	w, err := NewWriter(sampleRate, channels, sink, FormatFloat32LE, ApplicationVoIP)
	if err != nil {
		t.Fatalf("NewWriter: %v", err)
	}
	if got := w.enc.FrameSize(); got != frameSize {
		t.Fatalf("default frame size = %d, want %d", got, frameSize)
	}
	w.SetDTX(true)
	if !w.enc.DTXEnabled() {
		t.Fatal("DTX is not enabled")
	}

	// ProbeEncodeDiff creates the same 48 kHz stereo VoIP encoder and applies
	// the Writer's actual controls. Zero bandwidth/mode/channel selectors leave
	// those controls at their matching constructor defaults.
	records, err := libopustest.ProbeEncodeDiff(libopustest.EncodeDiffParams{
		SampleRate:         sampleRate,
		Channels:           channels,
		Application:        libopustest.EncodeDiffApplicationVoIP,
		Bitrate:            w.enc.Bitrate(),
		Complexity:         w.enc.Complexity(),
		Signal:             uint32(w.enc.Signal()),
		VBR:                w.enc.VBR(),
		VBRConstraint:      w.enc.VBRConstraint(),
		InbandFEC:          w.enc.InBandFEC(),
		PacketLoss:         w.enc.PacketLoss(),
		DTX:                w.enc.DTXEnabled(),
		LSBDepth:           w.enc.LSBDepth(),
		PredictionDisabled: w.enc.PredictionDisabled(),
		PhaseInvDisabled:   w.enc.PhaseInversionDisabled(),
		FrameSize:          frameSize,
		FrameCount:         frameCount,
		PCM:                pcm,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "encode diff oracle", err)
		return
	}

	frameBytes := make([]byte, frameSamples*4)
	packetIndex := 0
	enteredDTX, sawOneByteDTXPacket, resumedSpeech := false, false, false
	for frame, ref := range records {
		if ref.Ret < 0 {
			t.Fatalf("libopus frame %d encode returned %d", frame, ref.Ret)
		}
		framePCM := pcm[frame*frameSamples : (frame+1)*frameSamples]
		for i, value := range framePCM {
			binary.LittleEndian.PutUint32(frameBytes[i*4:], math.Float32bits(value))
		}
		packetsBefore := len(sink.packets)
		n, err := w.Write(frameBytes)
		if err != nil || n != len(frameBytes) {
			t.Fatalf("Writer.Write frame %d = (%d, %v), want (%d, nil)", frame, n, err, len(frameBytes))
		}
		if ref.Ret > 0 {
			if len(sink.packets) != packetsBefore+1 {
				t.Fatalf("frame %d emitted %d packets, libopus returned a %d-byte packet", frame, len(sink.packets)-packetsBefore, ref.Ret)
			}
			if !bytes.Equal(sink.packets[packetIndex], ref.Packet) {
				t.Fatalf("frame %d packet differs: Writer=%x libopus=%x", frame, sink.packets[packetIndex], ref.Packet)
			}
			packetIndex++
		} else if len(sink.packets) != packetsBefore {
			t.Fatalf("frame %d emitted a packet, libopus returned no packet", frame)
		}
		if got := w.enc.FinalRange(); got != ref.FinalRange {
			t.Fatalf("frame %d final range = %08x, libopus = %08x", frame, got, ref.FinalRange)
		}
		if frame >= speechFrames && frame < silenceEnd {
			enteredDTX = enteredDTX || w.enc.InDTX()
			sawOneByteDTXPacket = sawOneByteDTXPacket || (ref.Ret == 1 && len(ref.Packet) == 1)
		}
		if frame >= silenceEnd {
			resumedSpeech = resumedSpeech || !w.enc.InDTX()
		}
	}
	if !enteredDTX {
		t.Fatal("Writer did not enter DTX during silence")
	}
	if !sawOneByteDTXPacket {
		t.Fatal("libopus did not emit a one-byte DTX packet during silence")
	}
	if !resumedSpeech {
		t.Fatal("Writer did not leave DTX after speech resumed")
	}
	if packetIndex != len(sink.packets) {
		t.Fatalf("Writer packets = %d, compared %d libopus packets", len(sink.packets), packetIndex)
	}
}

func TestWriterInt16MatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const frameSize, frames = 960, 3
	for _, channels := range []int{1, 2} {
		t.Run(fmt.Sprintf("channels%d", channels), func(t *testing.T) {
			pcm := make([]int16, frameSize*channels*frames)
			seed := uint32(6716)
			inputSamples := frameSize*channels*2 + 443*channels
			for i := range inputSamples {
				seed ^= seed << 13
				seed ^= seed >> 17
				seed ^= seed << 5
				pcm[i] = int16(int32(seed) >> 30)
			}
			ref, err := libopustest.ProbeOpusEncodeFloatBuildShortRecords(libopustest.OpusEncodeFixedParams{
				SampleRate: 48000, Channels: channels, Application: libopustest.OpusApplicationAudio,
				MaxPacketBytes: 4000, ForceMode: libopustest.OpusForceModeCELTOnly,
				Bandwidth: libopustest.OpusBandwidthFullband, Bitrate: 64000, Complexity: 0, VBR: true,
				ForceChannels: channels, FrameSize: frameSize, FrameCount: frames, PCM: pcm,
			})
			if err != nil {
				t.Fatal(err)
			}
			sink := new(slicePacketSink)
			w, err := NewWriter(48000, channels, sink, FormatInt16LE, ApplicationAudio)
			if err != nil {
				t.Fatal(err)
			}
			for _, set := range []func() error{
				func() error { return w.enc.SetMode(EncoderModeCELT) },
				func() error { return w.enc.SetBandwidth(BandwidthFullband) },
				func() error { return w.SetComplexity(0) },
				func() error { return w.SetBitrate(64000) },
				func() error { return w.enc.SetBitrateMode(BitrateModeVBR) },
				func() error { return w.enc.SetForceChannels(channels) },
			} {
				if err := set(); err != nil {
					t.Fatal(err)
				}
			}
			raw := make([]byte, inputSamples*2)
			for i, v := range pcm[:inputSamples] {
				binary.LittleEndian.PutUint16(raw[i*2:], uint16(v))
			}
			// Split a sample across writes; Flush pads the final partial frame.
			split := frameSize*channels*2 + 1
			for _, chunk := range [][]byte{raw[:split], raw[split:]} {
				if n, err := w.Write(chunk); err != nil || n != len(chunk) {
					t.Fatalf("Write = %d, %v", n, err)
				}
			}
			if err := w.Flush(); err != nil {
				t.Fatal(err)
			}
			if len(sink.packets) != len(ref) {
				t.Fatalf("packets=%d, want %d", len(sink.packets), len(ref))
			}
			for i, packet := range sink.packets {
				if ref[i].Status != 0 || !bytes.Equal(packet, ref[i].Packet) {
					t.Fatalf("packet %d differs from opus_encode(int16*): bytes=%d/%d C status=%d", i, len(packet), len(ref[i].Packet), ref[i].Status)
				}
			}
			if got := w.enc.FinalRange(); got != ref[len(ref)-1].FinalRange {
				t.Fatalf("final range=%08x, want %08x", got, ref[len(ref)-1].FinalRange)
			}
		})
	}
}
