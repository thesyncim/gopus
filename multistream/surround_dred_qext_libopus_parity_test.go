//go:build gopus_dred && gopus_qext && !gopus_osce

package multistream

import (
	"bytes"
	"fmt"
	"strings"
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
	internaldred "github.com/thesyncim/gopus/internal/dred"
	"github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/types"
)

const (
	dredQEXTFrameSize        = 960
	dredQEXTFrameCount       = 96
	dredQEXTResetFrame       = 48
	dredQEXTBitrate          = 384000
	dredQEXTHighBitrate      = 768000
	dredQEXTDuration         = 80
	dredQEXTHighRateDuration = 16
	dredQEXTPacketCapacity   = 12000
	dredQEXTApplicationAudio = 2049
)

const (
	dredQEXTSurround = iota
	dredQEXTProjection
)

type dredQEXTFrameResult struct {
	finalRange   uint32
	dredLength   int
	qext         int
	streamRanges []uint32
	streamRates  []int
	packet       []byte
}

type dredQEXTReferenceResult struct {
	streams        int
	coupledStreams int
	mapping        []byte
	dredDuration   int
	qext           int
	frames         []dredQEXTFrameResult
}

var (
	dredQEXTMultistreamHelper  libopustest.HelperCache
	dredQEXTPitchModelHelper   libopustest.HelperCache
	dredQEXTEncoderModelHelper libopustest.HelperCache
)

func TestDREDQEXTSurroundAndProjectionEncodeMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := []struct {
		name        string
		kind        int
		channels    int
		projection  bool
		bitrate     int
		duration    int
		requireBoth bool
	}{
		{name: "surround_5_1", kind: dredQEXTSurround, channels: 6, bitrate: dredQEXTBitrate, duration: dredQEXTDuration},
		{name: "projection_foa", kind: dredQEXTProjection, channels: 4, projection: true, bitrate: dredQEXTBitrate, duration: dredQEXTDuration},
		{name: "surround_5_1_high_rate", kind: dredQEXTSurround, channels: 6, bitrate: dredQEXTHighBitrate, duration: dredQEXTHighRateDuration, requireBoth: true},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			pcm := dredQEXTPCM(tc.channels, tc.projection)
			ref := runDREDQEXTMultistreamReference(t, tc.kind, tc.channels, pcm, tc.bitrate, tc.duration)
			enc := newDREDQEXTMultistreamEncoder(t, tc.channels, tc.projection, tc.bitrate, tc.duration)
			if enc.Streams() != ref.streams || enc.CoupledStreams() != ref.coupledStreams {
				t.Fatalf("stream layout Go=%d/%d C=%d/%d", enc.Streams(), enc.CoupledStreams(), ref.streams, ref.coupledStreams)
			}
			if !bytes.Equal(enc.mapping, ref.mapping) {
				t.Fatalf("mapping Go=%v C=%v", enc.mapping, ref.mapping)
			}
			if !enc.DREDModelLoaded() || !enc.DREDReady() || !enc.QEXT() {
				t.Fatalf("combined controls not active: model=%t ready=%t qext=%t", enc.DREDModelLoaded(), enc.DREDReady(), enc.QEXT())
			}
			if ref.dredDuration != tc.duration || ref.qext != 1 {
				t.Fatalf("C controls duration=%d qext=%d want %d/1", ref.dredDuration, ref.qext, tc.duration)
			}

			packet := make([]byte, dredQEXTPacketCapacity)
			var framesWithDRED, framesWithQEXT, streamsWithBoth int
			for frame := range dredQEXTFrameCount {
				if frame == dredQEXTResetFrame {
					enc.Reset()
					if err := enc.SetDREDDuration(tc.duration); err != nil {
						t.Fatalf("reapply DRED duration after reset: %v", err)
					}
					if !enc.QEXT() || !enc.DREDReady() {
						t.Fatalf("combined features did not survive reset/rearm: qext=%t ready=%t", enc.QEXT(), enc.DREDReady())
					}
				}
				pcmFrame := pcm[frame*dredQEXTFrameSize*tc.channels : (frame+1)*dredQEXTFrameSize*tc.channels]
				n, err := enc.EncodeInt16(pcmFrame, dredQEXTFrameSize, packet)
				if err != nil {
					t.Fatalf("frame %d EncodeInt16: %v", frame, err)
				}
				got := packet[:n]
				want := ref.frames[frame]
				streamRanges := make([]uint32, len(enc.encoders))
				streamRates := make([]int, len(enc.encoders))
				for stream, child := range enc.encoders {
					streamRanges[stream] = child.FinalRange()
					streamRates[stream] = child.Bitrate()
				}
				if !equalIntSlices(streamRates, want.streamRates) {
					t.Fatalf("frame %d per-stream OPUS_GET_BITRATE differs: Go=%v C=%v", frame, streamRates, want.streamRates)
				}
				if enc.GetFinalRange() != want.finalRange || !equalU32Slices(streamRanges, want.streamRanges) || !bytes.Equal(got, want.packet) {
					first := firstByteMismatch(got, want.packet)
					diagnostic := describeDREDQEXTPacketDifference(t, got, want.packet, streamRanges, want.streamRanges, enc.Streams())
					t.Fatalf("frame %d mismatch: firstByte=%d len Go/C=%d/%d range Go/C=%08x/%08x streamRanges Go/C=%08x/%08x streamRates Go/C=%v/%v details=%s GoPrefix=%x CPrefix=%x",
						frame, first, len(got), len(want.packet), enc.GetFinalRange(), want.finalRange,
						streamRanges, want.streamRanges, streamRates, want.streamRates, diagnostic,
						got[:min(len(got), 24)], want.packet[:min(len(want.packet), 24)])
				}
				if want.dredLength != tc.duration || want.qext != 1 {
					t.Fatalf("frame %d C control state duration=%d qext=%d want %d/1", frame, want.dredLength, want.qext, tc.duration)
				}
				dredStreams, qextStreams, bothStreams := assertDREDQEXTStreamExtensions(t, frame, got, enc.Streams())
				if dredStreams > 0 {
					framesWithDRED++
				}
				if qextStreams > 0 {
					framesWithQEXT++
				}
				if bothStreams > 0 {
					streamsWithBoth++
				}
			}
			if framesWithDRED == 0 || framesWithQEXT == 0 || (tc.requireBoth && streamsWithBoth == 0) {
				t.Fatalf("combined extensions were not observed: DRED frames=%d QEXT frames=%d frames carrying both=%d",
					framesWithDRED, framesWithQEXT, streamsWithBoth)
			}
			t.Logf("matched %d strict packets/ranges; DRED frames=%d QEXT frames=%d frames with both=%d; reference=%s",
				dredQEXTFrameCount, framesWithDRED, framesWithQEXT, streamsWithBoth, libopustest.DREDQEXTRefPath())
			assertDREDQEXTMultistreamWarmZeroAlloc(t, enc, pcm, tc.channels)
		})
	}
}

func dredQEXTPCM(channels int, projection bool) []int16 {
	floatPCM := generateSurroundSweep(channels, dredQEXTFrameSize, dredQEXTFrameCount)
	if projection {
		floatPCM = generateAmbisonicsSweep(channels, dredQEXTFrameSize, dredQEXTFrameCount)
	}
	return floatToInt16(floatPCM)
}

func newDREDQEXTMultistreamEncoder(t testing.TB, channels int, projection bool, bitrate, duration int) *Encoder {
	t.Helper()
	var enc *Encoder
	var err error
	if projection {
		enc, err = NewProjectionEncoder(48000, channels)
	} else {
		enc, err = NewEncoderDefault(48000, channels)
	}
	if err != nil {
		t.Fatalf("create multistream encoder: %v", err)
	}
	enc.SetBitrate(bitrate)
	enc.SetVBR(true)
	enc.SetVBRConstraint(true)
	enc.SetComplexity(10)
	enc.SetBandwidth(types.BandwidthFullband)
	enc.SetMaxBandwidth(types.BandwidthFullband)
	enc.SetSignal(types.SignalMusic)
	enc.SetPacketLoss(20)
	enc.SetMode(encoder.ModeCELT)
	enc.SetQEXT(true)
	enc.SetDNNBlob(requireDREDQEXTEncoderModelBlob(t))
	if err := enc.SetDREDDuration(duration); err != nil {
		t.Fatalf("SetDREDDuration: %v", err)
	}
	return enc
}

func requireDREDQEXTEncoderModelBlob(t testing.TB) *dnnblob.Blob {
	t.Helper()
	pitchPath, err := dredQEXTPitchModelHelper.Path(func() (string, error) {
		return libopustest.BuildDREDHelper("", "libopus_pitchdnn_model_blob.c", "gopus_dred_qext_pitch_model_blob", true)
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "combined DRED-QEXT pitch model", err)
	}
	pitch, err := libopustest.RunHelper(pitchPath, nil)
	if err != nil {
		t.Fatalf("run combined DRED-QEXT pitch model helper: %v", err)
	}
	encoderPath, err := dredQEXTEncoderModelHelper.Path(func() (string, error) {
		return libopustest.BuildDREDHelper("", "libopus_dred_encoder_model_blob.c", "gopus_dred_qext_encoder_model_blob", true)
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "combined DRED-QEXT encoder model", err)
	}
	encoderModel, err := libopustest.RunHelper(encoderPath, nil)
	if err != nil {
		t.Fatalf("run combined DRED-QEXT encoder model helper: %v", err)
	}
	data := make([]byte, 0, len(pitch)+len(encoderModel))
	data = append(data, pitch...)
	data = append(data, encoderModel...)
	blob, err := dnnblob.Clone(data)
	if err != nil {
		t.Fatalf("clone selected combined model blob: %v", err)
	}
	return blob
}

func runDREDQEXTMultistreamReference(t *testing.T, kind, channels int, pcm []int16, bitrate, duration int) dredQEXTReferenceResult {
	return runDREDQEXTMultistreamReferenceFrames(t, kind, channels, pcm, bitrate, duration, dredQEXTFrameCount)
}

func runDREDQEXTMultistreamReferenceFrames(t *testing.T, kind, channels int, pcm []int16, bitrate, duration, frameCount int) dredQEXTReferenceResult {
	t.Helper()
	path, err := dredQEXTMultistreamHelper.CHelperPath(libopustest.CHelperConfig{
		Label:       "combined DRED-QEXT multistream encode",
		OutputBase:  "gopus_libopus_refencode_dred_qext_multistream",
		SourceFile:  "libopus_refencode_dred_qext_multistream.c",
		CFlags:      []string{"-DHAVE_CONFIG_H"},
		RefIncludes: []string{"celt", "dnn", "src"},
		DREDQEXTRef: true,
		Libs:        []string{libopustest.DREDQEXTRefPath(".libs", "libopus.a"), "-lm"},
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "combined DRED-QEXT multistream encode", err)
	}

	payload := libopustest.NewOraclePayloadVersion("GMQI", 3,
		uint32(kind), 48000, uint32(channels), dredQEXTApplicationAudio,
		uint32(bitrate), 1, 1, 10, 1105, // OPUS_BANDWIDTH_FULLBAND
		uint32(types.SignalMusic), 20, uint32(duration), 1,
		dredQEXTFrameSize, uint32(frameCount), dredQEXTPacketCapacity,
	)
	for frame := range frameCount {
		reset := uint32(0)
		if frame == dredQEXTResetFrame {
			reset = 1
		}
		payload.U32(reset)
		start := frame * dredQEXTFrameSize * channels
		for _, sample := range pcm[start : start+dredQEXTFrameSize*channels] {
			payload.I16(sample)
		}
	}
	reader, err := libopustest.RunOracleVersion(path, payload.Bytes(), "combined DRED-QEXT multistream encode", "GMQO", 3)
	if err != nil {
		t.Fatalf("run selected C combined DRED-QEXT encoder: %v", err)
	}
	result := dredQEXTReferenceResult{
		streams:        int(reader.U32()),
		coupledStreams: int(reader.U32()),
	}
	if gotChannels := int(reader.U32()); gotChannels != channels {
		t.Fatalf("C channels=%d want %d", gotChannels, channels)
	}
	result.mapping = append([]byte(nil), reader.Bytes(channels)...)
	result.dredDuration = int(reader.U32())
	result.qext = int(reader.U32())
	if records := reader.Count(frameCount); records != frameCount {
		t.Fatalf("C frame records=%d want %d", records, frameCount)
	}
	result.frames = make([]dredQEXTFrameResult, frameCount)
	for frame := range result.frames {
		entry := dredQEXTFrameResult{
			finalRange:   reader.U32(),
			dredLength:   int(reader.U32()),
			qext:         int(reader.U32()),
			streamRanges: make([]uint32, result.streams),
			streamRates:  make([]int, result.streams),
		}
		for stream := range entry.streamRanges {
			entry.streamRanges[stream] = reader.U32()
		}
		for stream := range entry.streamRates {
			entry.streamRates[stream] = int(reader.U32())
		}
		n := int(reader.U32())
		if n <= 0 || n > dredQEXTPacketCapacity {
			t.Fatalf("C frame %d packet length=%d", frame, n)
		}
		entry.packet = append([]byte(nil), reader.Bytes(n)...)
		result.frames[frame] = entry
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
	return result
}

func assertDREDQEXTStreamExtensions(t *testing.T, frame int, packet []byte, streams int) (dredStreams, qextStreams, streamsWithBoth int) {
	t.Helper()
	packets, err := parseMultistreamPacket(packet, streams)
	if err != nil {
		t.Fatalf("frame %d parse multistream packet: %v", frame, err)
	}
	for stream, streamPacket := range packets {
		if len(streamPacket) == 0 {
			t.Fatalf("frame %d stream %d is empty", frame, stream)
		}
		if toc := parseStreamTOC(streamPacket[0]); toc.mode != streamModeCELT {
			t.Fatalf("frame %d stream %d mode=%v want CELT", frame, stream, toc.mode)
		}
		_, _, hasDRED, err := findDREDPayload(streamPacket)
		if err != nil {
			t.Fatalf("frame %d stream %d parse DRED extension: %v", frame, stream, err)
		}
		streamHasQEXT := false
		if hasDRED {
			dredStreams++
		}
		parsed, err := parseOpusPacket(streamPacket, false)
		if err != nil {
			t.Fatalf("frame %d stream %d parse Opus packet: %v", frame, stream, err)
		}
		if len(parsed.padding) == 0 || parsed.paddingFrameCount <= 0 {
			continue
		}
		var iter packetExtensionIterator
		initPacketExtensionIterator(&iter, parsed.padding, parsed.paddingFrameCount)
		for {
			var extension packetExtensionData
			more, iterErr := iter.next(&extension)
			if iterErr != nil {
				t.Fatalf("frame %d stream %d parse extension: %v", frame, stream, iterErr)
			}
			if !more {
				break
			}
			if extension.ID == qextPacketExtensionID {
				qextStreams++
				streamHasQEXT = true
			}
		}
		if hasDRED && streamHasQEXT {
			streamsWithBoth++
		}
	}
	return dredStreams, qextStreams, streamsWithBoth
}

func describeDREDQEXTPacketDifference(t *testing.T, got, want []byte, gotRanges, wantRanges []uint32, streams int) string {
	t.Helper()
	gotStreams, gotErr := parseMultistreamPacket(got, streams)
	wantStreams, wantErr := parseMultistreamPacket(want, streams)
	if gotErr != nil || wantErr != nil {
		return fmt.Sprintf("stream-parse got=%v want=%v", gotErr, wantErr)
	}
	var b strings.Builder
	for stream := 0; stream < streams; stream++ {
		gotPacket, wantPacket := gotStreams[stream], wantStreams[stream]
		if bytes.Equal(gotPacket, wantPacket) && gotRanges[stream] == wantRanges[stream] {
			continue
		}
		fmt.Fprintf(&b, "stream%d byte=%d len=%d/%d range=%08x/%08x Go{%s} C{%s} ", stream,
			firstByteMismatch(gotPacket, wantPacket), len(gotPacket), len(wantPacket), gotRanges[stream], wantRanges[stream],
			describeDREDQEXTChildPacket(gotPacket), describeDREDQEXTChildPacket(wantPacket))
	}
	return b.String()
}

func describeDREDQEXTChildPacket(packet []byte) string {
	parsed, err := parseOpusPacket(packet, false)
	if err != nil {
		return fmt.Sprintf("parse-error=%v", err)
	}
	mainBytes := 0
	for _, frame := range parsed.frames {
		mainBytes += len(frame)
	}
	dredBytes := 0
	qextBytes := 0
	extensions, err := parsePacketExtensionList(parsed.padding, parsed.paddingFrameCount)
	if err != nil {
		return fmt.Sprintf("toc=%02x main=%d pad=%d extension-error=%v", packet[0], mainBytes, len(parsed.padding), err)
	}
	for _, extension := range extensions {
		switch extension.ID {
		case internaldred.ExtensionID:
			dredBytes += len(extension.Data)
		case qextPacketExtensionID:
			qextBytes += len(extension.Data)
		}
	}
	return fmt.Sprintf("toc=%02x frames=%d main=%d pad=%d ext=%d DRED=%d QEXT=%d", packet[0], len(parsed.frames), mainBytes, len(parsed.padding), len(extensions), dredBytes, qextBytes)
}

func equalU32Slices(a, b []uint32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func equalIntSlices(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func assertDREDQEXTMultistreamWarmZeroAlloc(t *testing.T, enc *Encoder, pcm []int16, channels int) {
	t.Helper()
	packet := make([]byte, dredQEXTPacketCapacity)
	frame := pcm[:dredQEXTFrameSize*channels]
	encodeFrame := func() {
		if n, err := enc.EncodeInt16(frame, dredQEXTFrameSize, packet); err != nil || n <= 0 {
			t.Fatalf("warm combined one-frame encode: bytes=%d err=%v", n, err)
		}
	}
	encodeFrame()
	frameAllocs := testing.AllocsPerRun(5, encodeFrame)
	t.Logf("combined DRED-QEXT one-frame public EncodeInt16: %.0f allocs/op", frameAllocs)
	encodeCycle := func() {
		for frame := 0; frame < dredQEXTFrameCount; frame++ {
			start := frame * dredQEXTFrameSize * channels
			end := start + dredQEXTFrameSize*channels
			n, err := enc.EncodeInt16(pcm[start:end], dredQEXTFrameSize, packet)
			if err != nil {
				t.Fatalf("warm combined encode frame %d: %v", frame, err)
			}
			if n <= 0 {
				t.Fatalf("warm combined encode frame %d returned %d bytes", frame, n)
			}
		}
	}
	encodeCycle()
	allocs := testing.AllocsPerRun(2, encodeCycle)
	if allocs != 0 {
		t.Fatalf("combined DRED-QEXT multistream EncodeInt16 allocs/op=%.2f want 0", allocs)
	}
	t.Logf("combined DRED-QEXT warmed public EncodeInt16: %d frames, %.0f allocs/op", dredQEXTFrameCount, allocs)
}
