//go:build gopus_fixed_point

package multistream

import (
	"bytes"
	"errors"
	"fmt"
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/libopustest"
	"math"
	"testing"
)

func TestProjectionFixedPLCSequenceMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		sampleRate = 48000
		channels   = 4
		frameSize  = 960
	)
	ref := projectionDecodeRef(t, channels, frameSize, 2, 128000)
	if !allPerStreamCELT(ref.packets, ref.streams) {
		t.Fatal("projection PLC fixture must use CELT packets")
	}
	mapping := trivialMapping(channels)
	sequence := [][]byte{ref.packets[0], nil, ref.packets[1]}

	wantFloat, err := decodeWithLibopusReferencePackets(3, sampleRate, channels, ref.streams, ref.coupledStreams, frameSize, mapping, ref.demixing, sequence)
	if err != nil {
		libopustest.HelperUnavailable(t, "projection float PLC decode", err)
	}
	wantInt16, err := decodeWithLibopusReferencePacketsInt16Gain(3, sampleRate, channels, ref.streams, ref.coupledStreams, frameSize, 0, mapping, ref.demixing, sequence)
	if err != nil {
		libopustest.HelperUnavailable(t, "projection int16 PLC decode", err)
	}
	wantInt24, err := decodeWithLibopusReferencePacketsInt24Gain(3, sampleRate, channels, ref.streams, ref.coupledStreams, frameSize, 0, mapping, ref.demixing, sequence)
	if err != nil {
		libopustest.HelperUnavailable(t, "projection int24 PLC decode", err)
	}

	floatDec, err := NewProjectionDecoder(sampleRate, channels, ref.streams, ref.coupledStreams, ref.demixing)
	if err != nil {
		t.Fatal(err)
	}
	int16Dec, err := NewProjectionDecoder(sampleRate, channels, ref.streams, ref.coupledStreams, ref.demixing)
	if err != nil {
		t.Fatal(err)
	}
	int24Dec, err := NewProjectionDecoder(sampleRate, channels, ref.streams, ref.coupledStreams, ref.demixing)
	if err != nil {
		t.Fatal(err)
	}
	var gotFloat []float32
	var gotInt16 []int16
	var gotInt24 []int32
	for frame, packet := range sequence {
		f, err := floatDec.DecodeToFloat32(packet, frameSize)
		if err != nil {
			t.Fatalf("float frame %d: %v", frame, err)
		}
		gotFloat = append(gotFloat, f...)
		i16, err := int16Dec.DecodeToInt16(packet, frameSize)
		if err != nil {
			t.Fatalf("int16 frame %d: %v", frame, err)
		}
		gotInt16 = append(gotInt16, i16...)
		i24, err := int24Dec.DecodeToInt24(packet, frameSize)
		if err != nil {
			t.Fatalf("int24 frame %d: %v", frame, err)
		}
		gotInt24 = append(gotInt24, i24...)
	}
	assertProjectionFloatSampleExact(t, gotFloat, wantFloat, "fixed projection receive-loss-receive")
	assertProjectionInt16SampleExact(t, gotInt16, wantInt16, "fixed projection receive-loss-receive")
	if len(gotInt24) != len(wantInt24) {
		t.Fatalf("int24 sample count Go=%d C=%d", len(gotInt24), len(wantInt24))
	}
	for i := range gotInt24 {
		if gotInt24[i] != wantInt24[i] {
			t.Fatalf("int24 sample %d Go/C=%d/%d", i, gotInt24[i], wantInt24[i])
		}
	}
}

func TestProjectionFixedDecodeBufferPreflightPreservesState(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		sampleRate = 48000
		channels   = 4
		frameSize  = 960
	)
	ref := projectionDecodeRef(t, channels, frameSize, 2, 128000)
	mapping := trivialMapping(channels)
	want, err := decodeWithLibopusReferencePackets(3, sampleRate, channels, ref.streams, ref.coupledStreams, frameSize, mapping, ref.demixing, ref.packets)
	if err != nil {
		libopustest.HelperUnavailable(t, "projection receive sequence", err)
	}
	dec, err := NewProjectionDecoder(sampleRate, channels, ref.streams, ref.coupledStreams, ref.demixing)
	if err != nil {
		t.Fatal(err)
	}
	output := make([]float32, frameSize*channels)
	if n, err := dec.DecodeIntoFloat32(ref.packets[0], output, frameSize); err != nil || n != frameSize {
		t.Fatalf("first receive=(%d,%v), want (%d,nil)", n, err, frameSize)
	}
	tooSmall := make([]float32, frameSize*channels-1)
	for i := range tooSmall {
		tooSmall[i] = math.Float32frombits(0x7fc12345)
	}
	if n, err := dec.DecodeIntoFloat32(ref.packets[1], tooSmall, frameSize); n != 0 || !errors.Is(err, ErrBufferTooSmall) {
		t.Fatalf("undersized receive=(%d,%v), want (0,ErrBufferTooSmall)", n, err)
	}
	for i, sample := range tooSmall {
		if math.Float32bits(sample) != 0x7fc12345 {
			t.Fatalf("undersized call wrote output[%d]=%08x", i, math.Float32bits(sample))
		}
	}
	if n, err := dec.DecodeIntoFloat32(ref.packets[1], output, frameSize); err != nil || n != frameSize {
		t.Fatalf("retry receive=(%d,%v), want (%d,nil)", n, err, frameSize)
	}
	assertProjectionFloatSampleExact(t, output, want[frameSize*channels:], "projection buffer preflight retry")
}

func TestProjectionFixedDecodeIntoPLCZeroAllocs(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		sampleRate = 48000
		channels   = 4
		frameSize  = 960
	)
	ref := projectionDecodeRef(t, channels, frameSize, 1, 128000)
	dec, err := NewProjectionDecoder(sampleRate, channels, ref.streams, ref.coupledStreams, ref.demixing)
	if err != nil {
		t.Fatal(err)
	}
	output := make([]float32, frameSize*channels)
	if n, err := dec.DecodeIntoFloat32(ref.packets[0], output, frameSize); err != nil || n != frameSize {
		t.Fatalf("warm receive=(%d,%v), want (%d,nil)", n, err, frameSize)
	}
	if n, err := dec.DecodeIntoFloat32(nil, output, frameSize); err != nil || n != frameSize {
		t.Fatalf("warm PLC=(%d,%v), want (%d,nil)", n, err, frameSize)
	}
	if got := testing.AllocsPerRun(25, func() {
		if n, err := dec.DecodeIntoFloat32(ref.packets[0], output, frameSize); err != nil || n != frameSize {
			t.Fatalf("measured receive=(%d,%v)", n, err)
		}
		if n, err := dec.DecodeIntoFloat32(nil, output, frameSize); err != nil || n != frameSize {
			t.Fatalf("measured PLC=(%d,%v)", n, err)
		}
	}); got != 0 {
		t.Fatalf("warm projection receive+PLC allocations=%v want 0", got)
	}
}

func projectionUsesIntegerDecoderReference() bool { return true }

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

func projectionShortOracleBits(enc *Encoder, stream, index int, _ float32) uint32 {
	return uint32(enc.projectionShortResScratch[stream][index])
}
