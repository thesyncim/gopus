package gopus

import (
	"bytes"
	"errors"
	"fmt"
	encodercore "github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/libopustest"
	mspkg "github.com/thesyncim/gopus/multistream"
	"math"
	"slices"
	"strings"
	"testing"
)

var multistreamRefdecodeHelper libopustest.HelperCache

func multistreamReferenceDecoderPath() (string, error) {
	return multistreamRefdecodeHelper.Path(func() (string, error) {
		return libopustest.BuildPublicAPIHelper(libopustest.CHelperConfig{
			Label:      "multistream reference decode",
			OutputBase: "gopus_libopus_refdecode_public_multistream",
			SourceFile: "libopus_refdecode_multistream.c",
			CFlags:     []string{"-O3", "-DNDEBUG"},
		})
	})
}

func runLibopusMultistreamDecode(sampleRate, channels, streams, coupled, frameSize, gainQ8, sampleFormat int, mapping []byte, packets [][]byte) (*libopustest.OracleReader, error) {
	binPath, err := multistreamReferenceDecoderPath()
	if err != nil {
		return nil, err
	}

	payload := libopustest.NewOraclePayloadVersion(
		"GMSI",
		4,
		uint32(sampleRate),
		uint32(int32(gainQ8)),
		uint32(sampleFormat),
		1,
		uint32(channels),
		uint32(streams),
		uint32(coupled),
		uint32(frameSize),
		uint32(len(packets)),
		uint32(len(mapping)),
		0,
	)
	payload.Raw(mapping)
	for _, packet := range packets {
		payload.U32(uint32(len(packet)))
		payload.Raw(packet)
	}
	return libopustest.RunOracle(binPath, payload.Bytes(), "multistream reference decode", "GMSO")
}

func decodeLibopusMultistreamInt16Gain(sampleRate, channels, streams, coupled, frameSize, gainQ8 int, mapping []byte, packets [][]byte) ([]int16, error) {
	reader, err := runLibopusMultistreamDecode(sampleRate, channels, streams, coupled, frameSize, gainQ8, 1, mapping, packets)
	if err != nil {
		return nil, err
	}
	nSamples := reader.Count(-1)
	reader.ExpectRemaining(nSamples * 2)
	out := make([]int16, nSamples)
	for i := range out {
		out[i] = reader.I16()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func decodeLibopusMultistreamFloat32(sampleRate, channels, streams, coupled, frameSize int, mapping []byte, packets [][]byte) ([]float32, error) {
	reader, err := runLibopusMultistreamDecode(sampleRate, channels, streams, coupled, frameSize, 0, 0, mapping, packets)
	if err != nil {
		return nil, err
	}
	nSamples := reader.Count(-1)
	reader.ExpectRemaining(nSamples * 4)
	out := make([]float32, nSamples)
	for i := range out {
		out[i] = reader.Float32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func TestMultistreamDecodeUsesAPIRatePacketDuration(t *testing.T) {
	modes := []struct {
		name   string
		packet func(*testing.T, int) []byte
	}{
		{name: "silk", packet: encodeAPIRateSILKPacket},
		{name: "celt", packet: encodeAPIRateCELTPacket},
		{name: "hybrid", packet: encodeAPIRateHybridPacket},
	}
	for _, mode := range modes {
		for _, channels := range []int{1, 2} {
			packet := mode.packet(t, channels)
			for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
				t.Run(mode.name+"_ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate), func(t *testing.T) {
					want, err := packetSamplesAtRate(packet, sampleRate)
					if err != nil {
						t.Fatalf("packetSamplesAtRate: %v", err)
					}
					if got, err := mspkg.PacketDurationAtRate(packet, 1, sampleRate); err != nil || got != want {
						t.Fatalf("multistream PacketDurationAtRate()=(%d,%v) want (%d,nil)", got, err, want)
					}

					smallDec := mustNewDefaultMultistreamDecoder(t, sampleRate, channels)
					if _, err := smallDec.Decode(packet, make([]float32, want*channels-1)); err != ErrBufferTooSmall {
						t.Fatalf("Decode small-buffer error=%v want %v", err, ErrBufferTooSmall)
					}

					dec := mustNewDefaultMultistreamDecoder(t, sampleRate, channels)
					pcm := make([]float32, want*channels)
					n, err := dec.Decode(packet, pcm)
					if err != nil {
						t.Fatalf("Decode: %v", err)
					}
					if n != want {
						t.Fatalf("Decode samples=%d want %d", n, want)
					}
					if got := dec.LastPacketDuration(); got != want {
						t.Fatalf("LastPacketDuration()=%d want %d", got, want)
					}

					plc := make([]float32, want*channels)
					n, err = dec.Decode(nil, plc)
					if err != nil {
						t.Fatalf("Decode(nil): %v", err)
					}
					if n != want {
						t.Fatalf("Decode(nil) samples=%d want %d", n, want)
					}
					if got := dec.LastPacketDuration(); got != want {
						t.Fatalf("LastPacketDuration() after PLC=%d want %d", got, want)
					}

					intDec := mustNewDefaultMultistreamDecoder(t, sampleRate, channels)
					pcm16 := make([]int16, want*channels)
					n, err = intDec.DecodeInt16(packet, pcm16)
					if err != nil {
						t.Fatalf("DecodeInt16: %v", err)
					}
					if n != want {
						t.Fatalf("DecodeInt16 samples=%d want %d", n, want)
					}
				})
			}
		}
	}
}

func TestMultistreamDecodeFloat32MatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	modes := []struct {
		name   string
		packet func(*testing.T, int) []byte
	}{
		{name: "silk", packet: encodeAPIRateSILKPacket},
		{name: "celt", packet: encodeAPIRateCELTPacket},
		{name: "hybrid", packet: encodeAPIRateHybridPacket},
	}
	for _, mode := range modes {
		for _, channels := range []int{1, 2} {
			packet := mode.packet(t, channels)
			streams := 1
			coupled := channels - 1
			mapping := []byte{0}
			if channels == 2 {
				mapping = []byte{0, 1}
			}
			for _, sampleRate := range []int{16000, 48000} {
				t.Run(mode.name+"_ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate), func(t *testing.T) {
					frameSize, err := packetSamplesAtRate(packet, sampleRate)
					if err != nil {
						t.Fatalf("packetSamplesAtRate: %v", err)
					}

					sequence := [][]byte{packet, nil}
					want, err := decodeLibopusMultistreamFloat32(sampleRate, channels, streams, coupled, frameSize, mapping, sequence)
					if err != nil {
						libopustest.HelperUnavailable(t, "multistream float32 reference decode", err)
					}

					dec := mustNewDefaultMultistreamDecoder(t, sampleRate, channels)
					got := make([]float32, 0, len(want))
					frame := make([]float32, frameSize*channels)
					n, err := dec.Decode(packet, frame)
					if err != nil {
						t.Fatalf("Decode(packet): %v", err)
					}
					if n != frameSize {
						t.Fatalf("Decode(packet)=%d want %d", n, frameSize)
					}
					got = append(got, frame[:n*channels]...)
					clear(frame)
					n, err = dec.Decode(nil, frame)
					if err != nil {
						t.Fatalf("Decode(nil): %v", err)
					}
					if n != frameSize {
						t.Fatalf("Decode(nil)=%d want %d", n, frameSize)
					}
					got = append(got, frame[:n*channels]...)
					// PLC dominates this short sequence, so opus_compare's Q is
					// diagnostic. Both public float outputs must match sample bits.
					assertAPIRateQualityFloat32PLC(t, got, want, sampleRate, channels, true, "multistream "+mode.name+" float32")
					assertAPIRateFloat32BitsExact(t, got, want, "multistream "+mode.name+" float32")
				})
			}
		}
	}
}

func TestMultistreamDecodeRequestedPLCDurationMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	modes := []struct {
		name   string
		packet func(*testing.T, int) []byte
	}{
		{name: "silk", packet: encodeAPIRateSILKPacket},
		{name: "celt", packet: encodeAPIRateCELTPacket},
		{name: "hybrid", packet: encodeAPIRateHybridPacket},
	}
	for _, mode := range modes {
		for _, channels := range []int{1, 2} {
			packet := mode.packet(t, channels)
			streams := 1
			coupled := channels - 1
			mapping := []byte{0}
			if channels == 2 {
				mapping = []byte{0, 1}
			}
			for _, sampleRate := range []int{8000, 16000, 48000} {
				t.Run(mode.name+"_ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate), func(t *testing.T) {
					packetFrameSize, err := packetSamplesAtRate(packet, sampleRate)
					if err != nil {
						t.Fatalf("packetSamplesAtRate: %v", err)
					}
					requestedFrameSize := sampleRate / 25
					if requestedFrameSize == packetFrameSize {
						t.Fatalf("requestedFrameSize=%d unexpectedly equals packet frame size", requestedFrameSize)
					}

					sequence := [][]byte{packet, nil}
					want, err := decodeLibopusMultistreamFloat32(sampleRate, channels, streams, coupled, requestedFrameSize, mapping, sequence)
					if err != nil {
						libopustest.HelperUnavailable(t, "multistream requested PLC reference decode", err)
					}

					dec := mustNewDefaultMultistreamDecoder(t, sampleRate, channels)
					got := make([]float32, 0, len(want))
					frame := make([]float32, requestedFrameSize*channels)
					n, err := dec.Decode(packet, frame)
					if err != nil {
						t.Fatalf("Decode(packet): %v", err)
					}
					if n != packetFrameSize {
						t.Fatalf("Decode(packet)=%d want %d", n, packetFrameSize)
					}
					got = append(got, frame[:n*channels]...)
					clear(frame)
					n, err = dec.Decode(nil, frame)
					if err != nil {
						t.Fatalf("Decode(nil): %v", err)
					}
					if n != requestedFrameSize {
						t.Fatalf("Decode(nil)=%d want %d", n, requestedFrameSize)
					}
					got = append(got, frame[:n*channels]...)
					assertAPIRateQualityFloat32PLC(t, got, want, sampleRate, channels, true, "multistream "+mode.name+" requested PLC")
					assertAPIRateFloat32BitsExact(t, got, want, "multistream "+mode.name+" requested PLC")
				})
			}
		}
	}
}

func TestMultistreamDecodeOverlongAndEmptyPLCMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, channels := range []int{1, 2} {
		packet := encodeAPIRateCELTPacket(t, channels)
		streams := 1
		coupled := channels - 1
		mapping := []byte{0}
		if channels == 2 {
			mapping = []byte{0, 1}
		}
		for _, sampleRate := range []int{8000, 48000} {
			t.Run("float_ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate), func(t *testing.T) {
				packetFrameSize, err := packetSamplesAtRate(packet, sampleRate)
				if err != nil {
					t.Fatalf("packetSamplesAtRate: %v", err)
				}
				requestedFrameSize := sampleRate
				wantPLCFrameSize := sampleRate / 25 * 3
				sequence := [][]byte{packet, {}}
				want, err := decodeLibopusMultistreamFloat32(sampleRate, channels, streams, coupled, requestedFrameSize, mapping, sequence)
				if err != nil {
					libopustest.HelperUnavailable(t, "multistream overlong PLC reference decode", err)
				}

				dec := mustNewDefaultMultistreamDecoder(t, sampleRate, channels)
				got := make([]float32, 0, len(want))
				frame := make([]float32, requestedFrameSize*channels)
				n, err := dec.Decode(packet, frame)
				if err != nil {
					t.Fatalf("Decode(packet): %v", err)
				}
				if n != packetFrameSize {
					t.Fatalf("Decode(packet)=%d want %d", n, packetFrameSize)
				}
				got = append(got, frame[:n*channels]...)
				clear(frame)
				n, err = dec.Decode(nil, frame)
				if err != nil {
					t.Fatalf("Decode(nil): %v", err)
				}
				if n != wantPLCFrameSize {
					t.Fatalf("Decode(nil)=%d want clamped %d", n, wantPLCFrameSize)
				}
				got = append(got, frame[:n*channels]...)
				assertAPIRateQualityFloat32PLC(t, got, want, sampleRate, channels, true, "multistream overlong empty PLC")
				assertAPIRateFloat32BitsExact(t, got, want, "multistream overlong empty PLC")
			})

			t.Run("int16_ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate), func(t *testing.T) {
				requestedFrameSize := sampleRate
				wantPLCFrameSize := sampleRate / 25 * 3
				sequence := [][]byte{packet, {}}
				want, err := decodeLibopusMultistreamInt16Gain(sampleRate, channels, streams, coupled, requestedFrameSize, 0, mapping, sequence)
				if err != nil {
					libopustest.HelperUnavailable(t, "multistream overlong PLC int16 reference decode", err)
				}

				dec := mustNewDefaultMultistreamDecoder(t, sampleRate, channels)
				got := make([]int16, 0, len(want))
				frame := make([]int16, requestedFrameSize*channels)
				n, err := dec.DecodeInt16(packet, frame)
				if err != nil {
					t.Fatalf("DecodeInt16(packet): %v", err)
				}
				got = append(got, frame[:n*channels]...)
				clear(frame)
				n, err = dec.DecodeInt16(nil, frame)
				if err != nil {
					t.Fatalf("DecodeInt16(nil): %v", err)
				}
				if n != wantPLCFrameSize {
					t.Fatalf("DecodeInt16(nil)=%d want clamped %d", n, wantPLCFrameSize)
				}
				got = append(got, frame[:n*channels]...)
				assertAPIRateQualityInt16PLC(t, got, want, sampleRate, channels, true, "multistream overlong empty PLC int16")
				assertAPIRateInt16Exact(t, got, want, "multistream overlong empty PLC int16")
			})
		}
	}
}

func TestMultistreamDecodeInt16HighGainMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		sampleRate = 48000
		channels   = 2
		streams    = 1
		coupled    = 1
		frameSize  = sampleRate / 50
		gainQ8     = 8192
	)
	mapping := []byte{0, 1}
	packet := encodeAPIRateCELTPacket(t, channels)
	sequence := [][]byte{packet, nil}
	want, err := decodeLibopusMultistreamInt16Gain(sampleRate, channels, streams, coupled, frameSize, gainQ8, mapping, sequence)
	if err != nil {
		libopustest.HelperUnavailable(t, "multistream int16 gain reference decode", err)
	}

	dec, err := NewMultistreamDecoder(sampleRate, channels, streams, coupled, mapping)
	if err != nil {
		t.Fatalf("NewMultistreamDecoder: %v", err)
	}
	if err := dec.SetGain(gainQ8); err != nil {
		t.Fatalf("SetGain(%d): %v", gainQ8, err)
	}
	got := make([]int16, 0, len(want))
	frame := make([]int16, frameSize*channels)
	n, err := dec.DecodeInt16(packet, frame)
	if err != nil {
		t.Fatalf("DecodeInt16(packet): %v", err)
	}
	if n != frameSize {
		t.Fatalf("DecodeInt16(packet)=%d want %d", n, frameSize)
	}
	got = append(got, frame[:n*channels]...)
	clear(frame)
	n, err = dec.DecodeInt16(nil, frame)
	if err != nil {
		t.Fatalf("DecodeInt16(nil): %v", err)
	}
	if n != frameSize {
		t.Fatalf("DecodeInt16(nil)=%d want %d", n, frameSize)
	}
	got = append(got, frame[:n*channels]...)
	assertAPIRateQualityInt16(t, got, want, sampleRate, channels, "multistream high-gain int16")
	assertAPIRateInt16Exact(t, got, want, "multistream high-gain int16")
}

func TestMultistreamDecodeInvalidRequestedPLCFrameSizeMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, channels := range []int{1, 2} {
		streams := 1
		coupled := channels - 1
		mapping := []byte{0}
		if channels == 2 {
			mapping = []byte{0, 1}
		}
		for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
			for _, frameSize := range invalidAPIRateRequestedFrameSizes(sampleRate) {
				t.Run("float_ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate)+"_request_"+itoaSmall(frameSize), func(t *testing.T) {
					if _, err := decodeLibopusMultistreamFloat32(sampleRate, channels, streams, coupled, frameSize, mapping, [][]byte{nil}); err == nil {
						t.Fatalf("libopus multistream Decode(nil) accepted frame_size=%d", frameSize)
					}

					dec := mustNewDefaultMultistreamDecoder(t, sampleRate, channels)
					n, err := dec.Decode(nil, make([]float32, frameSize*channels))
					if n != 0 || err != ErrInvalidFrameSize {
						t.Fatalf("Decode(nil) = (%d, %v), want (0, %v)", n, err, ErrInvalidFrameSize)
					}
				})
				t.Run("int16_ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate)+"_request_"+itoaSmall(frameSize), func(t *testing.T) {
					if _, err := decodeLibopusMultistreamInt16Gain(sampleRate, channels, streams, coupled, frameSize, 0, mapping, [][]byte{nil}); err == nil {
						t.Fatalf("libopus multistream DecodeInt16(nil) accepted frame_size=%d", frameSize)
					}

					dec := mustNewDefaultMultistreamDecoder(t, sampleRate, channels)
					n, err := dec.DecodeInt16(nil, make([]int16, frameSize*channels))
					if n != 0 || err != ErrInvalidFrameSize {
						t.Fatalf("DecodeInt16(nil) = (%d, %v), want (0, %v)", n, err, ErrInvalidFrameSize)
					}
				})
			}
		}
	}
}

func TestMultistreamDecodeRejectsNonChannelMultipleRequestedPLCBuffer(t *testing.T) {
	for _, channels := range []int{2, 3} {
		for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
			t.Run("ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate), func(t *testing.T) {
				frameSize := sampleRate / 50
				sampleCount := frameSize*channels + 1

				dec := mustNewDefaultMultistreamDecoder(t, sampleRate, channels)
				n, err := dec.Decode(nil, make([]float32, sampleCount))
				if n != 0 || err != ErrInvalidFrameSize {
					t.Fatalf("Decode(nil) = (%d, %v), want (0, %v)", n, err, ErrInvalidFrameSize)
				}

				intDec := mustNewDefaultMultistreamDecoder(t, sampleRate, channels)
				n, err = intDec.DecodeInt16(nil, make([]int16, sampleCount))
				if n != 0 || err != ErrInvalidFrameSize {
					t.Fatalf("DecodeInt16(nil) = (%d, %v), want (0, %v)", n, err, ErrInvalidFrameSize)
				}
			})
		}
	}
}

func TestMultistreamColdPLCAfterResetUsesAPIRateDefault(t *testing.T) {
	for _, channels := range []int{1, 2} {
		for _, sampleRate := range []int{8000, 12000, 16000, 24000, 48000} {
			t.Run("ch_"+itoaSmall(channels)+"_fs_"+itoaSmall(sampleRate), func(t *testing.T) {
				dec := mustNewDefaultMultistreamDecoder(t, sampleRate, channels)
				want := sampleRate / 50

				pcm := make([]float32, want*channels)
				n, err := dec.Decode(nil, pcm)
				if err != nil {
					t.Fatalf("cold Decode(nil): %v", err)
				}
				if n != want {
					t.Fatalf("cold Decode(nil) samples=%d want %d", n, want)
				}
				if got := dec.LastPacketDuration(); got != want {
					t.Fatalf("cold LastPacketDuration()=%d want %d", got, want)
				}

				dec.Reset()
				n, err = dec.Decode(nil, pcm)
				if err != nil {
					t.Fatalf("Decode(nil) after Reset: %v", err)
				}
				if n != want {
					t.Fatalf("Decode(nil) after Reset samples=%d want %d", n, want)
				}
				if got := dec.LastPacketDuration(); got != want {
					t.Fatalf("LastPacketDuration() after reset cold PLC=%d want %d", got, want)
				}
			})
		}
	}
}

var multistreamApplicationStateHelper libopustest.HelperCache

const (
	libopusApplicationVoIP     int32 = 2048
	libopusApplicationAudio    int32 = 2049
	libopusApplicationLowDelay int32 = 2051
)

type multistreamApplicationStateCase struct {
	name           string
	channels       int
	streams        int
	coupledStreams int
	mapping        []byte
	lowCapacity    int
	pcm            []float32
}

type multistreamApplicationPacket struct {
	status      int32
	rangeStatus int32
	finalRange  uint32
	bytes       []byte
}

type multistreamApplicationOracleResult struct {
	createStatus          int32
	bitrateStatus         int32
	lowPacket             multistreamApplicationPacket
	setApplicationStatus  int32
	getApplicationStatus  int32
	application           int32
	childApplicationState []int32
	fullPacket            multistreamApplicationPacket
}

func buildMultistreamApplicationStateHelper() (string, error) {
	return libopustest.BuildPublicAPIHelper(libopustest.CHelperConfig{
		Label:      "multistream application state after low-budget encode",
		OutputBase: "gopus_libopus_multistream_application_state",
		SourceFile: "libopus_multistream_application_state.c",
		CFlags:     []string{"-O3", "-DNDEBUG"},
	})
}

func probeMultistreamApplicationState(tc multistreamApplicationStateCase) (multistreamApplicationOracleResult, error) {
	helper, err := multistreamApplicationStateHelper.Path(buildMultistreamApplicationStateHelper)
	if err != nil {
		return multistreamApplicationOracleResult{}, err
	}
	if len(tc.pcm) != tc.channels*960 {
		return multistreamApplicationOracleResult{}, fmt.Errorf("invalid PCM length %d for %d channels", len(tc.pcm), tc.channels)
	}

	const fullCapacity = 4000
	payload := libopustest.NewOraclePayloadVersion("GMCI", 1,
		uint32(tc.channels), uint32(tc.streams), uint32(tc.coupledStreams),
		960, uint32(tc.lowCapacity), fullCapacity, 64000, uint32(len(tc.mapping)))
	payload.Raw(tc.mapping)
	for _, sample := range tc.pcm {
		payload.Float32(sample)
	}
	reader, err := libopustest.RunOracle(helper, payload.Bytes(), "multistream application state", "GMCO")
	if err != nil {
		return multistreamApplicationOracleResult{}, err
	}
	result := multistreamApplicationOracleResult{
		createStatus:         reader.I32(),
		bitrateStatus:        reader.I32(),
		lowPacket:            readMultistreamApplicationPacket(reader),
		setApplicationStatus: reader.I32(),
		getApplicationStatus: reader.I32(),
		application:          reader.I32(),
	}
	streamCount := reader.Count(tc.streams)
	result.childApplicationState = make([]int32, streamCount)
	for i := range result.childApplicationState {
		if status := reader.I32(); status != 0 {
			return multistreamApplicationOracleResult{}, fmt.Errorf("libopus child %d application getter status=%d", i, status)
		}
		result.childApplicationState[i] = reader.I32()
	}
	result.fullPacket = readMultistreamApplicationPacket(reader)
	if err := reader.ExpectConsumed(); err != nil {
		return multistreamApplicationOracleResult{}, fmt.Errorf("parse libopus multistream application result: %w", err)
	}
	if err := reader.Err(); err != nil {
		return multistreamApplicationOracleResult{}, err
	}
	return result, nil
}

func readMultistreamApplicationPacket(reader *libopustest.OracleReader) multistreamApplicationPacket {
	status := reader.I32()
	rangeStatus := reader.I32()
	finalRange := reader.U32()
	packetLen := reader.Count(-1)
	return multistreamApplicationPacket{
		status:      status,
		rangeStatus: rangeStatus,
		finalRange:  finalRange,
		bytes:       reader.Bytes(packetLen),
	}
}

func TestMultistreamEncoderSetApplicationAfterLowSpaceMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	monoSilence := make([]float32, 960)
	mixedPCM := make([]float32, 3*960)
	mixedPCM[2] = 0.25
	mixedPCM[5] = -0.125
	cases := []multistreamApplicationStateCase{
		{
			name:        "mono_low_space_keeps_first_frame_open",
			channels:    1,
			streams:     1,
			mapping:     []byte{0},
			lowCapacity: 1,
			pcm:         monoSilence,
		},
		{
			name:           "coupled_and_mono_low_space_keeps_all_children_open",
			channels:       3,
			streams:        2,
			coupledStreams: 1,
			mapping:        []byte{0, 1, 2},
			lowCapacity:    3,
			pcm:            mixedPCM,
		},
		{
			name:           "coupled_and_mono_budget_commits_a_child",
			channels:       3,
			streams:        2,
			coupledStreams: 1,
			mapping:        []byte{0, 1, 2},
			lowCapacity:    5,
			pcm:            mixedPCM,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			want, err := probeMultistreamApplicationState(tc)
			if err != nil {
				libopustest.HelperUnavailable(t, "multistream application state", err)
			}
			if want.createStatus != 0 || want.bitrateStatus != 0 {
				t.Fatalf("libopus setup status create=%d bitrate=%d", want.createStatus, want.bitrateStatus)
			}
			if want.lowPacket.status < 0 || want.lowPacket.rangeStatus != 0 {
				t.Fatalf("libopus low-budget encode status=%d range status=%d", want.lowPacket.status, want.lowPacket.rangeStatus)
			}
			if want.setApplicationStatus != 0 && want.setApplicationStatus != -1 {
				t.Fatalf("libopus SetApplication status=%d, want OPUS_OK or OPUS_BAD_ARG", want.setApplicationStatus)
			}
			if want.getApplicationStatus != 0 {
				t.Fatalf("libopus GetApplication status=%d", want.getApplicationStatus)
			}
			if want.fullPacket.status < 0 || want.fullPacket.rangeStatus != 0 {
				t.Fatalf("libopus full-budget encode status=%d range status=%d", want.fullPacket.status, want.fullPacket.rangeStatus)
			}

			enc, err := NewMultistreamEncoder(48000, tc.channels, tc.streams, tc.coupledStreams, tc.mapping, ApplicationAudio)
			if err != nil {
				t.Fatalf("NewMultistreamEncoder: %v", err)
			}
			if err := enc.SetBitrate(64000); err != nil {
				t.Fatalf("SetBitrate(64000): %v", err)
			}

			lowPacket := make([]byte, tc.lowCapacity)
			n, err := enc.Encode(tc.pcm, lowPacket)
			if err != nil {
				t.Fatalf("low-budget Encode: %v", err)
			}
			assertMultistreamApplicationPacket(t, "low-budget encode", multistreamApplicationPacket{
				status: int32(n), rangeStatus: 0, finalRange: enc.GetFinalRange(), bytes: lowPacket[:n],
			}, want.lowPacket)

			setErr := enc.SetApplication(ApplicationVoIP)
			if want.setApplicationStatus == 0 && setErr != nil {
				t.Fatalf("SetApplication(VoIP) error=%v, libopus accepted it", setErr)
			}
			if want.setApplicationStatus == -1 && setErr != ErrInvalidApplication {
				t.Fatalf("SetApplication(VoIP) error=%v, want %v from libopus rejection", setErr, ErrInvalidApplication)
			}
			if got := libopusApplicationCode(enc.Application()); got != want.application {
				t.Fatalf("Application()=%d, libopus OPUS_GET_APPLICATION=%d", got, want.application)
			}
			if got := enc.enc.VoIPApplication(); got != (want.application == libopusApplicationVoIP) {
				t.Fatalf("first child VoIP policy=%t, libopus application=%d", got, want.application)
			}
			if got := enc.enc.LowDelay(); got != (want.application == libopusApplicationLowDelay) {
				t.Fatalf("first child low-delay policy=%t, libopus application=%d", got, want.application)
			}
			for i, app := range want.childApplicationState {
				if want.setApplicationStatus == 0 && app != libopusApplicationVoIP {
					t.Fatalf("libopus child %d application=%d after successful broadcast, want VoIP", i, app)
				}
				if want.setApplicationStatus == -1 && app != libopusApplicationAudio {
					t.Fatalf("libopus child %d application=%d after rejected broadcast, want Audio", i, app)
				}
			}

			fullPacket := make([]byte, 4000)
			fullN, err := enc.Encode(tc.pcm, fullPacket)
			if err != nil {
				t.Fatalf("full-budget Encode: %v", err)
			}
			assertMultistreamApplicationPacket(t, "full-budget encode", multistreamApplicationPacket{
				status: int32(fullN), rangeStatus: 0, finalRange: enc.GetFinalRange(), bytes: fullPacket[:fullN],
			}, want.fullPacket)
		})
	}
}

func libopusApplicationCode(application Application) int32 {
	switch application {
	case ApplicationVoIP:
		return libopusApplicationVoIP
	case ApplicationAudio:
		return libopusApplicationAudio
	case ApplicationLowDelay:
		return libopusApplicationLowDelay
	default:
		return -1
	}
}

func assertMultistreamApplicationPacket(t *testing.T, label string, got, want multistreamApplicationPacket) {
	t.Helper()
	if got.status != want.status || got.rangeStatus != want.rangeStatus || got.finalRange != want.finalRange || !bytes.Equal(got.bytes, want.bytes) {
		t.Fatalf("%s differs from libopus: Go status=%d len=%d range=%08x; C status=%d len=%d range=%08x", label, got.status, len(got.bytes), got.finalRange, want.status, len(want.bytes), want.finalRange)
	}
}

func assertMultistreamCallerFloatBits(t *testing.T, got, want []float32) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("PCM length=%d want %d", len(got), len(want))
	}
	for i := range want {
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
			t.Fatalf("PCM[%d] bits=%08x want %08x", i, math.Float32bits(got[i]), math.Float32bits(want[i]))
		}
	}
}

func multistreamCallerHasSignalFloat32(pcm []float32) bool {
	for _, sample := range pcm {
		if sample != 0 && !math.IsNaN(float64(sample)) && !math.IsInf(float64(sample), 0) {
			return true
		}
	}
	return false
}

func multistreamCallerHasSignalInt16(pcm []int16) bool {
	for _, sample := range pcm {
		if sample != 0 {
			return true
		}
	}
	return false
}

func multistreamCallerHasSignalInt24(pcm []int32) bool {
	for _, sample := range pcm {
		if sample != 0 {
			return true
		}
	}
	return false
}

func TestMultistreamCallerBuffersMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const frameSize = 960
	const channels = 2
	const sentinel = uint32(0x7fc12345)
	for _, tc := range []struct {
		name   string
		packet func(*testing.T, int) []byte
	}{
		{"celt", encodeAPIRateCELTPacket},
		{"silk", encodeAPIRateSILKPacket},
		{"hybrid", encodeAPIRateHybridPacket},
	} {
		t.Run(tc.name, func(t *testing.T) {
			packet := tc.packet(t, channels)
			mapping := []byte{0, 1}
			packets := [][]byte{packet}
			want32, err := decodeLibopusMultistreamFloat32(48000, channels, 1, 1, frameSize, mapping, packets)
			if err != nil {
				libopustest.HelperUnavailable(t, "float32 multistream oracle", err)
			}
			want16, err := decodeLibopusMultistreamInt16Gain(48000, channels, 1, 1, frameSize, 0, mapping, packets)
			if err != nil {
				libopustest.HelperUnavailable(t, "int16 multistream oracle", err)
			}
			want24, err := decodeLibopusMultistreamInt24(48000, channels, 1, 1, frameSize, mapping, packets)
			if err != nil {
				libopustest.HelperUnavailable(t, "int24 multistream oracle", err)
			}

			floatDec := mustNewDefaultMultistreamDecoder(t, 48000, channels)
			floatPCM := make([]float32, frameSize*channels+1)
			for i := range floatPCM {
				floatPCM[i] = math.Float32frombits(sentinel)
			}
			n, err := floatDec.Decode(packet, floatPCM)
			if err != nil || n != frameSize {
				t.Fatalf("Decode=(%d,%v) want (%d,nil)", n, err, frameSize)
			}
			assertMultistreamCallerFloatBits(t, floatPCM[:n*channels], want32)
			if math.Float32bits(floatPCM[n*channels]) != sentinel {
				t.Fatal("Decode modified the incomplete-channel tail")
			}

			shortDec := mustNewDefaultMultistreamDecoder(t, 48000, channels)
			shortPCM := make([]int16, frameSize*channels+1)
			for i := range shortPCM {
				shortPCM[i] = 12345
			}
			n, err = shortDec.DecodeInt16(packet, shortPCM)
			if err != nil || n != frameSize {
				t.Fatalf("DecodeInt16=(%d,%v) want (%d,nil)", n, err, frameSize)
			}
			for i, want := range want16 {
				if shortPCM[i] != want {
					t.Fatalf("int16 PCM[%d]=%d want %d", i, shortPCM[i], want)
				}
			}
			if shortPCM[n*channels] != 12345 {
				t.Fatal("DecodeInt16 modified the incomplete-channel tail")
			}

			int24Dec := mustNewDefaultMultistreamDecoder(t, 48000, channels)
			int24PCM := make([]int32, frameSize*channels+1)
			for i := range int24PCM {
				int24PCM[i] = 1234567
			}
			n, err = int24Dec.DecodeInt24(packet, int24PCM)
			if err != nil || n != frameSize {
				t.Fatalf("DecodeInt24=(%d,%v) want (%d,nil)", n, err, frameSize)
			}
			for i, want := range want24 {
				if int24PCM[i] != want {
					t.Fatalf("int24 PCM[%d]=%d want %d", i, int24PCM[i], want)
				}
			}
			if int24PCM[n*channels] != 1234567 {
				t.Fatal("DecodeInt24 modified the incomplete-channel tail")
			}
		})
	}
}

func TestMultistreamCallerBufferWarmZeroAlloc(t *testing.T) {
	const frameSize = 960
	const channels = 2
	for _, tc := range []struct {
		name   string
		packet func(*testing.T, int) []byte
	}{
		{"celt", encodeAPIRateCELTPacket},
		{"silk", encodeAPIRateSILKPacket},
		{"hybrid", encodeAPIRateHybridPacket},
	} {
		t.Run(tc.name, func(t *testing.T) {
			packet := tc.packet(t, channels)
			floatDec := mustNewDefaultMultistreamDecoder(t, 48000, channels)
			int16Dec := mustNewDefaultMultistreamDecoder(t, 48000, channels)
			int24Dec := mustNewDefaultMultistreamDecoder(t, 48000, channels)
			floatPCM := make([]float32, frameSize*channels)
			int16PCM := make([]int16, frameSize*channels)
			int24PCM := make([]int32, frameSize*channels)
			for range 3 {
				if _, err := floatDec.Decode(packet, floatPCM); err != nil {
					t.Fatal(err)
				}
				if _, err := floatDec.Decode(nil, floatPCM); err != nil {
					t.Fatal(err)
				}
				if !multistreamCallerHasSignalFloat32(floatPCM) {
					t.Fatal("float PLC did not exercise active nonzero PCM")
				}
				if _, err := int16Dec.DecodeInt16(packet, int16PCM); err != nil {
					t.Fatal(err)
				}
				if _, err := int16Dec.DecodeInt16(nil, int16PCM); err != nil {
					t.Fatal(err)
				}
				if !multistreamCallerHasSignalInt16(int16PCM) {
					t.Fatal("int16 PLC did not exercise active nonzero PCM")
				}
				if _, err := int24Dec.DecodeInt24(packet, int24PCM); err != nil {
					t.Fatal(err)
				}
				if _, err := int24Dec.DecodeInt24(nil, int24PCM); err != nil {
					t.Fatal(err)
				}
				if !multistreamCallerHasSignalInt24(int24PCM) {
					t.Fatal("int24 PLC did not exercise active nonzero PCM")
				}
			}
			if got := testing.AllocsPerRun(50, func() {
				if n, err := floatDec.Decode(packet, floatPCM); err != nil || n != frameSize {
					t.Fatalf("float packet=(%d,%v)", n, err)
				}
				if n, err := floatDec.Decode(nil, floatPCM); err != nil || n != frameSize {
					t.Fatalf("float PLC=(%d,%v)", n, err)
				}
			}); got != 0 {
				t.Fatalf("warm float packet+active PLC allocations=%v want 0", got)
			}
			if !multistreamCallerHasSignalFloat32(floatPCM) {
				t.Fatal("measured float PLC did not exercise active nonzero PCM")
			}
			if got := testing.AllocsPerRun(50, func() {
				if n, err := int16Dec.DecodeInt16(packet, int16PCM); err != nil || n != frameSize {
					t.Fatalf("int16 packet=(%d,%v)", n, err)
				}
				if n, err := int16Dec.DecodeInt16(nil, int16PCM); err != nil || n != frameSize {
					t.Fatalf("int16 PLC=(%d,%v)", n, err)
				}
			}); got != 0 {
				t.Fatalf("warm int16 packet+active PLC allocations=%v want 0", got)
			}
			if !multistreamCallerHasSignalInt16(int16PCM) {
				t.Fatal("measured int16 PLC did not exercise active nonzero PCM")
			}
			if got := testing.AllocsPerRun(50, func() {
				if n, err := int24Dec.DecodeInt24(packet, int24PCM); err != nil || n != frameSize {
					t.Fatalf("int24 packet=(%d,%v)", n, err)
				}
				if n, err := int24Dec.DecodeInt24(nil, int24PCM); err != nil || n != frameSize {
					t.Fatalf("int24 PLC=(%d,%v)", n, err)
				}
			}); got != 0 {
				t.Fatalf("warm int24 packet+active PLC allocations=%v want 0", got)
			}
			if !multistreamCallerHasSignalInt24(int24PCM) {
				t.Fatal("measured int24 PLC did not exercise active nonzero PCM")
			}
		})
	}
}

func TestMultistreamCallerBufferMappingAndRetry(t *testing.T) {
	libopustest.RequireOracle(t)
	packet := encodeAPIRateSILKPacket(t, 2)
	mapping := []byte{0, 0, 1, 255}
	want, err := decodeLibopusMultistreamFloat32(48000, 4, 1, 1, 960, mapping, [][]byte{packet})
	if err != nil {
		libopustest.HelperUnavailable(t, "mapped multistream oracle", err)
	}
	dec, err := NewMultistreamDecoder(48000, 4, 1, 1, mapping)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := dec.Decode(packet, make([]float32, 960*4-1)); err != ErrBufferTooSmall {
		t.Fatalf("short Decode error=%v want %v", err, ErrBufferTooSmall)
	}
	const sentinel = uint32(0x7fc12345)
	pcm := make([]float32, 5760*4+3)
	for i := range pcm {
		pcm[i] = math.Float32frombits(sentinel)
	}
	n, err := dec.Decode(packet, pcm)
	if err != nil || n != 960 {
		t.Fatalf("Decode retry=(%d,%v) want (960,nil)", n, err)
	}
	assertMultistreamCallerFloatBits(t, pcm[:n*4], want)
	for i := n * 4; i < len(pcm); i++ {
		if math.Float32bits(pcm[i]) != sentinel {
			t.Fatalf("output tail[%d] was overwritten", i)
		}
	}
	seenSignal := false
	for s := range n {
		if math.Float32bits(pcm[s*4]) != math.Float32bits(pcm[s*4+1]) {
			t.Fatalf("duplicate mapping differs at sample %d", s)
		}
		if pcm[s*4+3] != 0 {
			t.Fatalf("muted channel[%d]=%v want zero", s, pcm[s*4+3])
		}
		if pcm[s*4] != 0 || pcm[s*4+2] != 0 {
			seenSignal = true
		}
	}
	if !seenSignal {
		t.Fatal("mapped packet did not exercise nonzero PCM")
	}
	fresh, err := NewMultistreamDecoder(48000, 4, 1, 1, mapping)
	if err != nil {
		t.Fatal(err)
	}
	freshPCM := make([]float32, n*4)
	if _, err := fresh.Decode(packet, freshPCM); err != nil {
		t.Fatal(err)
	}
	assertMultistreamCallerFloatBits(t, pcm[:n*4], freshPCM)
	if dec.GetFinalRange() != fresh.GetFinalRange() {
		t.Fatal("short-buffer retry changed final range")
	}
}

func TestMultistreamCallerBufferDTXAndOwnedOutput(t *testing.T) {
	libopustest.RequireOracle(t)
	packet := encodeAPIRateSILKPacket(t, 1)
	dtx := []byte{0x11} // two empty 40 ms SILK frames in one code-1 packet
	want, err := decodeLibopusMultistreamFloat32(48000, 1, 1, 0, 3840, []byte{0}, [][]byte{packet, dtx})
	if err != nil {
		libopustest.HelperUnavailable(t, "two-frame DTX oracle", err)
	}
	dec := mustNewDefaultMultistreamDecoder(t, 48000, 1)
	first := make([]float32, 960)
	if n, err := dec.Decode(packet, first); err != nil || n != 960 {
		t.Fatalf("good packet=(%d,%v)", n, err)
	}
	concealed := make([]float32, 3840)
	if n, err := dec.Decode(dtx, concealed); err != nil || n != 3840 {
		t.Fatalf("DTX packet=(%d,%v)", n, err)
	}
	assertMultistreamCallerFloatBits(t, concealed, want[960:])

	ownedDec, err := mspkg.NewDecoder(48000, 1, 1, 0, []byte{0})
	if err != nil {
		t.Fatal(err)
	}
	owned, err := ownedDec.DecodeToFloat32(packet, 960)
	if err != nil {
		t.Fatal(err)
	}
	saved := append([]float32(nil), owned...)
	twinDec, err := mspkg.NewDecoder(48000, 1, 1, 0, []byte{0})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := twinDec.DecodeToFloat32(packet, 960); err != nil {
		t.Fatal(err)
	}
	caller := make([]float32, 960)
	if _, err := ownedDec.DecodeIntoFloat32(nil, caller, 960); err != nil {
		t.Fatal(err)
	}
	twinCaller := make([]float32, 960)
	if _, err := twinDec.DecodeIntoFloat32(nil, twinCaller, 960); err != nil {
		t.Fatal(err)
	}
	for i := range caller {
		caller[i] = math.Float32frombits(0x7fc12345)
	}
	recovery := make([]float32, 960)
	twinRecovery := make([]float32, 960)
	if _, err := ownedDec.DecodeIntoFloat32(packet, recovery, 960); err != nil {
		t.Fatal(err)
	}
	if _, err := twinDec.DecodeIntoFloat32(packet, twinRecovery, 960); err != nil {
		t.Fatal(err)
	}
	assertMultistreamCallerFloatBits(t, recovery, twinRecovery)
	if ownedDec.FinalRange() != twinDec.FinalRange() {
		t.Fatal("mutating caller PCM changed recovery range")
	}
	ownedDec.Reset()
	assertMultistreamCallerFloatBits(t, owned, saved)
}

func TestMultistreamDecoder_Creation(t *testing.T) {
	// Test NewMultistreamDecoderDefault for channels 1-8
	for channels := 1; channels <= 8; channels++ {
		t.Run(string(rune('0'+channels))+"ch", func(t *testing.T) {
			dec := mustNewDefaultMultistreamDecoder(t, 48000, channels)

			if dec.Channels() != channels {
				t.Errorf("Channels() = %d, want %d", dec.Channels(), channels)
			}
			if dec.SampleRate() != 48000 {
				t.Errorf("SampleRate() = %d, want 48000", dec.SampleRate())
			}

			streams := dec.Streams()
			coupled := dec.CoupledStreams()
			t.Logf("%d channels: %d streams, %d coupled", channels, streams, coupled)
		})
	}

	// Test invalid sample rates
	_, err := NewMultistreamDecoderDefault(44100, 6)
	if err != ErrInvalidSampleRate {
		t.Errorf("Invalid sample rate: got error %v, want ErrInvalidSampleRate", err)
	}

	// Test invalid channels
	_, err = NewMultistreamDecoderDefault(48000, 0)
	if !errors.Is(err, ErrInvalidChannels) {
		t.Errorf("Zero channels: got error %v, want ErrInvalidChannels", err)
	}
}

func TestMultistreamDecoder_IgnoreExtensions(t *testing.T) {
	dec := mustNewDefaultMultistreamDecoder(t, 48000, 2)

	assertIgnoreExtensionsControls(t, dec)
}

func TestMultistreamDecoder_OptionalExtensionControls(t *testing.T) {
	dec := mustNewDefaultMultistreamDecoder(t, 48000, 2)

	assertOptionalDecoderControls(t, dec)
	if osce, ok := any(dec).(extraOSCEBWEControl); ok {
		if extsupport.OSCEBWERuntime {
			assertWorkingOSCEBWEControl(t, osce)
		} else {
			t.Fatal("non-OSCE-runtime build unexpectedly exposes OSCE BWE control")
		}
	} else if extsupport.OSCEBWERuntime {
		t.Fatal("OSCE runtime build does not expose OSCE BWE control")
	}
}

// TestMultistreamDecoder_PLC tests packet loss concealment.
func TestMultistreamDecoder_PLC(t *testing.T) {
	channels := 6
	sampleRate := 48000
	frameSize := 960

	enc := mustNewDefaultMultistreamEncoder(t, sampleRate, channels, ApplicationAudio)
	dec := mustNewDefaultMultistreamDecoder(t, sampleRate, channels)

	// Encode and decode first frame (establishes state)
	pcm1 := generateSurroundTestSignal(sampleRate, frameSize, channels)
	packet1, err := enc.EncodeFloat32(pcm1)
	if err != nil {
		t.Fatalf("First encode error: %v", err)
	}
	pcmOut := make([]float32, frameSize*channels)
	_, err = dec.Decode(packet1, pcmOut)
	if err != nil {
		t.Fatalf("First decode error: %v", err)
	}

	// Simulate packet loss - call Decode(nil, ...) for PLC
	n, err := dec.Decode(nil, pcmOut)
	if err != nil {
		t.Fatalf("PLC decode error: %v", err)
	}

	plcEnergy := computeEnergyFloat32(pcmOut[:n*channels])

	// PLC should produce some audio
	t.Logf("PLC: %d samples, energy=%.4f", n*channels, plcEnergy)

	// Verify output length
	expectedLen := frameSize * channels
	if n*channels != expectedLen {
		t.Errorf("PLC output length = %d, want %d", n*channels, expectedLen)
	}
}

// TestMultistreamDecoder_Reset tests decoder reset functionality.
func TestMultistreamDecoder_Reset(t *testing.T) {
	channels := 6
	sampleRate := 48000
	frameSize := 960

	enc := mustNewDefaultMultistreamEncoder(t, sampleRate, channels, ApplicationAudio)
	dec := mustNewDefaultMultistreamDecoder(t, sampleRate, channels)

	// Decode a few frames
	pcmOut := make([]float32, frameSize*channels)
	for i := range 3 {
		pcm := generateSurroundTestSignal(sampleRate, frameSize, channels)
		packet, _ := enc.EncodeFloat32(pcm)
		_, err := dec.Decode(packet, pcmOut)
		if err != nil {
			t.Fatalf("Pre-reset decode %d error: %v", i, err)
		}
	}

	// Reset
	dec.Reset()

	// Decode more frames after reset
	for i := range 3 {
		pcm := generateSurroundTestSignal(sampleRate, frameSize, channels)
		packet, _ := enc.EncodeFloat32(pcm)
		n, err := dec.Decode(packet, pcmOut)
		if err != nil {
			t.Fatalf("Post-reset decode %d error: %v", i, err)
		}
		if n == 0 {
			t.Errorf("Post-reset decode %d produced empty output", i)
		}
	}

	t.Log("Decoder reset verified: no crashes, decoding continues normally")
}

func TestMultistreamDecoder_Controls(t *testing.T) {
	mono := mustNewDefaultMultistreamDecoder(t, 48000, 1)
	if !mono.PhaseInversionDisabled() {
		t.Fatal("mono PhaseInversionDisabled()=false want true")
	}
	mono.SetPhaseInversionDisabled(false)
	mono.Reset()
	if mono.PhaseInversionDisabled() {
		t.Fatal("mono Reset changed phase inversion control")
	}

	stereo := mustNewDefaultMultistreamDecoder(t, 48000, 2)
	if stereo.PhaseInversionDisabled() {
		t.Fatal("stereo PhaseInversionDisabled()=true want false")
	}
	stereo.SetPhaseInversionDisabled(true)
	stereo.Reset()
	if !stereo.PhaseInversionDisabled() {
		t.Fatal("stereo Reset changed phase inversion control")
	}

	if err := stereo.SetGain(256); err != nil {
		t.Fatalf("SetGain(256) error: %v", err)
	}
	if got := stereo.Gain(); got != 256 {
		t.Fatalf("Gain()=%d want 256", got)
	}
	if err := stereo.SetGain(32768); err != ErrInvalidGain {
		t.Fatalf("SetGain(32768) error=%v want %v", err, ErrInvalidGain)
	}
	if got := stereo.Gain(); got != 256 {
		t.Fatalf("invalid SetGain changed Gain() to %d", got)
	}
	if got := stereo.Complexity(); got != 0 {
		t.Fatalf("Complexity() default=%d want 0", got)
	}
	if err := stereo.SetComplexity(6); err != nil {
		t.Fatalf("SetComplexity(6) error: %v", err)
	}
	if got := stereo.Complexity(); got != 6 {
		t.Fatalf("Complexity()=%d want 6", got)
	}
	if err := stereo.SetComplexity(-1); err != ErrInvalidComplexity {
		t.Fatalf("SetComplexity(-1) error=%v want %v", err, ErrInvalidComplexity)
	}
	if got := stereo.Complexity(); got != 6 {
		t.Fatalf("invalid SetComplexity changed Complexity() to %d", got)
	}
	stereo.Reset()
	if got := stereo.Complexity(); got != 6 {
		t.Fatalf("Reset changed Complexity() to %d", got)
	}
	if got := stereo.Bandwidth(); got != BandwidthFullband {
		t.Fatalf("Bandwidth()=%v want %v", got, BandwidthFullband)
	}
	if got := stereo.LastPacketDuration(); got != 0 {
		t.Fatalf("LastPacketDuration()=%d want 0", got)
	}
	if got := stereo.FinalRange(); got != 0 {
		t.Fatalf("FinalRange()=0x%08x before decode, want 0", got)
	}
	if got := stereo.GetFinalRange(); got != stereo.FinalRange() {
		t.Fatalf("GetFinalRange()=0x%08x FinalRange()=0x%08x", got, stereo.FinalRange())
	}
}

// TestMultistreamDecoder_ExplicitConstructor tests explicit decoder constructor with custom mapping.
func TestMultistreamDecoder_ExplicitConstructor(t *testing.T) {
	// Test creating decoder with explicit parameters (5.1 surround)
	sampleRate := 48000
	channels := 6
	streams := 4
	coupledStreams := 2
	mapping := []byte{0, 4, 1, 2, 3, 5} // Standard 5.1 mapping

	dec, err := NewMultistreamDecoder(sampleRate, channels, streams, coupledStreams, mapping)
	if err != nil {
		t.Fatalf("NewMultistreamDecoder error: %v", err)
	}

	if dec.Channels() != channels {
		t.Errorf("Channels() = %d, want %d", dec.Channels(), channels)
	}
	if dec.Streams() != streams {
		t.Errorf("Streams() = %d, want %d", dec.Streams(), streams)
	}
	if dec.CoupledStreams() != coupledStreams {
		t.Errorf("CoupledStreams() = %d, want %d", dec.CoupledStreams(), coupledStreams)
	}

	t.Logf("Explicit constructor decoder: %d channels, %d streams, %d coupled",
		channels, streams, coupledStreams)
}

func TestMultistreamEncodeShortOutputUsesPublicError(t *testing.T) {
	for _, channels := range []int{1, 2, 6} {
		for _, format := range []string{"float32", "int16", "int24"} {
			t.Run(fmt.Sprintf("%s/%dch", format, channels), func(t *testing.T) {
				enc, err := NewMultistreamEncoderDefault(48000, channels, ApplicationAudio)
				if err != nil {
					t.Fatal(err)
				}
				samples := enc.FrameSize() * channels
				pcm32 := make([]float32, samples)
				pcm16 := make([]int16, samples)
				pcm24 := make([]int32, samples)
				encode := func(out []byte) (int, error) {
					switch format {
					case "float32":
						return enc.Encode(pcm32, out)
					case "int16":
						return enc.EncodeInt16(pcm16, out)
					default:
						return enc.EncodeInt24(pcm24, out)
					}
				}
				// opus_multistream_encode_native requires one TOC per stream
				// and a self-delimited length for every stream except the last.
				minimum := enc.Streams()*2 - 1
				for size := 0; size < minimum; size++ {
					n, err := encode(make([]byte, size))
					if n != 0 || !errors.Is(err, ErrBufferTooSmall) {
						t.Fatalf("output size %d: n=%d err=%v; want 0, ErrBufferTooSmall", size, n, err)
					}
				}
			})
		}
	}
}

func TestMultistreamEncoder_Creation(t *testing.T) {
	// Test NewMultistreamEncoderDefault for channels 1-8
	for channels := 1; channels <= 8; channels++ {
		t.Run(string(rune('0'+channels))+"ch", func(t *testing.T) {
			enc := mustNewDefaultMultistreamEncoder(t, 48000, channels, ApplicationAudio)

			if enc.Channels() != channels {
				t.Errorf("Channels() = %d, want %d", enc.Channels(), channels)
			}
			if enc.SampleRate() != 48000 {
				t.Errorf("SampleRate() = %d, want 48000", enc.SampleRate())
			}

			// Verify stream counts based on channel configuration
			streams := enc.Streams()
			coupled := enc.CoupledStreams()
			t.Logf("%d channels: %d streams, %d coupled", channels, streams, coupled)

			// Sanity check: coupled <= streams
			if coupled > streams {
				t.Errorf("CoupledStreams(%d) > Streams(%d)", coupled, streams)
			}
		})
	}

	// Test invalid sample rates
	_, err := NewMultistreamEncoderDefault(44100, 6, ApplicationAudio)
	if err != ErrInvalidSampleRate {
		t.Errorf("Invalid sample rate: got error %v, want ErrInvalidSampleRate", err)
	}

	// Test invalid channels (0)
	_, err = NewMultistreamEncoderDefault(48000, 0, ApplicationAudio)
	if !errors.Is(err, ErrInvalidChannels) {
		t.Errorf("Zero channels: got error %v, want ErrInvalidChannels", err)
	}

	// Test invalid channels (>8 for default)
	_, err = NewMultistreamEncoderDefault(48000, 9, ApplicationAudio)
	if !errors.Is(err, ErrInvalidChannels) {
		t.Errorf("9 channels: got error %v, want ErrInvalidChannels", err)
	}

	_, err = NewMultistreamEncoderDefault(48000, 6, Application(99))
	if err != ErrInvalidApplication {
		t.Errorf("invalid application: got error %v, want ErrInvalidApplication", err)
	}
}

// TestMultistreamEncoder_Controls tests encoder control methods.
func TestMultistreamEncoder_Controls(t *testing.T) {
	channels := 6
	enc := mustNewDefaultMultistreamEncoder(t, 48000, channels, ApplicationAudio)
	var err error

	// Test application control
	if got := enc.Application(); got != ApplicationAudio {
		t.Fatalf("Application()=%v want=%v", got, ApplicationAudio)
	}
	if err := enc.SetApplication(ApplicationVoIP); err != nil {
		t.Fatalf("SetApplication(ApplicationVoIP) error: %v", err)
	}
	if got := enc.Application(); got != ApplicationVoIP {
		t.Fatalf("Application()=%v want=%v after SetApplication", got, ApplicationVoIP)
	}
	if err := enc.SetApplication(Application(-1)); err != ErrInvalidApplication {
		t.Fatalf("SetApplication(invalid) error=%v want=%v", err, ErrInvalidApplication)
	}
	if err := enc.SetApplication(ApplicationRestrictedSilk); err != ErrInvalidApplication {
		t.Fatalf("SetApplication(restricted silk) error=%v want=%v", err, ErrInvalidApplication)
	}
	if err := enc.SetApplication(ApplicationRestrictedCelt); err != ErrInvalidApplication {
		t.Fatalf("SetApplication(restricted celt) error=%v want=%v", err, ErrInvalidApplication)
	}

	// Test SetBitrate
	err = enc.SetBitrate(256000)
	if err != nil {
		t.Errorf("SetBitrate(256000) error: %v", err)
	}
	if enc.Bitrate() != 256000 {
		t.Errorf("Bitrate() = %d, want 256000", enc.Bitrate())
	}
	if err := enc.SetBitrate(750000 * channels); err != nil {
		t.Errorf("SetBitrate(max total) error: %v", err)
	}
	if got := enc.Bitrate(); got != 750000*channels {
		t.Errorf("Bitrate() = %d, want %d after max total bitrate", got, 750000*channels)
	}
	if err := enc.SetBitrate(750000*channels + 1); err != nil {
		t.Errorf("SetBitrate(above max total) error: %v", err)
	}
	if got := enc.Bitrate(); got != 750000*channels {
		t.Errorf("Bitrate() = %d, want %d after above-max clamp", got, 750000*channels)
	}
	if err := enc.SetBitrate(1); err != nil {
		t.Errorf("SetBitrate(1) error: %v", err)
	}
	if got := enc.Bitrate(); got != 500*channels {
		t.Errorf("Bitrate() = %d, want %d after minimum clamp", got, 500*channels)
	}
	if err := enc.SetBitrate(0); err != ErrInvalidBitrate {
		t.Errorf("SetBitrate(0) error = %v, want %v", err, ErrInvalidBitrate)
	}
	if err := enc.SetBitrate(BitrateAuto); err != nil {
		t.Errorf("SetBitrate(BitrateAuto) error: %v", err)
	}
	if got := enc.Bitrate(); got != BitrateAuto {
		t.Errorf("Bitrate() = %d, want BitrateAuto", got)
	}
	if err := enc.SetBitrate(BitrateMax); err != nil {
		t.Errorf("SetBitrate(BitrateMax) error: %v", err)
	}
	if got := enc.Bitrate(); got != BitrateMax {
		t.Errorf("Bitrate() = %d, want BitrateMax", got)
	}

	// Test SetComplexity
	if got := enc.Complexity(); got != 9 {
		t.Errorf("Complexity() default = %d, want 9", got)
	}
	err = enc.SetComplexity(8)
	if err != nil {
		t.Errorf("SetComplexity(8) error: %v", err)
	}
	if enc.Complexity() != 8 {
		t.Errorf("Complexity() = %d, want 8", enc.Complexity())
	}

	// Test invalid complexity
	err = enc.SetComplexity(11)
	if err != ErrInvalidComplexity {
		t.Errorf("SetComplexity(11) error = %v, want ErrInvalidComplexity", err)
	}

	// Test bitrate mode controls
	if got := enc.BitrateMode(); got != BitrateModeCVBR {
		t.Errorf("BitrateMode() = %v, want %v by default", got, BitrateModeCVBR)
	}
	if !enc.VBR() {
		t.Error("VBR() should be true by default")
	}
	if !enc.VBRConstraint() {
		t.Error("VBRConstraint() should be true by default")
	}
	if err := enc.SetBitrateMode(BitrateModeCBR); err != nil {
		t.Errorf("SetBitrateMode(BitrateModeCBR) error: %v", err)
	}
	if got := enc.BitrateMode(); got != BitrateModeCBR {
		t.Errorf("BitrateMode() = %v, want %v", got, BitrateModeCBR)
	}
	enc.SetVBR(true)
	if !enc.VBR() {
		t.Error("VBR() should be true after SetVBR(true)")
	}
	if got := enc.BitrateMode(); got != BitrateModeCVBR {
		t.Errorf("BitrateMode() = %v, want %v after SetVBR(true) with retained constraint", got, BitrateModeCVBR)
	}
	enc.SetVBRConstraint(true)
	if !enc.VBRConstraint() {
		t.Error("VBRConstraint() should be true after SetVBRConstraint(true)")
	}
	enc.SetVBRConstraint(false)
	if got := enc.BitrateMode(); got != BitrateModeVBR {
		t.Errorf("BitrateMode() = %v, want %v after SetVBRConstraint(false)", got, BitrateModeVBR)
	}
	enc.SetVBR(false)
	if got := enc.BitrateMode(); got != BitrateModeCBR {
		t.Errorf("BitrateMode() = %v, want %v after SetVBR(false)", got, BitrateModeCBR)
	}
	enc.SetVBR(true)
	if got := enc.BitrateMode(); got != BitrateModeVBR {
		t.Errorf("BitrateMode() = %v, want %v after re-enabling VBR with constraint=false", got, BitrateModeVBR)
	}
	if err := enc.SetBitrateMode(BitrateMode(99)); err != ErrInvalidBitrateMode {
		t.Errorf("SetBitrateMode(invalid) error = %v, want %v", err, ErrInvalidBitrateMode)
	}

	// Test SetFEC
	enc.SetFEC(true)
	if !enc.FECEnabled() {
		t.Error("FEC should be enabled")
	}
	enc.SetFEC(false)
	if enc.FECEnabled() {
		t.Error("FEC should be disabled")
	}
	if err := enc.SetInBandFEC(InBandFECMusicSafe); err != nil {
		t.Fatalf("SetInBandFEC(%d) error: %v", InBandFECMusicSafe, err)
	}
	if got := enc.InBandFEC(); got != InBandFECMusicSafe {
		t.Fatalf("InBandFEC()=%d want %d", got, InBandFECMusicSafe)
	}
	if !enc.FECEnabled() {
		t.Fatal("FECEnabled()=false want true")
	}
	if err := enc.SetInBandFEC(3); err != ErrInvalidFECConfig {
		t.Fatalf("SetInBandFEC(3) error=%v want %v", err, ErrInvalidFECConfig)
	}

	// Test SetDTX
	enc.SetDTX(true)
	if !enc.DTXEnabled() {
		t.Error("DTX should be enabled")
	}
	enc.SetDTX(false)
	if enc.DTXEnabled() {
		t.Error("DTX should be disabled")
	}

	// Test SetPacketLoss
	err = enc.SetPacketLoss(15)
	if err != nil {
		t.Errorf("SetPacketLoss(15) error: %v", err)
	}
	if enc.PacketLoss() != 15 {
		t.Errorf("PacketLoss() = %d, want 15", enc.PacketLoss())
	}
	err = enc.SetPacketLoss(101)
	if err != ErrInvalidPacketLoss {
		t.Errorf("SetPacketLoss(101) error = %v, want ErrInvalidPacketLoss", err)
	}

	// Test bandwidth control. Bandwidth() mirrors libopus OPUS_GET_BANDWIDTH
	// (st->bandwidth): it stays at the FULLBAND init default until an encode
	// decides the bandwidth and does not echo the SET request.
	if err := enc.SetBandwidth(BandwidthWideband); err != nil {
		t.Errorf("SetBandwidth(BandwidthWideband) error: %v", err)
	}
	if got := enc.Bandwidth(); got != BandwidthFullband {
		t.Errorf("Bandwidth() = %v, want %v (FULLBAND init default before encode)", got, BandwidthFullband)
	}
	if err := enc.SetBandwidth(Bandwidth(255)); err != ErrInvalidBandwidth {
		t.Errorf("SetBandwidth(invalid) error = %v, want %v", err, ErrInvalidBandwidth)
	}
	if err := enc.SetBandwidthAuto(); err != nil {
		t.Errorf("SetBandwidthAuto error: %v", err)
	}
	if got := enc.Bandwidth(); got != BandwidthFullband {
		t.Errorf("Bandwidth() after SetBandwidthAuto = %v, want %v", got, BandwidthFullband)
	}
	if err := enc.SetMaxBandwidth(BandwidthWideband); err != nil {
		t.Errorf("SetMaxBandwidth(BandwidthWideband) error: %v", err)
	}
	if got := enc.MaxBandwidth(); got != BandwidthWideband {
		t.Errorf("MaxBandwidth() = %v, want %v", got, BandwidthWideband)
	}
	if err := enc.SetMaxBandwidth(Bandwidth(255)); err != ErrInvalidBandwidth {
		t.Errorf("SetMaxBandwidth(invalid) error = %v, want %v", err, ErrInvalidBandwidth)
	}

	// Test frame size control
	if got := enc.FrameSize(); got != 960 {
		t.Errorf("FrameSize() = %d, want 960", got)
	}
	for _, size := range []int{120, 240, 480, 960, 1920, 2880, 3840, 4800, 5760} {
		if err := enc.SetFrameSize(size); err != nil {
			t.Errorf("SetFrameSize(%d) error: %v", size, err)
		}
		if got := enc.FrameSize(); got != size {
			t.Errorf("FrameSize() = %d, want %d", got, size)
		}
	}
	if err := enc.SetFrameSize(111); err != ErrInvalidFrameSize {
		t.Errorf("SetFrameSize(invalid) error = %v, want %v", err, ErrInvalidFrameSize)
	}
	if err := enc.SetFrameSize(960); err != nil {
		t.Errorf("SetFrameSize(960) error: %v", err)
	}

	if got := enc.ExpertFrameDuration(); got != ExpertFrameDurationArg {
		t.Errorf("ExpertFrameDuration() = %v, want %v", got, ExpertFrameDurationArg)
	}
	if err := enc.SetExpertFrameDuration(ExpertFrameDuration120Ms); err != nil {
		t.Errorf("SetExpertFrameDuration(120ms) error: %v", err)
	}
	if got := enc.FrameSize(); got != 960 {
		t.Errorf("FrameSize() after 120ms = %d, want 960", got)
	}
	if got := enc.ExpertFrameDuration(); got != ExpertFrameDuration120Ms {
		t.Errorf("ExpertFrameDuration() after 120ms = %v, want %v", got, ExpertFrameDuration120Ms)
	}
	if err := enc.SetExpertFrameDuration(ExpertFrameDurationArg); err != nil {
		t.Errorf("SetExpertFrameDuration(arg) error: %v", err)
	}
	if err := enc.SetExpertFrameDuration(ExpertFrameDuration(0)); err != nil {
		t.Errorf("SetExpertFrameDuration(invalid) error = %v, want nil", err)
	}
	if got := enc.ExpertFrameDuration(); got != ExpertFrameDuration(0) {
		t.Errorf("ExpertFrameDuration() after invalid value = %v, want 0", got)
	}
	if n, err := enc.Encode(generateSurroundTestSignal(48000, enc.FrameSize(), enc.Channels()), make([]byte, 4000)); n != 0 || err != ErrInvalidFrameSize {
		t.Errorf("Encode with invalid expert duration = (%d, %v), want (0, %v)", n, err, ErrInvalidFrameSize)
	}
	if err := enc.SetExpertFrameDuration(ExpertFrameDurationArg); err != nil {
		t.Errorf("SetExpertFrameDuration(arg restore) error: %v", err)
	}

	// Test force channels control
	for _, ch := range []int{1, -1} {
		if err := enc.SetForceChannels(ch); err != nil {
			t.Errorf("SetForceChannels(%d) error: %v", ch, err)
		}
		if got := enc.ForceChannels(); got != ch {
			t.Errorf("ForceChannels() = %d, want %d", got, ch)
		}
	}
	if err := enc.SetForceChannels(2); err != ErrInvalidForceChannels {
		t.Errorf("SetForceChannels(2) on layout with mono streams error = %v, want %v", err, ErrInvalidForceChannels)
	}
	if got := enc.ForceChannels(); got != 2 {
		t.Errorf("ForceChannels() after rejected stereo force = %d, want 2 from the first coupled stream", got)
	}
	if err := enc.SetForceChannels(0); err != ErrInvalidForceChannels {
		t.Errorf("SetForceChannels(0) error = %v, want %v", err, ErrInvalidForceChannels)
	}
	if got := enc.ForceChannels(); got != 2 {
		t.Errorf("ForceChannels() after invalid value = %d, want 2", got)
	}

	// Test prediction and phase inversion controls
	enc.SetPredictionDisabled(true)
	if !enc.PredictionDisabled() {
		t.Error("PredictionDisabled() should be true after SetPredictionDisabled(true)")
	}
	enc.SetPredictionDisabled(false)
	if enc.PredictionDisabled() {
		t.Error("PredictionDisabled() should be false after SetPredictionDisabled(false)")
	}

	enc.SetPhaseInversionDisabled(true)
	if !enc.PhaseInversionDisabled() {
		t.Error("PhaseInversionDisabled() should be true after SetPhaseInversionDisabled(true)")
	}
	enc.SetPhaseInversionDisabled(false)
	if enc.PhaseInversionDisabled() {
		t.Error("PhaseInversionDisabled() should be false after SetPhaseInversionDisabled(false)")
	}

	// Test signal hint control parity (libopus OPUS_SET_SIGNAL semantics).
	if err := enc.SetSignal(SignalVoice); err != nil {
		t.Errorf("SetSignal(SignalVoice) error: %v", err)
	}
	if got := enc.Signal(); got != SignalVoice {
		t.Errorf("Signal() = %v, want %v", got, SignalVoice)
	}
	if err := enc.SetSignal(SignalMusic); err != nil {
		t.Errorf("SetSignal(SignalMusic) error: %v", err)
	}
	if got := enc.Signal(); got != SignalMusic {
		t.Errorf("Signal() = %v, want %v", got, SignalMusic)
	}
	if err := enc.SetSignal(Signal(9999)); err != ErrInvalidSignal {
		t.Errorf("SetSignal(invalid) error = %v, want %v", err, ErrInvalidSignal)
	}

	// Encode a frame after setting controls to verify no errors
	frameSize := enc.FrameSize()
	pcm := generateSurroundTestSignal(48000, frameSize, channels)
	packet, err := enc.EncodeFloat32(pcm)
	if err != nil {
		t.Errorf("Encode after controls error: %v", err)
	}
	if len(packet) == 0 {
		t.Error("Encode after controls produced empty packet")
	}
	if enc.FinalRange() != enc.GetFinalRange() {
		t.Errorf("FinalRange() = %d, want %d", enc.FinalRange(), enc.GetFinalRange())
	}

	t.Logf("Controls verified: app=%v bitrate=%d complexity=%d mode=%v FEC=%v DTX=%v",
		enc.Application(), enc.Bitrate(), enc.Complexity(), enc.BitrateMode(), enc.FECEnabled(), enc.DTXEnabled())
}

func TestMultistreamEncoder_SetMode(t *testing.T) {
	enc := mustNewDefaultMultistreamEncoder(t, 48000, 6, ApplicationAudio)

	if got := enc.Mode(); got != EncoderModeAuto {
		t.Fatalf("Mode() default=%v want=%v", got, EncoderModeAuto)
	}
	for _, mode := range []EncoderMode{
		EncoderModeAuto,
		EncoderModeSILK,
		EncoderModeHybrid,
		EncoderModeCELT,
	} {
		if err := enc.SetMode(mode); err != nil {
			t.Fatalf("SetMode(%v) error: %v", mode, err)
		}
		if got := enc.Mode(); got != mode {
			t.Fatalf("Mode()=%v want=%v", got, mode)
		}
		if got := enc.enc.Mode(); got != mode {
			t.Fatalf("core Mode()=%v want=%v", got, mode)
		}
	}

	if err := enc.SetMode(EncoderMode(99)); err != ErrInvalidArgument {
		t.Fatalf("SetMode(invalid) error=%v want=%v", err, ErrInvalidArgument)
	}
	if got := enc.Mode(); got != EncoderModeCELT {
		t.Fatalf("Mode() after invalid=%v want=%v", got, EncoderModeCELT)
	}
	enc.Reset()
	if got := enc.Mode(); got != EncoderModeCELT {
		t.Fatalf("Mode() after Reset=%v want=%v", got, EncoderModeCELT)
	}
}

func TestMultistreamEncoder_ExpertFrameDurationSelectsEncodeFrame(t *testing.T) {
	enc, err := NewMultistreamEncoder(48000, 2, 1, 1, []byte{0, 1}, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewMultistreamEncoder error: %v", err)
	}
	if err := enc.SetFrameSize(5760); err != nil {
		t.Fatalf("SetFrameSize(5760) error: %v", err)
	}
	if err := enc.SetExpertFrameDuration(ExpertFrameDuration20Ms); err != nil {
		t.Fatalf("SetExpertFrameDuration(20ms) error: %v", err)
	}

	data := make([]byte, 4000)
	n, err := enc.Encode(generateSurroundTestSignal(48000, 5760, 2), data)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}
	if n == 0 {
		t.Fatalf("Encode returned empty packet")
	}
	if got := ParseTOC(data[0]).FrameSize; got != 960 {
		t.Fatalf("TOC frame size = %d, want 960 from fixed 20ms duration", got)
	}
}

func TestMultistreamEncoderForceChannelsStereoAllowedWhenAllStreamsCoupled(t *testing.T) {
	enc := mustNewDefaultMultistreamEncoder(t, 48000, 2, ApplicationAudio)

	if err := enc.SetForceChannels(2); err != nil {
		t.Fatalf("SetForceChannels(2) on all-coupled layout error: %v", err)
	}
	if got := enc.ForceChannels(); got != 2 {
		t.Fatalf("ForceChannels() = %d, want 2", got)
	}
}

func TestMultistreamEncoder_EncodeInt24(t *testing.T) {
	enc := mustNewDefaultMultistreamEncoder(t, 48000, 6, ApplicationAudio)

	pcm := generateSurroundTestSignalInt24(48000, enc.FrameSize(), enc.Channels())
	data := make([]byte, 4000*enc.Streams())

	n, err := enc.EncodeInt24(pcm, data)
	if err != nil {
		t.Fatalf("EncodeInt24 error: %v", err)
	}
	if n == 0 {
		t.Fatal("EncodeInt24 returned 0 bytes")
	}

	packet, err := enc.EncodeInt24Slice(pcm)
	if err != nil {
		t.Fatalf("EncodeInt24Slice error: %v", err)
	}
	if len(packet) == 0 {
		t.Fatal("EncodeInt24Slice returned empty packet")
	}
}

func TestMultistreamEncoder_EncodeInt24InvalidFrameSize(t *testing.T) {
	enc := mustNewDefaultMultistreamEncoder(t, 48000, 6, ApplicationAudio)

	short := make([]int32, enc.FrameSize()*enc.Channels()-1)
	data := make([]byte, 4000*enc.Streams())
	if _, err := enc.EncodeInt24(short, data); err != ErrInvalidFrameSize {
		t.Fatalf("EncodeInt24(short) error=%v want=%v", err, ErrInvalidFrameSize)
	}
	if _, err := enc.EncodeInt24Slice(short); err != ErrInvalidFrameSize {
		t.Fatalf("EncodeInt24Slice(short) error=%v want=%v", err, ErrInvalidFrameSize)
	}
}

func TestMultistreamEncoder_CVBRPacketEnvelope(t *testing.T) {
	enc := mustNewDefaultMultistreamEncoder(t, 48000, 6, ApplicationAudio)

	if got := enc.BitrateMode(); got != BitrateModeCVBR {
		t.Fatalf("BitrateMode() = %v, want %v", got, BitrateModeCVBR)
	}

	frameSize := 960
	pcm := generateSurroundTestSignal(48000, frameSize, 6)
	data := make([]byte, 4000*enc.Streams())

	for _, bitrate := range []int{128000, 256000, 384000} {
		if err := enc.SetBitrateMode(BitrateModeCVBR); err != nil {
			t.Fatalf("SetBitrateMode(CVBR) error: %v", err)
		}
		if err := enc.SetBitrate(bitrate); err != nil {
			t.Fatalf("SetBitrate(%d) error: %v", bitrate, err)
		}
		enc.Reset()

		for i := range 10 {
			n, err := enc.Encode(pcm, data)
			if err != nil {
				t.Fatalf("Encode bitrate=%d frame=%d error: %v", bitrate, i, err)
			}
			// opus_multistream_encode caps every elementary stream at 1275
			// bytes; the multistream packet as a whole may exceed that.
			rest := data[:n]
			for s := range enc.Streams() {
				streamLen := len(rest)
				if s < enc.Streams()-1 {
					packet, consumed, err := decodeSelfDelimitedPacket(rest)
					if err != nil {
						t.Fatalf("bitrate=%d frame=%d stream=%d: %v", bitrate, i, s, err)
					}
					streamLen = len(packet)
					rest = rest[consumed:]
				}
				if streamLen > 1275 {
					t.Fatalf("bitrate=%d frame=%d stream=%d packet=%d exceeds the 1275-byte stream limit", bitrate, i, s, streamLen)
				}
			}
		}
	}
}

// TestMultistreamEncoder_SetApplicationPreservesControls verifies application
// updates do not clobber other encoder CTLs.
func TestMultistreamEncoder_SetApplicationPreservesControls(t *testing.T) {
	enc := mustNewDefaultMultistreamEncoder(t, 48000, 6, ApplicationAudio)

	const wantBitrate = 210000
	const wantComplexity = 3

	if err := enc.SetBitrate(wantBitrate); err != nil {
		t.Fatalf("SetBitrate(%d) error: %v", wantBitrate, err)
	}
	if err := enc.SetComplexity(wantComplexity); err != nil {
		t.Fatalf("SetComplexity(%d) error: %v", wantComplexity, err)
	}
	if err := enc.SetSignal(SignalMusic); err != nil {
		t.Fatalf("SetSignal(SignalMusic) error: %v", err)
	}
	if err := enc.SetBandwidth(BandwidthSuperwideband); err != nil {
		t.Fatalf("SetBandwidth(SWB) error: %v", err)
	}
	if err := enc.SetForceChannels(1); err != nil {
		t.Fatalf("SetForceChannels(1) error: %v", err)
	}
	if err := enc.SetMode(EncoderModeCELT); err != nil {
		t.Fatalf("SetMode(CELT) error: %v", err)
	}

	if err := enc.SetApplication(ApplicationVoIP); err != nil {
		t.Fatalf("SetApplication(ApplicationVoIP) error: %v", err)
	}

	if got := enc.Bitrate(); got != wantBitrate {
		t.Fatalf("Bitrate() after SetApplication = %d, want %d", got, wantBitrate)
	}
	if got := enc.Complexity(); got != wantComplexity {
		t.Fatalf("Complexity() after SetApplication = %d, want %d", got, wantComplexity)
	}
	if got := enc.Signal(); got != SignalMusic {
		t.Fatalf("Signal() after SetApplication = %v, want %v", got, SignalMusic)
	}
	// Bandwidth() reports st->bandwidth (FULLBAND init default before encode);
	// SetApplication preserves the user bandwidth request but does not change
	// the decided value reported here.
	if got := enc.Bandwidth(); got != BandwidthFullband {
		t.Fatalf("Bandwidth() after SetApplication = %v, want %v (FULLBAND init default before encode)", got, BandwidthFullband)
	}
	if got := enc.ForceChannels(); got != 1 {
		t.Fatalf("ForceChannels() after SetApplication = %d, want 1", got)
	}
	if got := enc.Mode(); got != EncoderModeCELT {
		t.Fatalf("Mode() after SetApplication = %v, want %v", got, EncoderModeCELT)
	}
}

func TestMultistreamEncoder_SetApplicationUpdatesApplicationPolicy(t *testing.T) {
	enc := mustNewDefaultMultistreamEncoder(t, 48000, 6, ApplicationAudio)

	if got := enc.enc.Mode(); got != encodercore.ModeAuto {
		t.Fatalf("initial Mode() = %v, want %v", got, encodercore.ModeAuto)
	}
	if enc.enc.LowDelay() {
		t.Fatalf("initial LowDelay() = true, want false")
	}
	if got := enc.Bandwidth(); got != BandwidthFullband {
		t.Fatalf("initial Bandwidth() = %v, want %v", got, BandwidthFullband)
	}

	if err := enc.SetApplication(ApplicationVoIP); err != nil {
		t.Fatalf("SetApplication(ApplicationVoIP) error: %v", err)
	}
	if got := enc.enc.Mode(); got != encodercore.ModeAuto {
		t.Fatalf("Mode() after VoIP = %v, want %v", got, encodercore.ModeAuto)
	}
	if enc.enc.LowDelay() {
		t.Fatalf("LowDelay() after VoIP = true, want false")
	}
	if got := enc.Bandwidth(); got != BandwidthFullband {
		t.Fatalf("Bandwidth() after VoIP = %v, want preserved %v", got, BandwidthFullband)
	}

	if err := enc.SetApplication(ApplicationLowDelay); err != nil {
		t.Fatalf("SetApplication(ApplicationLowDelay) error: %v", err)
	}
	if got := enc.enc.Mode(); got != encodercore.ModeAuto {
		t.Fatalf("Mode() after LowDelay = %v, want preserved %v", got, encodercore.ModeAuto)
	}
	if !enc.enc.LowDelay() {
		t.Fatalf("LowDelay() after LowDelay app = false, want true")
	}
	if got := enc.Bandwidth(); got != BandwidthFullband {
		t.Fatalf("Bandwidth() after LowDelay = %v, want %v", got, BandwidthFullband)
	}
}

func TestMultistreamEncoder_SetApplicationFromLowDelayRestoresDefaultModeUnlessForced(t *testing.T) {
	enc := mustNewDefaultMultistreamEncoder(t, 48000, 6, ApplicationLowDelay)
	if err := enc.SetApplication(ApplicationAudio); err != nil {
		t.Fatalf("SetApplication(ApplicationAudio) error: %v", err)
	}
	if got := enc.Mode(); got != EncoderModeAuto {
		t.Fatalf("Mode()=%v want %v", got, EncoderModeAuto)
	}
	if enc.enc.LowDelay() {
		t.Fatal("LowDelay()=true after switching to audio, want false")
	}

	forced := mustNewDefaultMultistreamEncoder(t, 48000, 6, ApplicationLowDelay)
	if err := forced.SetMode(EncoderModeCELT); err != nil {
		t.Fatalf("SetMode(CELT) error: %v", err)
	}
	if err := forced.SetApplication(ApplicationAudio); err != nil {
		t.Fatalf("SetApplication(ApplicationAudio) with forced mode error: %v", err)
	}
	if got := forced.Mode(); got != EncoderModeCELT {
		t.Fatalf("forced Mode()=%v want %v", got, EncoderModeCELT)
	}
}

func TestMultistreamEncoder_SetApplicationAfterEncodeRejected(t *testing.T) {
	enc := mustNewDefaultMultistreamEncoder(t, 48000, 6, ApplicationAudio)

	pcm := generateSurroundTestSignal(48000, 960, 6)
	packet := make([]byte, 4000*enc.Streams())
	assertApplicationLockAfterEncode(
		t,
		enc.Application,
		enc.SetApplication,
		enc.Reset,
		func() error {
			_, err := enc.Encode(pcm, packet)
			return err
		},
		ApplicationVoIP,
		ApplicationVoIP,
	)
}

func TestMultistreamEncoder_RestrictedApplications(t *testing.T) {
	for _, tt := range restrictedApplicationTestCases() {
		t.Run(tt.name, func(t *testing.T) {
			enc := mustNewDefaultMultistreamEncoder(t, 48000, 6, tt.application)

			if got := enc.Application(); got != tt.application {
				t.Fatalf("Application()=%v want=%v", got, tt.application)
			}
			if got := enc.enc.Mode(); got != tt.wantMode {
				t.Fatalf("Mode()=%v want=%v", got, tt.wantMode)
			}
			if got := enc.enc.LowDelay(); got != tt.wantLowDelay {
				t.Fatalf("LowDelay()=%v want=%v", got, tt.wantLowDelay)
			}
			if got := enc.Bandwidth(); got != tt.wantBandwidth {
				t.Fatalf("Bandwidth()=%v want=%v", got, tt.wantBandwidth)
			}
			if got := enc.Lookahead(); got != tt.wantLookahead {
				t.Fatalf("Lookahead()=%d want=%d", got, tt.wantLookahead)
			}

			if err := enc.SetApplication(tt.application); err != ErrInvalidApplication {
				t.Fatalf("SetApplication(same restricted) error=%v want=%v", err, ErrInvalidApplication)
			}
			if err := enc.SetApplication(ApplicationAudio); err != ErrInvalidApplication {
				t.Fatalf("SetApplication(change restricted) error=%v want=%v", err, ErrInvalidApplication)
			}
			if tt.application == ApplicationRestrictedSilk {
				if err := enc.SetFrameSize(240); err != ErrInvalidFrameSize {
					t.Fatalf("SetFrameSize(240) error=%v want=%v", err, ErrInvalidFrameSize)
				}
				if err := enc.SetFrameSize(480); err != nil {
					t.Fatalf("SetFrameSize(480) error: %v", err)
				}
			}
		})
	}
}

func TestMultistreamEncoder_OptionalExtensionControls(t *testing.T) {
	enc := mustNewDefaultMultistreamEncoder(t, 48000, 2, ApplicationAudio)

	assertOptionalEncoderControls(t, enc)
	dred, ok := any(enc).(extraDREDControl)
	if extsupport.DRED {
		if !ok {
			t.Fatal("gopus_dred build does not expose DRED control")
		}
		assertWorkingDREDControl(t, dred)
	} else if extsupport.DREDRuntime {
		if !ok {
			t.Fatal("DRED runtime build does not expose DRED control")
		}
		assertWorkingDREDControl(t, dred)
	} else if ok {
		t.Fatal("non-DRED-runtime build unexpectedly exposes DRED control")
	}
	qext, ok := any(enc).(qextEncoderControl)
	if extsupport.QEXT {
		if !ok {
			t.Fatal("QEXT build does not expose multistream encoder QEXT control")
		}
		assertSupportedQEXTControl(t, qext)
	} else if ok {
		t.Fatal("non-QEXT build unexpectedly exposes multistream encoder QEXT control")
	}
}

// TestMultistreamEncoder_Reset tests encoder reset functionality.
func TestMultistreamEncoder_Reset(t *testing.T) {
	channels := 6
	sampleRate := 48000
	frameSize := 960

	enc := mustNewDefaultMultistreamEncoder(t, sampleRate, channels, ApplicationAudio)

	// Encode a few frames
	for i := range 3 {
		pcm := generateSurroundTestSignal(sampleRate, frameSize, channels)
		_, err := enc.EncodeFloat32(pcm)
		if err != nil {
			t.Fatalf("Pre-reset encode %d error: %v", i, err)
		}
	}

	// Reset
	enc.Reset()

	// Encode more frames after reset
	for i := range 3 {
		pcm := generateSurroundTestSignal(sampleRate, frameSize, channels)
		packet, err := enc.EncodeFloat32(pcm)
		if err != nil {
			t.Fatalf("Post-reset encode %d error: %v", i, err)
		}
		if len(packet) == 0 {
			t.Errorf("Post-reset encode %d produced empty packet", i)
		}
	}

	t.Log("Encoder reset verified: no crashes, encoding continues normally")
}

// TestMultistreamEncoder_ExplicitConstructor tests explicit encoder constructor with custom mapping.
func TestMultistreamEncoder_ExplicitConstructor(t *testing.T) {
	// Test creating encoder with explicit parameters (5.1 surround)
	sampleRate := 48000
	channels := 6
	streams := 4
	coupledStreams := 2
	mapping := []byte{0, 4, 1, 2, 3, 5} // Standard 5.1 mapping

	enc, err := NewMultistreamEncoder(sampleRate, channels, streams, coupledStreams, mapping, ApplicationAudio)
	if err != nil {
		t.Fatalf("NewMultistreamEncoder error: %v", err)
	}

	if enc.Channels() != channels {
		t.Errorf("Channels() = %d, want %d", enc.Channels(), channels)
	}
	if enc.Streams() != streams {
		t.Errorf("Streams() = %d, want %d", enc.Streams(), streams)
	}
	if enc.CoupledStreams() != coupledStreams {
		t.Errorf("CoupledStreams() = %d, want %d", enc.CoupledStreams(), coupledStreams)
	}

	// Verify encoding works
	frameSize := 960
	pcm := generateSurroundTestSignal(sampleRate, frameSize, channels)
	packet, err := enc.EncodeFloat32(pcm)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}
	if len(packet) == 0 {
		t.Error("Explicit constructor encoder produced empty packet")
	}

	t.Logf("Explicit constructor: %d channels, %d streams, %d coupled, packet=%d bytes",
		channels, streams, coupledStreams, len(packet))
}

func TestMultistreamEncoder_Lookahead(t *testing.T) {
	for _, tc := range lookaheadTestCases() {
		t.Run(tc.name, func(t *testing.T) {
			enc := mustNewDefaultMultistreamEncoder(t, tc.sampleRate, 6, tc.application)
			if got := enc.Lookahead(); got != tc.want {
				t.Fatalf("Lookahead() = %d, want %d", got, tc.want)
			}
		})
	}

	t.Run("set_application_updates_lookahead_before_encode", func(t *testing.T) {
		enc := mustNewDefaultMultistreamEncoder(t, 48000, 6, ApplicationAudio)
		assertLookaheadUpdatesBeforeEncode(t, enc.Lookahead, enc.SetApplication)
	})
}

var multistreamForceChannelsStateHelper libopustest.HelperCache

type forceChannelsState struct {
	first     int32
	children  []int32
	getStatus int32
	childCode []int32
}

type forceChannelsPacket struct {
	status     int32
	rangeCode  int32
	finalRange uint32
	bytes      []byte
}

func TestMultistreamForceChannelsPartialFailureMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		sampleRate = 48000
		channels   = 3
		frameSize  = sampleRate / 50
		frameCount = 3
		maxBytes   = 8000
	)

	helper, err := multistreamForceChannelsStateHelper.Path(func() (string, error) {
		return libopustest.BuildPublicAPIHelper(libopustest.CHelperConfig{
			Label:      "multistream force channels partial failure",
			OutputBase: "gopus_libopus_multistream_force_channels_state",
			SourceFile: "libopus_multistream_force_channels_state.c",
			CFlags:     []string{"-O3", "-DNDEBUG"},
		})
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "multistream force channels state", err)
	}
	for _, complexity := range []int{7, 9, 10} {
		t.Run(fmt.Sprintf("complexity_%d", complexity), func(t *testing.T) {
			frames := forceChannelsStatePCM(channels, frameSize, frameCount)
			input := libopustest.NewOraclePayloadVersion("GFCI", 2, uint32(complexity), frameSize, frameCount)
			for _, frame := range frames {
				for _, sample := range frame {
					input.I16(sample)
				}
			}

			reader, err := libopustest.RunOracleVersion(helper, input.Bytes(), "multistream force channels state", "GFCO", 2)
			if err != nil {
				t.Fatalf("run libopus force channels helper: %v", err)
			}
			if got := reader.U32(); got != 2 {
				t.Fatalf("libopus streams=%d, want 2", got)
			}
			if got := reader.U32(); got != 1 {
				t.Fatalf("libopus coupled streams=%d, want 1", got)
			}
			if got := reader.U32(); got != channels {
				t.Fatalf("libopus channels=%d, want %d", got, channels)
			}
			if got := reader.Bytes(channels); !bytes.Equal(got, []byte{0, 1, 2}) {
				t.Fatalf("libopus mapping=%v, want [0 1 2]", got)
			}
			initialState := readForceChannelsState(reader, 2)
			initialPacket := readForceChannelsPacket(reader)
			setStatus := reader.I32()
			failedState := readForceChannelsState(reader, 2)
			failedPacket := readForceChannelsPacket(reader)
			resetStatus := reader.I32()
			resetState := readForceChannelsState(reader, 2)
			recoveryStatus := reader.I32()
			recoveredState := readForceChannelsState(reader, 2)
			recoveredPacket := readForceChannelsPacket(reader)
			if err := reader.ExpectConsumed(); err != nil {
				t.Fatalf("parse libopus force channels result: %v", err)
			}
			if setStatus != -1 {
				t.Fatalf("libopus SetForceChannels(2) status=%d, want OPUS_BAD_ARG (-1)", setStatus)
			}
			assertForceChannelsState(t, "libopus before control", initialState, []int32{-1000, -1000})
			assertForceChannelsState(t, "libopus after failed stereo broadcast", failedState, []int32{2, -1000})
			assertForceChannelsState(t, "libopus after reset", resetState, []int32{2, -1000})
			assertForceChannelsState(t, "libopus after auto recovery", recoveredState, []int32{-1000, -1000})
			if resetStatus != 0 {
				t.Fatalf("libopus reset status=%d, want OPUS_OK", resetStatus)
			}
			if recoveryStatus != 0 {
				t.Fatalf("libopus SetForceChannels(OPUS_AUTO) status=%d, want OPUS_OK", recoveryStatus)
			}

			enc, err := NewMultistreamEncoder(sampleRate, channels, 2, 1, []byte{0, 1, 2}, ApplicationAudio)
			if err != nil {
				t.Fatalf("NewMultistreamEncoder: %v", err)
			}
			if err := enc.SetBitrate(256000); err != nil {
				t.Fatalf("SetBitrate(256000): %v", err)
			}
			if err := enc.SetComplexity(complexity); err != nil {
				t.Fatalf("SetComplexity(%d): %v", complexity, err)
			}
			if got := enc.ForceChannels(); got != -1 {
				t.Fatalf("ForceChannels() before control=%d, want -1", got)
			}
			goInitialPacket := encodeForceChannelsFrame(t, enc, frames[0], maxBytes)
			assertForceChannelsPacket(t, "before control", goInitialPacket, initialPacket)

			if got := enc.SetForceChannels(2); got != ErrInvalidForceChannels {
				t.Fatalf("SetForceChannels(2) error=%v, want %v", got, ErrInvalidForceChannels)
			}
			if got := enc.ForceChannels(); got != 2 {
				t.Fatalf("ForceChannels() after failed broadcast=%d, want 2 from first coupled stream", got)
			}
			goFailedPacket := encodeForceChannelsFrame(t, enc, frames[1], maxBytes)
			assertForceChannelsPacket(t, "after failed broadcast", goFailedPacket, failedPacket)

			enc.Reset()
			if got := enc.ForceChannels(); got != 2 {
				t.Fatalf("ForceChannels() after reset=%d, want 2", got)
			}
			if err := enc.SetForceChannels(-1); err != nil {
				t.Fatalf("SetForceChannels(-1) recovery: %v", err)
			}
			if got := enc.ForceChannels(); got != -1 {
				t.Fatalf("ForceChannels() after recovery=%d, want -1", got)
			}
			goRecoveredPacket := encodeForceChannelsFrame(t, enc, frames[2], maxBytes)
			assertForceChannelsPacket(t, "after reset and recovery", goRecoveredPacket, recoveredPacket)
		})
	}
}

func readForceChannelsState(reader *libopustest.OracleReader, streams int) forceChannelsState {
	state := forceChannelsState{
		getStatus: reader.I32(),
		first:     reader.I32(),
		children:  make([]int32, streams),
		childCode: make([]int32, streams),
	}
	if got := reader.Count(streams); got != streams {
		return state
	}
	for i := range streams {
		state.childCode[i] = reader.I32()
		state.children[i] = reader.I32()
	}
	return state
}

func assertForceChannelsState(t *testing.T, label string, got forceChannelsState, want []int32) {
	t.Helper()
	if got.getStatus != 0 {
		t.Fatalf("%s first-child GET status=%d, want OPUS_OK", label, got.getStatus)
	}
	if len(got.children) != len(want) {
		t.Fatalf("%s child count=%d, want %d", label, len(got.children), len(want))
	}
	if got.first != want[0] {
		t.Fatalf("%s first-child force channels=%d, want %d", label, got.first, want[0])
	}
	for i := range want {
		if got.childCode[i] != 0 {
			t.Fatalf("%s child %d GET status=%d, want OPUS_OK", label, i, got.childCode[i])
		}
		if got.children[i] != want[i] {
			t.Fatalf("%s child %d force channels=%d, want %d", label, i, got.children[i], want[i])
		}
	}
}

func readForceChannelsPacket(reader *libopustest.OracleReader) forceChannelsPacket {
	packet := forceChannelsPacket{
		status:     reader.I32(),
		rangeCode:  reader.I32(),
		finalRange: reader.U32(),
	}
	n := reader.Count(-1)
	packet.bytes = append([]byte(nil), reader.Bytes(n)...)
	return packet
}

func encodeForceChannelsFrame(t *testing.T, enc *MultistreamEncoder, pcm []int16, maxBytes int) forceChannelsPacket {
	t.Helper()
	out := make([]byte, maxBytes)
	n, err := enc.EncodeInt16(pcm, out)
	if err != nil {
		t.Fatalf("EncodeInt16: %v", err)
	}
	return forceChannelsPacket{
		status:     int32(n),
		rangeCode:  0,
		finalRange: enc.GetFinalRange(),
		bytes:      append([]byte(nil), out[:n]...),
	}
}

func assertForceChannelsPacket(t *testing.T, label string, got, want forceChannelsPacket) {
	t.Helper()
	if want.status <= 0 || want.rangeCode != 0 {
		t.Fatalf("libopus %s encode status/range status=%d rangeStatus=%d", label, want.status, want.rangeCode)
	}
	if got.status != want.status {
		t.Fatalf("%s packet length=%d, want libopus %d (final range Go/C=%08x/%08x)",
			label, got.status, want.status, got.finalRange, want.finalRange)
	}
	if got.finalRange != want.finalRange {
		t.Fatalf("%s final range=%08x, want libopus %08x", label, got.finalRange, want.finalRange)
	}
	if !bytes.Equal(got.bytes, want.bytes) {
		diff := firstForceChannelsByteDifference(got.bytes, want.bytes)
		t.Fatalf("%s packet differs from libopus: Go=%d bytes C=%d bytes first difference %s", label, len(got.bytes), len(want.bytes), diff)
	}
}

func firstForceChannelsByteDifference(got, want []byte) string {
	limit := min(len(got), len(want))
	for i := range limit {
		if got[i] != want[i] {
			return fmt.Sprintf("at %d Go=%02x C=%02x", i, got[i], want[i])
		}
	}
	return fmt.Sprintf("at end Go=%d C=%d", len(got), len(want))
}

func forceChannelsStatePCM(channels, frameSize, frameCount int) [][]int16 {
	frames := make([][]int16, frameCount)
	for frame := range frameCount {
		pcm := make([]int16, channels*frameSize)
		for sample := range frameSize {
			for channel := range channels {
				value := (sample*(97+channel*34) + frame*7919 + channel*11939) % 60001
				pcm[sample*channels+channel] = int16(value - 30000)
			}
		}
		frames[frame] = pcm
	}
	return frames
}

var multistreamShortRefHelper libopustest.HelperCache
var multistreamExplicitShortRefHelper libopustest.HelperCache

type multistreamShortRef struct {
	streams, coupled int
	mapping          []byte
	packets          [][]byte
	ranges           []uint32
}

func encodeLibopusMultistreamShort(sampleRate, channels, family, bitrate, frameSize, frameCount, maxBytes int, vbr bool, pcm []int16) (*multistreamShortRef, error) {
	bin, err := multistreamShortRefHelper.Path(func() (string, error) {
		return libopustest.BuildPublicAPIHelper(libopustest.CHelperConfig{
			Label:       "multistream short reference encode",
			OutputBase:  "gopus_libopus_refencode_multistream_short",
			SourceFile:  "libopus_refencode_multistream.c",
			CFlags:      []string{"-O3", "-DNDEBUG"},
			RefIncludes: []string{"celt", "src"},
		})
	})
	if err != nil {
		return nil, err
	}
	var useVBR uint32
	if vbr {
		useVBR = 1
	}
	bandwidthAuto := int32(-1000)
	payload := libopustest.NewOraclePayloadVersion("GMEI", 2,
		uint32(sampleRate), uint32(channels), uint32(family), 2049,
		uint32(bitrate), useVBR, 1, 10, uint32(bandwidthAuto),
		uint32(frameSize), uint32(frameCount), uint32(maxBytes), 1)
	for _, sample := range pcm {
		payload.I16(sample)
	}
	reader, err := libopustest.RunOracleVersion(bin, payload.Bytes(), "multistream short reference encode", "GMEO", 2)
	if err != nil {
		return nil, err
	}
	ref := &multistreamShortRef{streams: int(reader.U32()), coupled: int(reader.U32())}
	if got := int(reader.U32()); got != channels {
		return nil, fmt.Errorf("reference channels=%d want %d", got, channels)
	}
	ref.mapping = append([]byte(nil), reader.Bytes(channels)...)
	count := reader.Count(frameCount)
	ref.packets = make([][]byte, count)
	ref.ranges = make([]uint32, count)
	for frame := range count {
		ref.ranges[frame] = reader.U32()
		n := int(reader.U32())
		ref.packets[frame] = append([]byte(nil), reader.Bytes(n)...)
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return ref, nil
}

func encodeLibopusExplicitMultistreamShort(sampleRate, channels, streams, coupled, bitrate, frameSize, frameCount, maxBytes int, vbr bool, mapping []byte, pcm []int16) (*multistreamShortRef, error) {
	bin, err := multistreamExplicitShortRefHelper.Path(func() (string, error) {
		return libopustest.BuildPublicAPIHelper(libopustest.CHelperConfig{
			Label:      "explicit multistream short reference encode",
			OutputBase: "gopus_libopus_multistream_short_explicit",
			SourceFile: "libopus_multistream_short_explicit_oracle.c",
			CFlags:     []string{"-O3", "-DNDEBUG"},
		})
	})
	if err != nil {
		return nil, err
	}
	var useVBR uint32
	if vbr {
		useVBR = 1
	}
	bandwidthAuto := int32(-1000)
	payload := libopustest.NewOraclePayloadVersion("GMSI", 1,
		uint32(sampleRate), uint32(channels), uint32(streams), uint32(coupled), 2049,
		uint32(bitrate), useVBR, 1, 10, uint32(bandwidthAuto),
		uint32(frameSize), uint32(frameCount), uint32(maxBytes))
	payload.Raw(mapping)
	for _, sample := range pcm {
		payload.I16(sample)
	}
	reader, err := libopustest.RunOracleVersion(bin, payload.Bytes(), "explicit multistream short reference encode", "GMSO", 1)
	if err != nil {
		return nil, err
	}
	count := reader.Count(frameCount)
	ref := &multistreamShortRef{streams: streams, coupled: coupled, mapping: append([]byte(nil), mapping...)}
	ref.packets = make([][]byte, count)
	ref.ranges = make([]uint32, count)
	for frame := range count {
		ref.ranges[frame] = reader.U32()
		n := int(reader.U32())
		ref.packets[frame] = append([]byte(nil), reader.Bytes(n)...)
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return ref, nil
}

func publicMultistreamShortSweep(channels, frameSize, frameCount int) []int16 {
	pcm := make([]int16, channels*frameSize*frameCount)
	for sample := range frameSize * frameCount {
		tm := float64(sample) / 48000
		amp := 0.25 + 0.1*math.Sin(2*math.Pi*1.5*tm)
		for ch := range channels {
			v := amp * math.Sin(2*math.Pi*110*float64(ch+1)*tm)
			pcm[sample*channels+ch] = int16(math.Round(v * 32768))
		}
	}
	return pcm
}

func newPublicShortMultistream(t *testing.T, channels, frameSize, bitrate int, vbr bool) *MultistreamEncoder {
	t.Helper()
	var enc *MultistreamEncoder
	var err error
	if channels <= 8 {
		enc, err = NewMultistreamEncoderDefault(48000, channels, ApplicationAudio)
	} else {
		mapping := make([]byte, channels)
		for i := range mapping {
			mapping[i] = byte(i)
		}
		enc, err = NewMultistreamEncoder(48000, channels, channels/2, channels/2, mapping, ApplicationAudio)
	}
	if err != nil {
		t.Fatal(err)
	}
	if err := enc.SetFrameSize(frameSize); err != nil {
		t.Fatal(err)
	}
	if err := enc.SetBitrate(bitrate); err != nil {
		t.Fatal(err)
	}
	enc.SetVBR(vbr)
	enc.SetVBRConstraint(true)
	if err := enc.SetComplexity(10); err != nil {
		t.Fatal(err)
	}
	if err := enc.SetBandwidthAuto(); err != nil {
		t.Fatal(err)
	}
	return enc
}

func TestPublicMultistreamInt16MatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range []struct {
		channels, family, frameSize, frames, bitrate, maxBytes int
		vbr                                                    bool
	}{
		{6, 1, 960, 24, 192000, 4000, true},
	} {
		t.Run(fmt.Sprintf("ch%d_family%d", tc.channels, tc.family), func(t *testing.T) {
			pcm := publicMultistreamShortSweep(tc.channels, tc.frameSize, tc.frames)
			ref, err := encodeLibopusMultistreamShort(48000, tc.channels, tc.family, tc.bitrate, tc.frameSize, tc.frames, tc.maxBytes, tc.vbr, pcm)
			if err != nil {
				t.Fatalf("live C encode: %v", err)
			}
			enc := newPublicShortMultistream(t, tc.channels, tc.frameSize, tc.bitrate, tc.vbr)
			if enc.Streams() != ref.streams || enc.CoupledStreams() != ref.coupled {
				t.Fatalf("stream layout Go=%d/%d C=%d/%d", enc.Streams(), enc.CoupledStreams(), ref.streams, ref.coupled)
			}
			out := make([]byte, tc.maxBytes)
			for frame := range tc.frames {
				input := pcm[frame*tc.frameSize*tc.channels : (frame+1)*tc.frameSize*tc.channels]
				n, err := enc.EncodeInt16(input, out)
				if err != nil {
					t.Fatalf("frame %d: %v", frame, err)
				}
				if !bytes.Equal(out[:n], ref.packets[frame]) || enc.GetFinalRange() != ref.ranges[frame] {
					t.Errorf("frame %d: firstByte=%d GoLen=%d CLen=%d GoRange=%08x CRange=%08x", frame,
						firstPacketDifference(out[:n], ref.packets[frame]), n, len(ref.packets[frame]), enc.GetFinalRange(), ref.ranges[frame])
				}
			}
			last := pcm[(tc.frames-1)*tc.frameSize*tc.channels:]
			if _, err := enc.EncodeInt16(last, out); err != nil {
				t.Fatalf("warm short encode: %v", err)
			}
			if allocs := testing.AllocsPerRun(20, func() {
				if _, err := enc.EncodeInt16(last, out); err != nil {
					t.Fatalf("short encode: %v", err)
				}
			}); allocs != 0 {
				t.Fatalf("warm public short caller-buffer allocations=%g want 0", allocs)
			}
		})
	}
}

func TestPublicExplicitMultistreamInt16MatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const channels, streams, coupled, frameSize, frames, bitrate, maxBytes = 16, 8, 8, 960, 24, 384000, 4000
	mapping := make([]byte, channels)
	for i := range mapping {
		mapping[i] = byte(i)
	}
	pcm := publicMultistreamShortSweep(channels, frameSize, frames)
	ref, err := encodeLibopusExplicitMultistreamShort(48000, channels, streams, coupled, bitrate, frameSize, frames, maxBytes, true, mapping, pcm)
	if err != nil {
		t.Fatalf("live C explicit multistream encode: %v", err)
	}
	enc := newPublicShortMultistream(t, channels, frameSize, bitrate, true)
	out := make([]byte, maxBytes)
	for frame := range frames {
		input := pcm[frame*frameSize*channels : (frame+1)*frameSize*channels]
		n, err := enc.EncodeInt16(input, out)
		if err != nil {
			t.Fatalf("frame %d: %v", frame, err)
		}
		if !bytes.Equal(out[:n], ref.packets[frame]) || enc.GetFinalRange() != ref.ranges[frame] {
			t.Errorf("frame %d: firstByte=%d GoLen=%d CLen=%d GoRange=%08x CRange=%08x", frame,
				firstPacketDifference(out[:n], ref.packets[frame]), n, len(ref.packets[frame]), enc.GetFinalRange(), ref.ranges[frame])
		}
	}
	last := pcm[(frames-1)*frameSize*channels:]
	if _, err := enc.EncodeInt16(last, out); err != nil {
		t.Fatalf("warm short encode: %v", err)
	}
	if allocs := testing.AllocsPerRun(20, func() {
		if _, err := enc.EncodeInt16(last, out); err != nil {
			t.Fatalf("short encode: %v", err)
		}
	}); allocs != 0 {
		t.Fatalf("warm public explicit short caller-buffer allocations=%g want 0", allocs)
	}
}

func TestPublicMultistreamInt16BufferAndControlState(t *testing.T) {
	libopustest.RequireOracle(t)
	const channels, frameSize, frames, bitrate, maxBytes = 6, 960, 2, 192000, 4000
	pcm := publicMultistreamShortSweep(channels, frameSize, frames)
	ref, err := encodeLibopusMultistreamShort(48000, channels, 1, bitrate, frameSize, frames, maxBytes, true, pcm)
	if err != nil {
		t.Fatalf("live C encode: %v", err)
	}
	enc := newPublicShortMultistream(t, channels, frameSize, bitrate, true)
	first := pcm[:frameSize*channels]
	firstCopy := append([]int16(nil), first...)
	if n, err := enc.EncodeInt16(first, make([]byte, 1)); err == nil || n != 0 {
		t.Fatalf("short output: n=%d err=%v", n, err)
	}
	out1 := make([]byte, maxBytes)
	n1, err := enc.EncodeInt16(first, out1)
	if err != nil {
		t.Fatalf("encode after short output: %v", err)
	}
	if !bytes.Equal(out1[:n1], ref.packets[0]) || enc.GetFinalRange() != ref.ranges[0] {
		t.Fatalf("state after short output: firstByte=%d GoRange=%08x CRange=%08x",
			firstPacketDifference(out1[:n1], ref.packets[0]), enc.GetFinalRange(), ref.ranges[0])
	}
	if !slices.Equal(first, firstCopy) {
		t.Fatal("encoder modified caller input")
	}
	owned := append([]byte(nil), out1[:n1]...)
	out2 := make([]byte, maxBytes)
	n2, err := enc.EncodeInt16(pcm[frameSize*channels:], out2)
	if err != nil {
		t.Fatalf("second encode: %v", err)
	}
	if !bytes.Equal(out2[:n2], ref.packets[1]) || enc.GetFinalRange() != ref.ranges[1] {
		t.Fatalf("second packet: firstByte=%d GoRange=%08x CRange=%08x",
			firstPacketDifference(out2[:n2], ref.packets[1]), enc.GetFinalRange(), ref.ranges[1])
	}
	if !bytes.Equal(out1[:n1], owned) {
		t.Fatal("second encode changed the first caller output buffer")
	}

	for _, depth := range []int{8, 12, 24} {
		t.Run(fmt.Sprintf("lsb%d", depth), func(t *testing.T) {
			enc := newPublicShortMultistream(t, 2, frameSize, 128000, true)
			if err := enc.SetLSBDepth(depth); err != nil {
				t.Fatal(err)
			}
			short := publicMultistreamShortSweep(2, frameSize, 1)
			out := make([]byte, maxBytes)
			if _, err := enc.EncodeInt16(short, out); err != nil {
				t.Fatal(err)
			}
			if got := enc.LSBDepth(); got != depth {
				t.Fatalf("after short input LSB depth=%d want %d", got, depth)
			}
			floatPCM := make([]float32, len(short))
			for i, sample := range short {
				floatPCM[i] = float32(sample) / 32768
			}
			if _, err := enc.Encode(floatPCM, out); err != nil {
				t.Fatal(err)
			}
			if got := enc.LSBDepth(); got != depth {
				t.Fatalf("after float input LSB depth=%d want %d", got, depth)
			}
			if _, err := enc.EncodeInt16(short, out); err != nil {
				t.Fatal(err)
			}
			if got := enc.LSBDepth(); got != depth {
				t.Fatalf("after alternating input LSB depth=%d want %d", got, depth)
			}
		})
	}
}

func TestPublicMultistreamInt16LowSpaceMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const channels, frameSize, frames, bitrate, maxBytes = 8, 480, 5, 64000, 15
	pcm := publicMultistreamShortSweep(channels, frameSize, frames)
	ref, err := encodeLibopusMultistreamShort(48000, channels, 1, bitrate, frameSize, frames, maxBytes, false, pcm)
	if err != nil {
		t.Fatalf("live C encode: %v", err)
	}
	enc := newPublicShortMultistream(t, channels, frameSize, bitrate, false)
	out := make([]byte, maxBytes)
	for frame := range frames {
		input := pcm[frame*frameSize*channels : (frame+1)*frameSize*channels]
		n, err := enc.EncodeInt16(input, out)
		if err != nil {
			t.Fatalf("frame %d: %v", frame, err)
		}
		if !bytes.Equal(out[:n], ref.packets[frame]) || enc.GetFinalRange() != ref.ranges[frame] {
			t.Fatalf("frame %d: firstByte=%d GoLen=%d CLen=%d GoRange=%08x CRange=%08x", frame,
				firstPacketDifference(out[:n], ref.packets[frame]), n, len(ref.packets[frame]), enc.GetFinalRange(), ref.ranges[frame])
		}
	}
	last := pcm[(frames-1)*frameSize*channels:]
	if _, err := enc.EncodeInt16(last, out); err != nil {
		t.Fatalf("warm low-space encode: %v", err)
	}
	if allocs := testing.AllocsPerRun(20, func() {
		if _, err := enc.EncodeInt16(last, out); err != nil {
			t.Fatalf("low-space encode: %v", err)
		}
	}); allocs != 0 {
		t.Fatalf("warm low-space caller-buffer allocations=%g want 0", allocs)
	}
}

func firstPacketDifference(got, want []byte) int {
	for i := range min(len(got), len(want)) {
		if got[i] != want[i] {
			return i
		}
	}
	if len(got) != len(want) {
		return min(len(got), len(want))
	}
	return -1
}

const libopusRefdecodeMSFormatInt24 = 2

// decodeLibopusMultistreamInt24 drives libopus opus_multistream_decode24()
// through the refdecode_multistream C helper.
func decodeLibopusMultistreamInt24(sampleRate, channels, streams, coupled, frameSize int, mapping []byte, packets [][]byte) ([]int32, error) {
	reader, err := runLibopusMultistreamDecode(sampleRate, channels, streams, coupled, frameSize, 0, libopusRefdecodeMSFormatInt24, mapping, packets)
	if err != nil {
		return nil, err
	}
	nSamples := reader.Count(-1)
	reader.ExpectRemaining(nSamples * 4)
	out := make([]int32, nSamples)
	for i := range out {
		out[i] = reader.I32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

// TestMultistreamDecodeInt24MatchesLibopus compares stereo int24 samples with
// the selected libopus opus_multistream_decode24 build.
func TestMultistreamDecodeInt24MatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	const (
		sampleRate = 48000
		channels   = 2
	)
	enc := mustNewDefaultMultistreamEncoder(t, sampleRate, channels, ApplicationAudio)
	frameSize := enc.FrameSize()

	pcm := generateSurroundTestSignal(sampleRate, frameSize, channels)
	buf := make([]byte, 4000)
	n, err := enc.Encode(pcm, buf)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	encodedPacket := buf[:n]

	dec := mustNewDefaultMultistreamDecoder(t, sampleRate, channels)
	streams := dec.Streams()
	coupled := dec.CoupledStreams()
	mapping := defaultVorbisMapping(channels)

	want, err := decodeLibopusMultistreamInt24(sampleRate, channels, streams, coupled, frameSize, mapping, [][]byte{encodedPacket})
	if err != nil {
		libopustest.HelperUnavailable(t, "multistream reference decode int24", err)
	}

	got := make([]int32, frameSize*channels)
	nDec, err := dec.DecodeInt24(encodedPacket, got)
	if err != nil {
		t.Fatalf("DecodeInt24: %v", err)
	}
	got = got[:nDec*channels]

	assertInt24ParitySelectedCExact(t, got, want, sampleRate, channels, "multistream int24 decode")
}

// TestMultistreamDecodeInt24SliceMatchesDecodeInt24 verifies that
// MultistreamDecoder.DecodeInt24Slice returns the same samples as DecodeInt24.
func TestMultistreamDecodeInt24SliceMatchesDecodeInt24(t *testing.T) {
	const (
		sampleRate = 48000
		channels   = 2
	)
	enc := mustNewDefaultMultistreamEncoder(t, sampleRate, channels, ApplicationAudio)
	pcm := generateSurroundTestSignal(sampleRate, enc.FrameSize(), channels)
	buf := make([]byte, 4000)
	n, err := enc.Encode(pcm, buf)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	encodedPacket := buf[:n]

	dec1 := mustNewDefaultMultistreamDecoder(t, sampleRate, channels)
	got24 := make([]int32, enc.FrameSize()*channels)
	n24, err := dec1.DecodeInt24(encodedPacket, got24)
	if err != nil {
		t.Fatalf("DecodeInt24: %v", err)
	}
	want := got24[:n24*channels]

	dec2 := mustNewDefaultMultistreamDecoder(t, sampleRate, channels)
	got, err := dec2.DecodeInt24Slice(encodedPacket)
	if err != nil {
		t.Fatalf("DecodeInt24Slice: %v", err)
	}

	if len(got) != len(want) {
		t.Fatalf("len got=%d want=%d", len(got), len(want))
	}
	for i := range got {
		if got[i] != want[i] {
			t.Fatalf("sample[%d]=%d want %d", i, got[i], want[i])
		}
	}
}

// defaultVorbisMapping returns the standard Vorbis channel mapping for the
// given channel count; used to build oracle payloads.
func defaultVorbisMapping(channels int) []byte {
	// RFC 7845 / Vorbis channel mappings for channels 1-8.
	mappings := [][]byte{
		{0},
		{0, 1},
		{0, 2, 1},
		{0, 1, 2, 3},
		{0, 4, 1, 2, 3},
		{0, 4, 1, 2, 3, 5},
		{0, 4, 1, 2, 3, 5, 6},
		{0, 6, 1, 2, 3, 4, 5, 7},
	}
	if channels >= 1 && channels <= 8 {
		return mappings[channels-1]
	}
	m := make([]byte, channels)
	for i := range m {
		m[i] = byte(i)
	}
	return m
}

func TestMultistreamRoundTrip_51(t *testing.T) {
	channels := 6 // 5.1 surround
	sampleRate := 48000
	frameSize := 960 // 20ms

	enc := mustNewDefaultMultistreamEncoder(t, sampleRate, channels, ApplicationAudio)
	dec := mustNewDefaultMultistreamDecoder(t, sampleRate, channels)

	// Generate 6-channel test signal
	pcmIn := generateSurroundTestSignal(sampleRate, frameSize, channels)
	inputEnergy := computeEnergyFloat32(pcmIn)

	// Encode
	packet, err := enc.EncodeFloat32(pcmIn)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}

	if len(packet) == 0 {
		t.Fatal("Encoded packet is empty")
	}

	// Decode
	pcmOut := make([]float32, frameSize*channels)
	n, err := dec.Decode(packet, pcmOut)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}

	outputEnergy := computeEnergyFloat32(pcmOut[:n*channels])

	// Verify output length
	expectedLen := frameSize * channels
	if n*channels != expectedLen {
		t.Errorf("Output length = %d, want %d", n*channels, expectedLen)
	}

	// Log metrics
	t.Logf("5.1 round-trip: input energy=%.4f, output energy=%.4f, packet=%d bytes",
		inputEnergy, outputEnergy, len(packet))

	// Energy ratio
	if inputEnergy > 0 {
		ratio := outputEnergy / inputEnergy
		t.Logf("Energy ratio: %.2f%%", ratio*100)
	}

	// Verify non-zero output
	if outputEnergy == 0 {
		t.Error("Output has zero energy")
	}

	// Log per-channel energy
	for ch := range channels {
		chEnergy := computeChannelEnergy(pcmOut[:n*channels], channels, ch)
		t.Logf("  Channel %d energy: %.4f", ch, chEnergy)
	}
}

// TestMultistreamRoundTrip_71 tests 7.1 surround (8 channel) encode/decode round-trip.
func TestMultistreamRoundTrip_71(t *testing.T) {
	channels := 8 // 7.1 surround
	sampleRate := 48000
	frameSize := 960 // 20ms

	enc := mustNewDefaultMultistreamEncoder(t, sampleRate, channels, ApplicationAudio)
	dec := mustNewDefaultMultistreamDecoder(t, sampleRate, channels)

	// Generate 8-channel test signal
	pcmIn := generateSurroundTestSignal(sampleRate, frameSize, channels)
	inputEnergy := computeEnergyFloat32(pcmIn)

	// Encode
	packet, err := enc.EncodeFloat32(pcmIn)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}

	if len(packet) == 0 {
		t.Fatal("Encoded packet is empty")
	}

	// Decode
	pcmOut := make([]float32, frameSize*channels)
	n, err := dec.Decode(packet, pcmOut)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}

	outputEnergy := computeEnergyFloat32(pcmOut[:n*channels])

	// Verify output length
	expectedLen := frameSize * channels
	if n*channels != expectedLen {
		t.Errorf("Output length = %d, want %d", n*channels, expectedLen)
	}

	// Log metrics
	t.Logf("7.1 round-trip: input energy=%.4f, output energy=%.4f, packet=%d bytes",
		inputEnergy, outputEnergy, len(packet))

	// Energy ratio
	if inputEnergy > 0 {
		ratio := outputEnergy / inputEnergy
		t.Logf("Energy ratio: %.2f%%", ratio*100)
	}

	// Verify non-zero output
	if outputEnergy == 0 {
		t.Error("Output has zero energy")
	}
}

// TestMultistreamRoundTrip_Stereo tests stereo (2 channel) multistream as edge case.
func TestMultistreamRoundTrip_Stereo(t *testing.T) {
	channels := 2 // Stereo
	sampleRate := 48000
	frameSize := 960 // 20ms

	enc := mustNewDefaultMultistreamEncoder(t, sampleRate, channels, ApplicationAudio)
	dec := mustNewDefaultMultistreamDecoder(t, sampleRate, channels)

	// Generate stereo test signal (L: 440Hz, R: 880Hz)
	pcmIn := make([]float32, frameSize*channels)
	for i := range frameSize {
		pcmIn[i*2] = float32(0.5 * math.Sin(2*math.Pi*440*float64(i)/float64(sampleRate)))
		pcmIn[i*2+1] = float32(0.5 * math.Sin(2*math.Pi*880*float64(i)/float64(sampleRate)))
	}
	inputEnergy := computeEnergyFloat32(pcmIn)

	// Encode
	packet, err := enc.EncodeFloat32(pcmIn)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}

	// Decode
	pcmOut := make([]float32, frameSize*channels)
	n, err := dec.Decode(packet, pcmOut)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}

	outputEnergy := computeEnergyFloat32(pcmOut[:n*channels])

	t.Logf("Stereo round-trip: input=%.4f, output=%.4f, packet=%d bytes",
		inputEnergy, outputEnergy, len(packet))

	// Verify streams and coupled streams for stereo
	if enc.Streams() != 1 {
		t.Errorf("Stereo should have 1 stream, got %d", enc.Streams())
	}
	if enc.CoupledStreams() != 1 {
		t.Errorf("Stereo should have 1 coupled stream, got %d", enc.CoupledStreams())
	}
}

// TestMultistreamRoundTrip_MultipleFrames tests encoding/decoding multiple consecutive frames.
func TestMultistreamRoundTrip_MultipleFrames(t *testing.T) {
	channels := 6 // 5.1 surround
	sampleRate := 48000
	frameSize := 960 // 20ms
	numFrames := 10

	enc := mustNewDefaultMultistreamEncoder(t, sampleRate, channels, ApplicationAudio)
	dec := mustNewDefaultMultistreamDecoder(t, sampleRate, channels)

	var totalPacketBytes int
	var totalInputEnergy, totalOutputEnergy float64

	pcmOut := make([]float32, frameSize*channels)
	for i := range numFrames {
		// Generate unique signal for each frame
		pcmIn := generateSurroundTestSignal(sampleRate, frameSize, channels)
		// Shift frequency slightly for each frame
		for s := 0; s < frameSize*channels; s++ {
			pcmIn[s] *= float32(1.0 - float64(i)*0.05) // Slight amplitude variation
		}

		totalInputEnergy += computeEnergyFloat32(pcmIn)

		// Encode
		packet, err := enc.EncodeFloat32(pcmIn)
		if err != nil {
			t.Fatalf("Frame %d encode error: %v", i, err)
		}
		totalPacketBytes += len(packet)

		// Decode
		n, err := dec.Decode(packet, pcmOut)
		if err != nil {
			t.Fatalf("Frame %d decode error: %v", i, err)
		}
		totalOutputEnergy += computeEnergyFloat32(pcmOut[:n*channels])
	}

	avgPacketSize := totalPacketBytes / numFrames
	avgInputEnergy := totalInputEnergy / float64(numFrames)
	avgOutputEnergy := totalOutputEnergy / float64(numFrames)

	t.Logf("Multiple frames: %d frames, avg packet=%d bytes, avg input=%.4f, avg output=%.4f",
		numFrames, avgPacketSize, avgInputEnergy, avgOutputEnergy)

	if avgOutputEnergy == 0 {
		t.Error("Average output energy is zero")
	}
}

// TestMultistreamRoundTrip_Int16 tests int16 encode/decode path.
func TestMultistreamRoundTrip_Int16(t *testing.T) {
	channels := 6
	sampleRate := 48000
	frameSize := 960

	enc := mustNewDefaultMultistreamEncoder(t, sampleRate, channels, ApplicationAudio)
	dec := mustNewDefaultMultistreamDecoder(t, sampleRate, channels)

	// Generate int16 test signal
	pcmIn := make([]int16, frameSize*channels)
	for s := range frameSize {
		for ch := range channels {
			freq := 220.0 + float64(ch)*110
			pcmIn[s*channels+ch] = int16(8192 * math.Sin(2*math.Pi*freq*float64(s)/float64(sampleRate)))
		}
	}

	// Encode
	packet, err := enc.EncodeInt16Slice(pcmIn)
	if err != nil {
		t.Fatalf("EncodeInt16Slice error: %v", err)
	}

	// Decode
	pcmOut := make([]int16, frameSize*channels)
	n, err := dec.DecodeInt16(packet, pcmOut)
	if err != nil {
		t.Fatalf("DecodeInt16 error: %v", err)
	}

	// Verify output length
	expectedLen := frameSize * channels
	if n*channels != expectedLen {
		t.Errorf("Output length = %d, want %d", n*channels, expectedLen)
	}

	t.Logf("Int16 round-trip: %d input samples -> %d bytes -> %d output samples",
		len(pcmIn), len(packet), n*channels)
}

// TestMultistreamRoundTrip_AllApplications tests all application modes.
func TestMultistreamRoundTrip_AllApplications(t *testing.T) {
	apps := []struct {
		app  Application
		name string
	}{
		{ApplicationVoIP, "VoIP"},
		{ApplicationAudio, "Audio"},
		{ApplicationLowDelay, "LowDelay"},
	}

	channels := 6
	sampleRate := 48000
	frameSize := 960

	for _, tc := range apps {
		t.Run(tc.name, func(t *testing.T) {
			enc := mustNewDefaultMultistreamEncoder(t, sampleRate, channels, tc.app)
			dec := mustNewDefaultMultistreamDecoder(t, sampleRate, channels)

			pcm := generateSurroundTestSignal(sampleRate, frameSize, channels)
			packet, err := enc.EncodeFloat32(pcm)
			if err != nil {
				t.Fatalf("Encode error: %v", err)
			}

			pcmOut := make([]float32, frameSize*channels)
			n, err := dec.Decode(packet, pcmOut)
			if err != nil {
				t.Fatalf("Decode error: %v", err)
			}

			energy := computeEnergyFloat32(pcmOut[:n*channels])
			t.Logf("%s: packet=%d bytes, output energy=%.4f", tc.name, len(packet), energy)
		})
	}
}

// TestMultistreamRoundTrip_Mono tests mono (1 channel) multistream as edge case.
func TestMultistreamRoundTrip_Mono(t *testing.T) {
	channels := 1
	sampleRate := 48000
	frameSize := 960

	enc := mustNewDefaultMultistreamEncoder(t, sampleRate, channels, ApplicationAudio)
	dec := mustNewDefaultMultistreamDecoder(t, sampleRate, channels)

	// Generate mono test signal
	pcmIn := make([]float32, frameSize)
	for i := range frameSize {
		pcmIn[i] = float32(0.5 * math.Sin(2*math.Pi*440*float64(i)/float64(sampleRate)))
	}
	inputEnergy := computeEnergyFloat32(pcmIn)

	// Encode
	packet, err := enc.EncodeFloat32(pcmIn)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}

	// Decode
	pcmOut := make([]float32, frameSize*channels)
	n, err := dec.Decode(packet, pcmOut)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}

	outputEnergy := computeEnergyFloat32(pcmOut[:n*channels])

	t.Logf("Mono multistream: input=%.4f, output=%.4f, packet=%d bytes",
		inputEnergy, outputEnergy, len(packet))

	// Verify streams for mono
	if enc.Streams() != 1 {
		t.Errorf("Mono should have 1 stream, got %d", enc.Streams())
	}
	if enc.CoupledStreams() != 0 {
		t.Errorf("Mono should have 0 coupled streams, got %d", enc.CoupledStreams())
	}
}

func generateSurroundTestSignal(sampleRate, frameSize, channels int) []float32 {
	pcm := make([]float32, frameSize*channels)
	// Use different frequencies for each channel
	// Base frequencies spaced to be distinguishable: 220, 330, 440, 550, 660, 770, 880, 990 Hz
	baseFreq := 220.0

	for s := range frameSize {
		for ch := range channels {
			freq := baseFreq + float64(ch)*110
			val := float32(0.3 * math.Sin(2*math.Pi*freq*float64(s)/float64(sampleRate)))
			pcm[s*channels+ch] = val
		}
	}
	return pcm
}

func generateSurroundTestSignalInt24(sampleRate, frameSize, channels int) []int32 {
	pcm := make([]int32, frameSize*channels)
	baseFreq := 220.0

	for s := range frameSize {
		for ch := range channels {
			freq := baseFreq + float64(ch)*110
			val := int32((1 << 22) * math.Sin(2*math.Pi*freq*float64(s)/float64(sampleRate)))
			pcm[s*channels+ch] = val
		}
	}
	return pcm
}

// computeEnergyFloat32 computes the RMS energy of a float32 signal.
func computeEnergyFloat32(samples []float32) float64 {
	if len(samples) == 0 {
		return 0
	}
	var sum float64
	for _, s := range samples {
		sum += float64(s) * float64(s)
	}
	return math.Sqrt(sum / float64(len(samples)))
}

// computeChannelEnergy computes the RMS energy for a single channel in interleaved audio.
func computeChannelEnergy(samples []float32, channels, targetChannel int) float64 {
	if len(samples) == 0 || targetChannel >= channels {
		return 0
	}
	var sum float64
	var count int
	for i := targetChannel; i < len(samples); i += channels {
		sum += float64(samples[i]) * float64(samples[i])
		count++
	}
	if count == 0 {
		return 0
	}
	return math.Sqrt(sum / float64(count))
}

func TestMultistreamConstructorErrorsReportSupportedRanges(t *testing.T) {
	tests := []struct {
		name        string
		run         func() error
		wantIs      error
		wantContain []string
	}{
		{
			name: "default encoder low channel count",
			run: func() error {
				_, err := NewMultistreamEncoderDefault(48000, 0, ApplicationAudio)
				return err
			},
			wantIs:      ErrInvalidChannels,
			wantContain: []string{"default multistream encoder supports 1-8 channels", "got 0"},
		},
		{
			name: "default encoder high channel count",
			run: func() error {
				_, err := NewMultistreamEncoderDefault(48000, 9, ApplicationAudio)
				return err
			},
			wantIs:      ErrInvalidChannels,
			wantContain: []string{"default multistream encoder supports 1-8 channels", "got 9", "NewMultistreamEncoder"},
		},
		{
			name: "default decoder high channel count",
			run: func() error {
				_, err := NewMultistreamDecoderDefault(48000, 9)
				return err
			},
			wantIs:      ErrInvalidChannels,
			wantContain: []string{"default multistream decoder supports 1-8 channels", "got 9", "NewMultistreamDecoder"},
		},
		{
			name: "explicit encoder high channel count",
			run: func() error {
				_, err := NewMultistreamEncoder(48000, 256, 1, 0, make([]byte, 256), ApplicationAudio)
				return err
			},
			wantIs:      ErrInvalidChannels,
			wantContain: []string{"multistream encoder supports 1-255 channels", "got 256"},
		},
		{
			name: "explicit decoder high channel count",
			run: func() error {
				_, err := NewMultistreamDecoder(48000, 256, 1, 0, make([]byte, 256))
				return err
			},
			wantIs:      ErrInvalidChannels,
			wantContain: []string{"multistream decoder supports 1-255 channels", "got 256"},
		},
		{
			name: "explicit encoder stream budget",
			run: func() error {
				_, err := NewMultistreamEncoder(48000, 2, 200, 56, []byte{0, 1}, ApplicationAudio)
				return err
			},
			wantIs:      ErrInvalidStreams,
			wantContain: []string{"streams + coupledStreams <= 255", "200 + 56 = 256"},
		},
		{
			name: "explicit decoder stream budget",
			run: func() error {
				_, err := NewMultistreamDecoder(48000, 2, 200, 56, []byte{0, 1})
				return err
			},
			wantIs:      ErrInvalidStreams,
			wantContain: []string{"streams + coupledStreams <= 255", "200 + 56 = 256"},
		},
		{
			name: "explicit encoder mapping length",
			run: func() error {
				_, err := NewMultistreamEncoder(48000, 6, 4, 2, []byte{0, 4, 1, 2, 3}, ApplicationAudio)
				return err
			},
			wantIs:      ErrInvalidMapping,
			wantContain: []string{"expected 6 mapping entries for 6 channels", "got 5"},
		},
		{
			name: "explicit decoder mapping length",
			run: func() error {
				_, err := NewMultistreamDecoder(48000, 6, 4, 2, []byte{0, 4, 1, 2, 3})
				return err
			},
			wantIs:      ErrInvalidMapping,
			wantContain: []string{"expected 6 mapping entries for 6 channels", "got 5"},
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := tc.run()
			if err == nil {
				t.Fatal("expected error, got nil")
			}
			if !errors.Is(err, tc.wantIs) {
				t.Fatalf("errors.Is(%v, %v) = false", err, tc.wantIs)
			}
			for _, needle := range tc.wantContain {
				if !strings.Contains(err.Error(), needle) {
					t.Fatalf("error %q does not contain %q", err.Error(), needle)
				}
			}
		})
	}
}

func TestMultistreamEncodeFloat32DefaultLSBUsesInputPCM(t *testing.T) {
	const (
		sampleRate = 48000
		channels   = 6
		frameSize  = 960
	)

	pcm := generateSurroundTestSignal(sampleRate, frameSize, channels)
	enc := mustNewDefaultMultistreamEncoder(t, sampleRate, channels, ApplicationAudio)
	ref := mustNewDefaultMultistreamEncoder(t, sampleRate, channels, ApplicationAudio)

	data := make([]byte, maxPacketBytesPerStream*enc.Streams())
	n, err := enc.Encode(pcm, data)
	if err != nil {
		t.Fatalf("Encode() error: %v", err)
	}
	if n == 0 {
		t.Fatal("Encode() returned an empty packet")
	}

	refBuf := make([]byte, maxPacketBytesPerStream*enc.Streams())
	refN, err := ref.enc.Encode(pcm, frameSize, refBuf)
	refPacket := refBuf[:refN]
	if err != nil {
		t.Fatalf("reference multistream Encode() error: %v", err)
	}
	if len(refPacket) == 0 {
		t.Fatal("reference multistream Encode() returned an empty packet")
	}

	got := data[:n]
	if !bytes.Equal(got, refPacket) {
		t.Fatalf("Encode() packet mismatch at default LSB depth: got %d bytes want %d", len(got), len(refPacket))
	}
	if gotRange, wantRange := enc.FinalRange(), ref.FinalRange(); gotRange != wantRange {
		t.Fatalf("FinalRange() = 0x%08x, want 0x%08x", gotRange, wantRange)
	}
}
