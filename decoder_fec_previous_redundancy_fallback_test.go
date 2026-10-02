package gopus

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestDecodeWithFECPreviousCELTRedundancyFallbackMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const sampleRate, channels, packetFrameSize = 48000, 1, 960
	packets := encodePreviousRedundancyFECFallbackPackets(t)
	if ParseTOC(packets[0][0]).Mode != ModeSILK ||
		ParseTOC(packets[1][0]).Mode != ModeSILK ||
		ParseTOC(packets[2][0]).Mode != ModeHybrid ||
		ParseTOC(packets[3][0]).Mode != ModeCELT {
		t.Fatalf("packet modes=%v, want SILK/SILK/Hybrid/CELT", []Mode{
			ParseTOC(packets[0][0]).Mode, ParseTOC(packets[1][0]).Mode,
			ParseTOC(packets[2][0]).Mode, ParseTOC(packets[3][0]).Mode,
		})
	}

	type scenario struct {
		name             string
		fecPacketIndex   int
		requestedSamples int
	}
	scenarios := []scenario{
		{name: "CELT_packet_full_request", fecPacketIndex: 3, requestedSamples: packetFrameSize},
		{name: "CELT_packet_short_request", fecPacketIndex: 3, requestedSamples: packetFrameSize / 2},
		{name: "Hybrid_packet_short_request", fecPacketIndex: 2, requestedSamples: packetFrameSize / 2},
		{name: "SILK_packet_short_request", fecPacketIndex: 0, requestedSamples: packetFrameSize / 2},
	}
	formats := []struct {
		name string
		kind uint32
	}{
		{name: "float32_continuation", kind: libopustest.DecodeDiffFormatFloat32},
		{name: "int16_continuation", kind: libopustest.DecodeDiffFormatInt16},
		{name: "int24_continuation", kind: libopustest.DecodeDiffFormatInt24},
	}
	for _, format := range formats {
		format := format
		t.Run(format.name, func(t *testing.T) {
			for _, tc := range scenarios {
				t.Run(tc.name, func(t *testing.T) {
					steps := []libopustest.DecodeDiffCase{
						{Packet: packets[0], Format: format.kind, FrameSize: packetFrameSize},
						{Packet: packets[1], Format: format.kind, FrameSize: packetFrameSize},
						{Packet: packets[2], Format: format.kind, FrameSize: packetFrameSize},
						{Packet: packets[tc.fecPacketIndex], Format: libopustest.DecodeDiffFormatFloat32, FrameSize: uint32(tc.requestedSamples), DecodeFEC: true},
						{Format: format.kind, FrameSize: uint32(tc.requestedSamples)},
						{Packet: packets[tc.fecPacketIndex], Format: format.kind, FrameSize: packetFrameSize},
					}
					want, err := libopustest.ProbeDecodeSequence(sampleRate, channels, steps)
					if err != nil {
						t.Fatalf("probe selected libopus sequence: %v", err)
					}
					if len(want) != len(steps) {
						t.Fatalf("selected libopus records=%d, want %d", len(want), len(steps))
					}

					dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
					if err != nil {
						t.Fatal(err)
					}
					pcm32 := make([]float32, packetFrameSize*channels)
					pcm16 := make([]int16, packetFrameSize*channels)
					pcm24 := make([]int32, packetFrameSize*channels)
					for step := range steps {
						if step == 3 && (!dec.haveDecoded || dec.prevMode != ModeHybrid || !dec.prevRedundancy) {
							t.Fatalf("redundancy seed state haveDecoded=%v prevMode=%v prevRedundancy=%v; want decoded Hybrid with CELT redundancy", dec.haveDecoded, dec.prevMode, dec.prevRedundancy)
						}
						frameSize := int(steps[step].FrameSize)
						outputLength := frameSize * channels
						var n int
						var decodeErr error
						switch step {
						case 3:
							n, decodeErr = dec.DecodeWithFEC(packets[tc.fecPacketIndex], pcm32[:outputLength], true)
						case 4:
							switch format.kind {
							case libopustest.DecodeDiffFormatFloat32:
								n, decodeErr = dec.Decode(nil, pcm32[:outputLength])
							case libopustest.DecodeDiffFormatInt16:
								n, decodeErr = dec.DecodeInt16(nil, pcm16[:outputLength])
							default:
								n, decodeErr = dec.DecodeInt24(nil, pcm24[:outputLength])
							}
						default:
							packetIndex := step
							if step == 5 {
								packetIndex = tc.fecPacketIndex
							}
							switch format.kind {
							case libopustest.DecodeDiffFormatFloat32:
								n, decodeErr = dec.Decode(packets[packetIndex], pcm32[:outputLength])
							case libopustest.DecodeDiffFormatInt16:
								n, decodeErr = dec.DecodeInt16(packets[packetIndex], pcm16[:outputLength])
							default:
								n, decodeErr = dec.DecodeInt24(packets[packetIndex], pcm24[:outputLength])
							}
						}
						if decodeErr != nil || n != int(want[step].Code) {
							t.Fatalf("step%d decode=(%d,%v), selected C=%d", step, n, decodeErr, want[step].Code)
						}
						if got, expected := dec.FinalRange(), want[step].FinalRange; got != expected {
							t.Fatalf("step%d FinalRange=%08x selected C=%08x", step, got, expected)
						}
						if step == 3 || format.kind == libopustest.DecodeDiffFormatFloat32 {
							wantPCM := want[step].Float32()
							if len(wantPCM) != n*channels {
								t.Fatalf("step%d selected C PCM length=%d, status=%d", step, len(wantPCM), n)
							}
							for i := range wantPCM {
								if gotBits, wantBits := math.Float32bits(pcm32[i]), math.Float32bits(wantPCM[i]); gotBits != wantBits {
									t.Fatalf("step%d PCM[%d]=%08x selected C=%08x", step, i, gotBits, wantBits)
								}
							}
						} else if format.kind == libopustest.DecodeDiffFormatInt16 {
							wantPCM := want[step].Int16()
							if len(wantPCM) != n*channels {
								t.Fatalf("step%d selected C PCM length=%d, status=%d", step, len(wantPCM), n)
							}
							for i := range wantPCM {
								if pcm16[i] != wantPCM[i] {
									t.Fatalf("step%d PCM[%d]=%d selected C=%d", step, i, pcm16[i], wantPCM[i])
								}
							}
						} else {
							wantPCM := want[step].Int24()
							if len(wantPCM) != n*channels {
								t.Fatalf("step%d selected C PCM length=%d, status=%d", step, len(wantPCM), n)
							}
							for i := range wantPCM {
								if pcm24[i] != wantPCM[i] {
									t.Fatalf("step%d PCM[%d]=%d selected C=%d", step, i, pcm24[i], wantPCM[i])
								}
							}
						}
						if step == 3 && (dec.prevMode != ModeCELT || dec.prevRedundancy) {
							t.Fatalf("post-fallback state prevMode=%v prevRedundancy=%v, want CELT with redundancy consumed", dec.prevMode, dec.prevRedundancy)
						}
					}
				})
			}
		})
	}

	// Repeat the real history on one decoder so the new fallback remains
	// allocation-free after warmup without adding resets to the measured path.
	allocDec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatal(err)
	}
	allocPCM := make([]float32, packetFrameSize*channels)
	decodeHistoryAndFallback := func() error {
		for _, packet := range packets[:3] {
			if _, err := allocDec.Decode(packet, allocPCM); err != nil {
				return err
			}
		}
		_, err := allocDec.DecodeWithFEC(packets[3], allocPCM, true)
		return err
	}
	for range 3 {
		if err := decodeHistoryAndFallback(); err != nil {
			t.Fatalf("warm fallback history: %v", err)
		}
	}
	var allocErr error
	if allocs := testing.AllocsPerRun(20, func() {
		if err := decodeHistoryAndFallback(); err != nil && allocErr == nil {
			allocErr = err
		}
	}); allocs != 0 {
		t.Fatalf("warmed previous-redundancy FEC fallback allocations=%g, want zero", allocs)
	}
	if allocErr != nil {
		t.Fatalf("measured fallback history: %v", allocErr)
	}
}

func encodePreviousRedundancyFECFallbackPackets(t *testing.T) [][]byte {
	t.Helper()
	libopustest.RequireOracle(t)
	const sampleRate, channels, frameSize, frameCount = 48000, 1, 960, 4
	pcm := make([]int16, frameSize*channels*frameCount)
	for frame := range frameCount {
		for i := range frameSize {
			tm := float64(frame*frameSize+i) / sampleRate
			v := 0.25*math.Sin(2*math.Pi*220*tm) + 0.08*math.Sin(2*math.Pi*1700*tm)
			pcm[frame*frameSize+i] = int16(v * 32767)
		}
	}
	frames := []libopustest.OpusEncodeFixedMixedFrame{
		{ShortPCM: pcm[:frameSize], ForceMode: libopustest.OpusForceModeSILKOnly, Bandwidth: libopustest.OpusBandwidthWideband},
		{ShortPCM: pcm[frameSize : 2*frameSize], ForceMode: libopustest.OpusForceModeSILKOnly, Bandwidth: libopustest.OpusBandwidthWideband},
		{ShortPCM: pcm[2*frameSize : 3*frameSize], ForceMode: libopustest.OpusForceModeCELTOnly, Bandwidth: libopustest.OpusBandwidthFullband},
		{ShortPCM: pcm[3*frameSize:], ForceMode: libopustest.OpusForceModeCELTOnly, Bandwidth: libopustest.OpusBandwidthFullband},
	}
	records, err := libopustest.ProbeOpusEncodeFixedMixedRecords(libopustest.OpusEncodeFixedParams{
		SampleRate: sampleRate, Channels: channels, Application: libopustest.OpusApplicationAudio,
		Bitrate: 128000, Complexity: 10, ForceChannels: channels, LSBDepth: 16,
		FrameSize: frameSize, FrameCount: frameCount, PCM: pcm,
	}, frames)
	if err != nil {
		t.Fatalf("encode selected libopus sequence: %v", err)
	}
	if len(records) != frameCount {
		t.Fatalf("selected libopus packets=%d, want %d", len(records), frameCount)
	}
	packets := make([][]byte, frameCount)
	for i, record := range records {
		if record.Status < 0 || len(record.Packet) < 2 {
			t.Fatalf("selected libopus frame%d status=%d packetBytes=%d", i, record.Status, len(record.Packet))
		}
		packets[i] = append([]byte(nil), record.Packet...)
	}
	return packets
}
