package multistream

import (
	"bytes"
	"encoding/binary"
	"errors"
	"slices"
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
	internaldred "github.com/thesyncim/gopus/internal/dred"
	"github.com/thesyncim/gopus/rangecoding"
)

type streamDecoderStub struct {
	channels int
	decode   func(data []byte, frameSize int) ([]float64, error)
}

func (s streamDecoderStub) Decode(data []byte, frameSize int) ([]float64, error) {
	return s.decode(data, frameSize)
}

func (s streamDecoderStub) DecodeStereo(data []byte, frameSize int) ([]float64, error) {
	return s.decode(data, frameSize)
}

func (s streamDecoderStub) Reset() {}

func (s streamDecoderStub) Channels() int {
	return s.channels
}

func (s streamDecoderStub) SetIgnoreExtensions(bool) {}

// TestNewDecoder_ValidConfigs tests decoder creation with valid configurations.
func TestNewDecoder_ValidConfigs(t *testing.T) {
	tests := []struct {
		name         string
		channels     int
		wantStreams  int
		wantCoupled  int
		wantOutputCh int
	}{
		{"mono", 1, 1, 0, 1},
		{"stereo", 2, 1, 1, 2},
		{"3.0 surround", 3, 2, 1, 3},
		{"quad", 4, 2, 2, 4},
		{"5.0 surround", 5, 3, 2, 5},
		{"5.1 surround", 6, 4, 2, 6},
		{"6.1 surround", 7, 5, 2, 7},
		{"7.1 surround", 8, 5, 3, 8},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			streams, coupled, mapping, err := DefaultMapping(tc.channels)
			if err != nil {
				t.Fatalf("DefaultMapping(%d) error: %v", tc.channels, err)
			}

			dec, err := NewDecoder(48000, tc.channels, streams, coupled, mapping)
			if err != nil {
				t.Fatalf("NewDecoder error: %v", err)
			}

			if got := dec.Channels(); got != tc.wantOutputCh {
				t.Errorf("Channels() = %d, want %d", got, tc.wantOutputCh)
			}
			if got := dec.Streams(); got != tc.wantStreams {
				t.Errorf("Streams() = %d, want %d", got, tc.wantStreams)
			}
			if got := dec.CoupledStreams(); got != tc.wantCoupled {
				t.Errorf("CoupledStreams() = %d, want %d", got, tc.wantCoupled)
			}
			if got := dec.SampleRate(); got != 48000 {
				t.Errorf("SampleRate() = %d, want 48000", got)
			}
		})
	}
}

// TestNewDecoder_InvalidConfigs tests decoder creation with invalid configurations.
func TestNewDecoder_InvalidConfigs(t *testing.T) {
	tests := []struct {
		name     string
		channels int
		streams  int
		coupled  int
		mapping  []byte
		wantErr  error
	}{
		{
			name:     "channels < 1",
			channels: 0,
			streams:  1,
			coupled:  0,
			mapping:  []byte{0},
			wantErr:  ErrInvalidChannels,
		},
		{
			name:     "channels > 255",
			channels: 256,
			streams:  1,
			coupled:  0,
			mapping:  make([]byte, 256),
			wantErr:  ErrInvalidChannels,
		},
		{
			name:     "streams < 1",
			channels: 2,
			streams:  0,
			coupled:  0,
			mapping:  []byte{0, 1},
			wantErr:  ErrInvalidStreams,
		},
		{
			name:     "coupledStreams > streams",
			channels: 2,
			streams:  1,
			coupled:  2,
			mapping:  []byte{0, 1},
			wantErr:  ErrInvalidCoupledStreams,
		},
		{
			name:     "coupledStreams < 0",
			channels: 2,
			streams:  1,
			coupled:  -1,
			mapping:  []byte{0, 1},
			wantErr:  ErrInvalidCoupledStreams,
		},
		{
			name:     "mapping length != channels",
			channels: 2,
			streams:  1,
			coupled:  1,
			mapping:  []byte{0}, // Only 1 element, need 2
			wantErr:  ErrInvalidMapping,
		},
		{
			name:     "mapping index exceeds max",
			channels: 2,
			streams:  1,
			coupled:  1,
			mapping:  []byte{0, 5}, // 5 > streams+coupled (1+1=2)
			wantErr:  ErrInvalidMapping,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := NewDecoder(48000, tc.channels, tc.streams, tc.coupled, tc.mapping)
			if err == nil {
				t.Fatalf("NewDecoder should have failed")
			}
			if !errors.Is(err, tc.wantErr) {
				t.Errorf("got error %v, want %v", err, tc.wantErr)
			}
		})
	}
}

// TestDefaultMapping tests the default Vorbis-style mapping for 1-8 channels.
func TestDefaultMapping(t *testing.T) {
	tests := []struct {
		channels    int
		wantStreams int
		wantCoupled int
		wantMapLen  int
	}{
		{1, 1, 0, 1}, // mono
		{2, 1, 1, 2}, // stereo
		{3, 2, 1, 3}, // 3.0
		{4, 2, 2, 4}, // quad
		{5, 3, 2, 5}, // 5.0
		{6, 4, 2, 6}, // 5.1
		{7, 5, 2, 7}, // 6.1
		{8, 5, 3, 8}, // 7.1
	}

	for _, tc := range tests {
		streams, coupled, mapping, err := DefaultMapping(tc.channels)
		if err != nil {
			t.Errorf("DefaultMapping(%d) unexpected error: %v", tc.channels, err)
			continue
		}
		if streams != tc.wantStreams {
			t.Errorf("DefaultMapping(%d) streams = %d, want %d", tc.channels, streams, tc.wantStreams)
		}
		if coupled != tc.wantCoupled {
			t.Errorf("DefaultMapping(%d) coupled = %d, want %d", tc.channels, coupled, tc.wantCoupled)
		}
		if len(mapping) != tc.wantMapLen {
			t.Errorf("DefaultMapping(%d) mapping len = %d, want %d", tc.channels, len(mapping), tc.wantMapLen)
		}
	}

	// Test invalid channel counts
	for _, ch := range []int{0, 9, 100, -1} {
		_, _, _, err := DefaultMapping(ch)
		if !errors.Is(err, ErrUnsupportedChannels) {
			t.Errorf("DefaultMapping(%d) should return ErrUnsupportedChannels, got %v", ch, err)
		}
	}
}

// TestResolveMapping tests the mapping resolution logic.
func TestResolveMapping(t *testing.T) {
	tests := []struct {
		name       string
		mappingIdx byte
		coupled    int
		wantStream int
		wantChan   int
	}{
		// Stereo (1 coupled stream): indices 0,1 are coupled stream 0
		{"stereo L", 0, 1, 0, 0},
		{"stereo R", 1, 1, 0, 1},

		// 5.1 (2 coupled streams): indices 0-3 coupled, 4-5 uncoupled
		{"5.1 FL", 0, 2, 0, 0},  // coupled 0, left
		{"5.1 FR", 1, 2, 0, 1},  // coupled 0, right
		{"5.1 RL", 2, 2, 1, 0},  // coupled 1, left
		{"5.1 RR", 3, 2, 1, 1},  // coupled 1, right
		{"5.1 C", 4, 2, 2, 0},   // uncoupled 2
		{"5.1 LFE", 5, 2, 3, 0}, // uncoupled 3

		// 7.1 (3 coupled streams): indices 0-5 coupled, 6-7 uncoupled
		{"7.1 FL", 0, 3, 0, 0},
		{"7.1 FR", 1, 3, 0, 1},
		{"7.1 SL", 2, 3, 1, 0},
		{"7.1 SR", 3, 3, 1, 1},
		{"7.1 RL", 4, 3, 2, 0},
		{"7.1 RR", 5, 3, 2, 1},
		{"7.1 C", 6, 3, 3, 0},
		{"7.1 LFE", 7, 3, 4, 0},

		// Silent channel
		{"silent", 255, 2, -1, -1},

		// Mono (0 coupled streams)
		{"mono", 0, 0, 0, 0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			stream, ch := resolveMapping(tc.mappingIdx, tc.coupled)
			if stream != tc.wantStream {
				t.Errorf("resolveMapping(%d, %d) stream = %d, want %d",
					tc.mappingIdx, tc.coupled, stream, tc.wantStream)
			}
			if ch != tc.wantChan {
				t.Errorf("resolveMapping(%d, %d) chan = %d, want %d",
					tc.mappingIdx, tc.coupled, ch, tc.wantChan)
			}
		})
	}
}

// TestStreamChannels tests the streamChannels helper function.
func TestStreamChannels(t *testing.T) {
	tests := []struct {
		streamIdx int
		coupled   int
		want      int
	}{
		{0, 2, 2}, // First coupled stream = stereo
		{1, 2, 2}, // Second coupled stream = stereo
		{2, 2, 1}, // First uncoupled stream = mono
		{3, 2, 1}, // Second uncoupled stream = mono
		{0, 0, 1}, // No coupled streams = all mono
		{0, 1, 2}, // One coupled stream (stereo)
		{1, 1, 1}, // After coupled stream = mono
	}

	for _, tc := range tests {
		got := streamChannels(tc.streamIdx, tc.coupled)
		if got != tc.want {
			t.Errorf("streamChannels(%d, %d) = %d, want %d",
				tc.streamIdx, tc.coupled, got, tc.want)
		}
	}
}

// TestParseSelfDelimitedLength tests the length prefix parsing.
func TestParseSelfDelimitedLength(t *testing.T) {
	tests := []struct {
		name     string
		data     []byte
		wantLen  int
		wantCons int
		wantErr  bool
	}{
		{"single byte small", []byte{100}, 100, 1, false},
		{"single byte max", []byte{251}, 251, 1, false},
		{"two byte min", []byte{252, 0}, 252, 2, false},
		{"two byte", []byte{252, 1}, 256, 2, false},         // 4*1 + 252 = 256
		{"two byte larger", []byte{253, 10}, 293, 2, false}, // 4*10 + 253 = 293
		{"empty data", []byte{}, 0, 0, true},
		{"need second byte", []byte{252}, 0, 0, true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			length, consumed, err := parseSelfDelimitedLength(tc.data)
			if tc.wantErr {
				if err == nil {
					t.Errorf("expected error, got nil")
				}
				return
			}
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if length != tc.wantLen {
				t.Errorf("length = %d, want %d", length, tc.wantLen)
			}
			if consumed != tc.wantCons {
				t.Errorf("consumed = %d, want %d", consumed, tc.wantCons)
			}
		})
	}
}

// TestParseMultistreamPacket tests multistream packet parsing.
func TestParseMultistreamPacket(t *testing.T) {
	t.Run("single stream", func(t *testing.T) {
		// Single stream: no length prefix, entire packet is the stream
		data := []byte{0xFC, 0x01, 0x02, 0x03} // Just raw packet data
		packets, err := parseMultistreamPacket(data, 1)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(packets) != 1 {
			t.Fatalf("expected 1 packet, got %d", len(packets))
		}
		if len(packets[0]) != 4 {
			t.Errorf("packet 0 len = %d, want 4", len(packets[0]))
		}
	})

	t.Run("two streams", func(t *testing.T) {
		// Two streams: first uses self-delimited framing, second is standard.
		stream0 := []byte{0xF8, 0x01, 0x02}
		stream1 := []byte{0xF8, 0x03, 0x04, 0x05}

		selfDelimited0, err := makeSelfDelimitedPacket(stream0)
		if err != nil {
			t.Fatalf("makeSelfDelimitedPacket error: %v", err)
		}
		data := append(selfDelimited0, stream1...)

		packets, err := parseMultistreamPacket(data, 2)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(packets) != 2 {
			t.Fatalf("expected 2 packets, got %d", len(packets))
		}
		if len(packets[0]) != 3 {
			t.Errorf("packet 0 len = %d, want 3", len(packets[0]))
		}
		if len(packets[1]) != 4 {
			t.Errorf("packet 1 len = %d, want 4", len(packets[1]))
		}
	})

	t.Run("empty data", func(t *testing.T) {
		_, err := parseMultistreamPacket([]byte{}, 2)
		if !errors.Is(err, ErrPacketTooShort) {
			t.Errorf("expected ErrPacketTooShort, got %v", err)
		}
	})

	t.Run("invalid stream count", func(t *testing.T) {
		_, err := parseMultistreamPacket([]byte{1, 2, 3}, 0)
		if !errors.Is(err, ErrInvalidStreamCount) {
			t.Errorf("expected ErrInvalidStreamCount, got %v", err)
		}
	})

	t.Run("insufficient data for length", func(t *testing.T) {
		// Self-delimited frame length says 10 bytes but only 1 byte follows.
		data := []byte{0xF8, 10, 0x01, 0xF8, 0x02}
		_, err := parseMultistreamPacket(data, 2)
		if !errors.Is(err, ErrPacketTooShort) {
			t.Errorf("expected ErrPacketTooShort, got %v", err)
		}
	})
}

func appendDNNBlobRecordForTest(dst []byte, name string, typ int32, payloadSize int) []byte {
	const headerSize = 64
	blockSize := ((payloadSize + headerSize - 1) / headerSize) * headerSize
	out := make([]byte, headerSize+blockSize)
	copy(out[:4], []byte("DNNw"))
	binary.LittleEndian.PutUint32(out[4:8], 0)
	binary.LittleEndian.PutUint32(out[8:12], uint32(typ))
	binary.LittleEndian.PutUint32(out[12:16], uint32(payloadSize))
	binary.LittleEndian.PutUint32(out[16:20], uint32(blockSize))
	copy(out[20:63], []byte(name))
	out[63] = 0
	return append(dst, out...)
}

func makeDecoderBlobForDREDTest(t *testing.T, withDRED bool) *dnnblob.Blob {
	t.Helper()

	var raw []byte
	raw = appendDNNBlobRecordForTest(raw, "plc_dense_in_bias", 0, 128*4)
	raw = appendDNNBlobRecordForTest(raw, "cond_net_pembed_bias", 0, 12*4)
	raw = appendDNNBlobRecordForTest(raw, "dense_if_upsampler_1_bias", 0, 64*4)
	raw = appendDNNBlobRecordForTest(raw, "lace_pitch_embedding_bias", 0, 64*4)
	raw = appendDNNBlobRecordForTest(raw, "nolace_pitch_embedding_bias", 0, 64*4)
	if withDRED {
		raw = appendDNNBlobRecordForTest(raw, "dec_dense1_bias", 0, 64*4)
	}

	blob, err := dnnblob.Clone(raw)
	if err != nil {
		t.Fatalf("dnnblob.Clone error: %v", err)
	}
	return blob
}

func addDREDExtensionToOpusPacketForTest(t *testing.T, packet []byte, body []byte) []byte {
	t.Helper()

	parsed, err := parseOpusPacket(packet, false)
	if err != nil {
		t.Fatalf("parseOpusPacket error: %v", err)
	}
	padding := append(append([]byte(nil), parsed.padding...), byte(internaldred.ExtensionID<<1), 'D', internaldred.ExperimentalVersion)
	padding = append(padding, body...)
	dst := make([]byte, len(packet)+len(padding)+8)
	n, err := buildOpusPacketFromFramesAndPadding(parsed.tocBase, parsed.frames, padding, false, dst)
	if err != nil {
		t.Fatalf("buildOpusPacketFromFramesAndPadding error: %v", err)
	}
	return dst[:n]
}

func addDREDExtensionToOpusPacketFrameForTest(t *testing.T, packet []byte, frame int, body []byte) []byte {
	t.Helper()

	if frame == 0 {
		return addDREDExtensionToOpusPacketForTest(t, packet, body)
	}
	if frame != 1 {
		t.Fatalf("unsupported test frame index %d", frame)
	}
	if len(packet) < 2 {
		t.Fatal("packet too short for frame extension test")
	}

	dst := make([]byte, len(packet)*2+16)
	padding := append([]byte{0x02, byte(internaldred.ExtensionID << 1), 'D', internaldred.ExperimentalVersion}, body...)
	n, err := buildOpusPacketFromFramesAndPadding(packet[0]&0xFC, [][]byte{packet[1:], packet[1:]}, padding, false, dst)
	if err != nil {
		t.Fatalf("buildOpusPacketFromFramesAndPadding(frame=%d) error: %v", frame, err)
	}
	return dst[:n]
}

func makeExperimentalDREDPayloadBodyForTest(t *testing.T, dredFrameOffset, dredOffset int) []byte {
	t.Helper()

	rawOffset := 16 - dredOffset + dredFrameOffset
	if rawOffset < 0 || rawOffset >= 32 {
		t.Fatalf("rawOffset=%d out of range for dredOffset=%d frameOffset=%d", rawOffset, dredOffset, dredFrameOffset)
	}

	var enc rangecoding.Encoder
	enc.Init(make([]byte, internaldred.MinBytes))
	enc.EncodeUniform(6, 16)
	enc.EncodeUniform(3, 8)
	enc.EncodeUniform(0, 2)
	enc.EncodeUniform(uint32(rawOffset), 32)
	enc.Shrink(internaldred.MinBytes)
	return enc.Done()
}

func rebuildMultistreamPacketForTest(t *testing.T, packets [][]byte) []byte {
	t.Helper()

	out := make([]byte, 0, 256)
	for i := 0; i < len(packets)-1; i++ {
		sd, err := makeSelfDelimitedPacket(packets[i])
		if err != nil {
			t.Fatalf("makeSelfDelimitedPacket(stream=%d) error: %v", i, err)
		}
		out = append(out, sd...)
	}
	out = append(out, packets[len(packets)-1]...)
	return out
}

func makeMultistreamPacketWithDREDForTest(t *testing.T, channels, targetStream int, body []byte) []byte {
	return makeMultistreamPacketWithDREDFrameForTest(t, channels, targetStream, 0, body)
}

func makeMultistreamPacketWithDREDFrameForTest(t *testing.T, channels, targetStream, frame int, body []byte) []byte {
	t.Helper()

	enc, err := NewEncoderDefault(48000, channels)
	if err != nil {
		t.Fatalf("NewEncoderDefault error: %v", err)
	}
	enc.SetBitrate(256000)

	packet, err := enc.Encode(generateTestSignal(channels, 960, 48000, 997), 960)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}
	packets, err := parseMultistreamPacket(packet, enc.Streams())
	if err != nil {
		t.Fatalf("parseMultistreamPacket error: %v", err)
	}
	if targetStream < 0 || targetStream >= len(packets) {
		t.Fatalf("targetStream=%d out of range for %d packets", targetStream, len(packets))
	}
	packets[targetStream] = addDREDExtensionToOpusPacketFrameForTest(t, packets[targetStream], frame, body)
	return rebuildMultistreamPacketForTest(t, packets)
}

func makeMultistreamTwoFramePacketWithDREDForTest(t *testing.T, channels, targetStream, frame int, body []byte) []byte {
	t.Helper()

	enc, err := NewEncoderDefault(48000, channels)
	if err != nil {
		t.Fatalf("NewEncoderDefault error: %v", err)
	}
	enc.SetBitrate(256000)

	packet, err := enc.Encode(generateTestSignal(channels, 960, 48000, 997), 960)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}
	packets, err := parseMultistreamPacket(packet, enc.Streams())
	if err != nil {
		t.Fatalf("parseMultistreamPacket error: %v", err)
	}
	if targetStream < 0 || targetStream >= len(packets) {
		t.Fatalf("targetStream=%d out of range for %d packets", targetStream, len(packets))
	}

	for i := range packets {
		base := packets[i]
		if len(base) < 2 {
			t.Fatalf("stream %d packet too short", i)
		}
		dst := make([]byte, len(base)*2+16)
		padding := []byte(nil)
		if i == targetStream {
			if frame != 1 {
				t.Fatalf("unsupported frame=%d for two-frame test", frame)
			}
			padding = append([]byte{0x02, byte(internaldred.ExtensionID << 1), 'D', internaldred.ExperimentalVersion}, body...)
		}
		n, err := buildOpusPacketFromFramesAndPadding(base[0]&0xFC, [][]byte{base[1:], base[1:]}, padding, false, dst)
		if err != nil {
			t.Fatalf("buildOpusPacketFromFramesAndPadding(stream=%d): %v", i, err)
		}
		packets[i] = dst[:n]
	}

	return rebuildMultistreamPacketForTest(t, packets)
}

func TestDecoderCachesDREDPayloadPerStreamWhenModelLoaded(t *testing.T) {
	const channels = 3
	const targetStream = 1
	body := makeExperimentalDREDPayloadBodyForTest(t, 0, 4)
	packet := makeMultistreamPacketWithDREDForTest(t, channels, targetStream, body)

	dec, err := NewDecoderDefault(48000, channels)
	if err != nil {
		t.Fatalf("NewDecoderDefault error: %v", err)
	}
	dec.SetDNNBlob(makeDecoderBlobForDREDTest(t, true))

	samples, err := dec.Decode(packet, 960)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if len(samples) != 960*channels {
		t.Fatalf("len(samples)=%d want %d", len(samples), 960*channels)
	}
	if dec.dredCache[0] != (internaldred.Cache{}) {
		t.Fatalf("stream 0 cached DRED cache=%+v want zero state", dec.dredCache[0])
	}
	if dec.dredCache[targetStream].Len != len(body) {
		t.Fatalf("stream %d dredCache.Len=%d want %d", targetStream, dec.dredCache[targetStream].Len, len(body))
	}
	if dec.dredCache[targetStream].Parsed.Header.DredOffset != 4 {
		t.Fatalf("stream %d dredCache.Parsed.Header.DredOffset=%d want 4", targetStream, dec.dredCache[targetStream].Parsed.Header.DredOffset)
	}
	if dec.dredCache[targetStream].Parsed.Header.DredFrameOffset != 0 || dec.dredCache[targetStream].Parsed.Header.Q0 != 6 || dec.dredCache[targetStream].Parsed.Header.DQ != 3 || dec.dredCache[targetStream].Parsed.Header.QMax != 15 {
		t.Fatalf("stream %d dredCache.Parsed.Header=(frame=%d q0=%d dq=%d qmax=%d) want (0,6,3,15)", targetStream, dec.dredCache[targetStream].Parsed.Header.DredFrameOffset, dec.dredCache[targetStream].Parsed.Header.Q0, dec.dredCache[targetStream].Parsed.Header.DQ, dec.dredCache[targetStream].Parsed.Header.QMax)
	}
	if !bytes.Equal(dec.dredData[targetStream][:dec.dredCache[targetStream].Len], body) {
		t.Fatalf("stream %d cached DRED payload=%x want %x", targetStream, dec.dredData[targetStream][:dec.dredCache[targetStream].Len], body)
	}
	result := dec.cachedDREDResult(targetStream, 960)
	if result.Availability.FeatureFrames != 4 || result.Availability.MaxLatents != 0 || result.Availability.OffsetSamples != 480 || result.Availability.EndSamples != 0 || result.Availability.AvailableSamples != 0 {
		t.Fatalf("stream %d cachedDREDResult=%+v want availability {FeatureFrames:4 MaxLatents:0 OffsetSamples:480 EndSamples:0 AvailableSamples:0}", targetStream, result)
	}
	if got := dec.cachedDREDMaxAvailableSamples(targetStream, 960); got != 0 {
		t.Fatalf("stream %d cachedDREDMaxAvailableSamples=%d want 0", targetStream, got)
	}
	quant := make([]int, 6)
	if n := dec.cachedDREDResult(targetStream, 10080).FillQuantizerLevels(quant); n != 0 {
		t.Fatalf("stream %d cachedDREDResult.FillQuantizerLevels count=%d want 0", targetStream, n)
	}
	if want := []int{0, 0, 0, 0, 0, 0}; !slices.Equal(quant, want) {
		t.Fatalf("stream %d cachedDREDResult.FillQuantizerLevels=%v want %v", targetStream, quant, want)
	}
	window := dec.cachedDREDFeatureWindow(targetStream, 960, 960, 960, 0)
	if window.FeatureOffsetBase != 1 || window.RecoverableFeatureFrames != 0 || window.MissingPositiveFrames != 2 {
		t.Fatalf("stream %d cachedDREDFeatureWindow=%+v want base=1 recoverable=0 missing=2", targetStream, window)
	}

	dec.Reset()
	for i := range dec.dredCache {
		if dec.dredCache[i] != (internaldred.Cache{}) {
			t.Fatalf("Reset left stream %d DRED cache=%+v want zero state", i, dec.dredCache[i])
		}
		if got := dec.cachedDREDMaxAvailableSamples(i, 960); got != 0 {
			t.Fatalf("stream %d cachedDREDMaxAvailableSamples after Reset=%d want 0", i, got)
		}
	}
}

func TestDecoderCachesDREDSampleTimingForLaterStreamFrame(t *testing.T) {
	const channels = 3
	const targetStream = 1
	body := makeExperimentalDREDPayloadBodyForTest(t, 8, -4)
	packet := makeMultistreamTwoFramePacketWithDREDForTest(t, channels, targetStream, 1, body)

	dec, err := NewDecoderDefault(48000, channels)
	if err != nil {
		t.Fatalf("NewDecoderDefault error: %v", err)
	}
	dec.SetDNNBlob(makeDecoderBlobForDREDTest(t, true))

	samples, err := dec.Decode(packet, 1920)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if len(samples) != 1920*channels {
		t.Fatalf("len(samples)=%d want %d", len(samples), 1920*channels)
	}
	if dec.dredCache[targetStream].Parsed.Header.DredOffset != -4 {
		t.Fatalf("stream %d dredCache.Parsed.Header.DredOffset=%d want -4", targetStream, dec.dredCache[targetStream].Parsed.Header.DredOffset)
	}
	if dec.dredCache[targetStream].Parsed.Header.DredFrameOffset != 8 {
		t.Fatalf("stream %d dredCache.Parsed.Header.DredFrameOffset=%d want 8", targetStream, dec.dredCache[targetStream].Parsed.Header.DredFrameOffset)
	}
	if dec.dredCache[targetStream].Parsed.Header.QMax != 15 {
		t.Fatalf("stream %d dredCache.Parsed.Header.QMax=%d want 15", targetStream, dec.dredCache[targetStream].Parsed.Header.QMax)
	}
	result := dec.cachedDREDResult(targetStream, 960)
	if result.Availability.FeatureFrames != 4 || result.Availability.MaxLatents != 0 || result.Availability.OffsetSamples != -480 || result.Availability.EndSamples != 480 || result.Availability.AvailableSamples != 480 {
		t.Fatalf("stream %d cachedDREDResult=%+v want availability {FeatureFrames:4 MaxLatents:0 OffsetSamples:-480 EndSamples:480 AvailableSamples:480}", targetStream, result)
	}
	window := dec.cachedDREDFeatureWindow(targetStream, 960, 3840, 960, 0)
	if window.FeatureOffsetBase != 5 || window.RecoverableFeatureFrames != 0 || window.MissingPositiveFrames != 2 {
		t.Fatalf("stream %d cachedDREDFeatureWindow=%+v want base=5 recoverable=0 missing=2", targetStream, window)
	}
	if got := dec.cachedDREDMaxAvailableSamples(targetStream, 960); got != 480 {
		t.Fatalf("stream %d cachedDREDMaxAvailableSamples=%d want 480", targetStream, got)
	}
}

func TestDecoderLeavesDREDPayloadDormantWithoutDREDModel(t *testing.T) {
	packet := makeMultistreamPacketWithDREDForTest(t, 3, 1, makeExperimentalDREDPayloadBodyForTest(t, 0, 4))

	dec, err := NewDecoderDefault(48000, 3)
	if err != nil {
		t.Fatalf("NewDecoderDefault error: %v", err)
	}
	dec.SetDNNBlob(makeDecoderBlobForDREDTest(t, false))

	samples, err := dec.Decode(packet, 960)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if len(samples) != 960*3 {
		t.Fatalf("len(samples)=%d want %d", len(samples), 960*3)
	}
	for i := range dec.dredCache {
		if dec.dredCache[i] != (internaldred.Cache{}) {
			t.Fatalf("stream %d cached dormant DRED cache=%+v want zero state", i, dec.dredCache[i])
		}
		if got := dec.cachedDREDMaxAvailableSamples(i, 960); got != 0 {
			t.Fatalf("stream %d cachedDREDMaxAvailableSamples without model=%d want 0", i, got)
		}
	}
}

func TestDecoderLeavesDREDPayloadDormantWhenIgnoringExtensions(t *testing.T) {
	packet := makeMultistreamPacketWithDREDForTest(t, 3, 1, makeExperimentalDREDPayloadBodyForTest(t, 0, 4))

	dec, err := NewDecoderDefault(48000, 3)
	if err != nil {
		t.Fatalf("NewDecoderDefault error: %v", err)
	}
	dec.SetDNNBlob(makeDecoderBlobForDREDTest(t, true))
	dec.SetIgnoreExtensions(true)

	samples, err := dec.Decode(packet, 960)
	if err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if len(samples) != 960*3 {
		t.Fatalf("len(samples)=%d want %d", len(samples), 960*3)
	}
	for i := range dec.dredCache {
		if dec.dredCache[i] != (internaldred.Cache{}) {
			t.Fatalf("stream %d cached ignored DRED cache=%+v want zero state", i, dec.dredCache[i])
		}
		if got := dec.cachedDREDMaxAvailableSamples(i, 960); got != 0 {
			t.Fatalf("stream %d cachedDREDMaxAvailableSamples while ignoring=%d want 0", i, got)
		}
	}
}

func TestDecoderClearsDREDPayloadWhenDowngradingBlobOrIgnoringExtensions(t *testing.T) {
	packet := makeMultistreamPacketWithDREDForTest(t, 3, 1, makeExperimentalDREDPayloadBodyForTest(t, 0, 4))

	dec, err := NewDecoderDefault(48000, 3)
	if err != nil {
		t.Fatalf("NewDecoderDefault error: %v", err)
	}
	dec.SetDNNBlob(makeDecoderBlobForDREDTest(t, true))

	if _, err := dec.Decode(packet, 960); err != nil {
		t.Fatalf("Decode error: %v", err)
	}
	if dec.dredCache[1].Empty() {
		t.Fatal("expected cached DRED payload before downgrade")
	}

	dec.SetDNNBlob(makeDecoderBlobForDREDTest(t, false))
	for i := range dec.dredCache {
		if dec.dredCache[i] != (internaldred.Cache{}) {
			t.Fatalf("downgraded blob left stream %d DRED cache=%+v want zero state", i, dec.dredCache[i])
		}
	}

	dec.SetDNNBlob(makeDecoderBlobForDREDTest(t, true))
	if _, err := dec.Decode(packet, 960); err != nil {
		t.Fatalf("Decode after reenable error: %v", err)
	}
	if dec.dredCache[1].Empty() {
		t.Fatal("expected cached DRED payload before ignore toggle")
	}
	dec.SetIgnoreExtensions(true)
	for i := range dec.dredCache {
		if dec.dredCache[i] != (internaldred.Cache{}) {
			t.Fatalf("SetIgnoreExtensions(true) left stream %d DRED cache=%+v want zero state", i, dec.dredCache[i])
		}
	}
}

func TestDecoderDoesNotCachePartialDREDStateWhenLaterStreamFails(t *testing.T) {
	packet := makeMultistreamPacketWithDREDForTest(t, 3, 0, makeExperimentalDREDPayloadBodyForTest(t, 0, 4))

	dec, err := NewDecoderDefault(48000, 3)
	if err != nil {
		t.Fatalf("NewDecoderDefault error: %v", err)
	}
	dec.SetDNNBlob(makeDecoderBlobForDREDTest(t, true))
	dec.decoders[0] = streamDecoderStub{
		channels: 2,
		decode: func(_ []byte, frameSize int) ([]float64, error) {
			return make([]float64, frameSize*2), nil
		},
	}
	dec.decoders[1] = streamDecoderStub{
		channels: 1,
		decode: func(_ []byte, _ int) ([]float64, error) {
			return nil, errors.New("boom")
		},
	}

	if _, err := dec.Decode(packet, 960); err == nil {
		t.Fatal("Decode error=nil want non-nil")
	}
	for i := range dec.dredCache {
		if dec.dredCache[i] != (internaldred.Cache{}) {
			t.Fatalf("failed decode left stream %d DRED cache=%+v want zero state", i, dec.dredCache[i])
		}
	}
}

// TestApplyChannelMapping tests the channel mapping application.
func TestApplyChannelMapping(t *testing.T) {
	t.Run("stereo simple", func(t *testing.T) {
		// 1 coupled stream (stereo), interleaved [L0, R0, L1, R1, ...]
		decodedStreams := [][]float64{
			{0.1, 0.2, 0.3, 0.4}, // stream 0: stereo, 2 samples
		}
		mapping := []byte{0, 1} // ch0 = left, ch1 = right
		frameSize := 2

		output := applyChannelMapping(decodedStreams, mapping, 1, frameSize, 2)

		// Expected: [L0, R0, L1, R1]
		expected := []float64{0.1, 0.2, 0.3, 0.4}
		if len(output) != len(expected) {
			t.Fatalf("output len = %d, want %d", len(output), len(expected))
		}
		for i, want := range expected {
			if output[i] != want {
				t.Errorf("output[%d] = %f, want %f", i, output[i], want)
			}
		}
	})

	t.Run("5.1 surround", func(t *testing.T) {
		// 2 coupled streams + 2 uncoupled
		// Stream 0: stereo (FL/FR) = [0.1, 0.2, 0.3, 0.4] (2 samples interleaved)
		// Stream 1: stereo (RL/RR) = [0.5, 0.6, 0.7, 0.8]
		// Stream 2: mono (C) = [0.9, 1.0]
		// Stream 3: mono (LFE) = [1.1, 1.2]
		decodedStreams := [][]float64{
			{0.1, 0.2, 0.3, 0.4}, // stream 0: FL/FR
			{0.5, 0.6, 0.7, 0.8}, // stream 1: RL/RR
			{0.9, 1.0},           // stream 2: C
			{1.1, 1.2},           // stream 3: LFE
		}
		// 5.1 mapping: FL, C, FR, RL, RR, LFE
		// mapping[0]=0 -> coupled 0, left (FL)
		// mapping[1]=4 -> uncoupled 2 (C)
		// mapping[2]=1 -> coupled 0, right (FR)
		// mapping[3]=2 -> coupled 1, left (RL)
		// mapping[4]=3 -> coupled 1, right (RR)
		// mapping[5]=5 -> uncoupled 3 (LFE)
		mapping := []byte{0, 4, 1, 2, 3, 5}
		frameSize := 2

		output := applyChannelMapping(decodedStreams, mapping, 2, frameSize, 6)

		// Output should be interleaved as:
		// Sample 0: [FL, C, FR, RL, RR, LFE] = [0.1, 0.9, 0.2, 0.5, 0.6, 1.1]
		// Sample 1: [FL, C, FR, RL, RR, LFE] = [0.3, 1.0, 0.4, 0.7, 0.8, 1.2]
		expected := []float64{
			0.1, 0.9, 0.2, 0.5, 0.6, 1.1, // sample 0
			0.3, 1.0, 0.4, 0.7, 0.8, 1.2, // sample 1
		}

		if len(output) != len(expected) {
			t.Fatalf("output len = %d, want %d", len(output), len(expected))
		}
		for i, want := range expected {
			if output[i] != want {
				t.Errorf("output[%d] = %f, want %f", i, output[i], want)
			}
		}
	})

	t.Run("silent channel", func(t *testing.T) {
		// 1 mono stream + 1 silent channel
		decodedStreams := [][]float64{
			{0.5, 0.6}, // stream 0: mono
		}
		mapping := []byte{0, 255} // ch0 = stream 0, ch1 = silent
		frameSize := 2

		output := applyChannelMapping(decodedStreams, mapping, 0, frameSize, 2)

		// Expected: [mono0, 0, mono1, 0]
		expected := []float64{0.5, 0.0, 0.6, 0.0}
		for i, want := range expected {
			if output[i] != want {
				t.Errorf("output[%d] = %f, want %f", i, output[i], want)
			}
		}
	})
}

// TestGetFrameDuration tests frame duration extraction from TOC.
func TestGetFrameDuration(t *testing.T) {
	tests := []struct {
		name     string
		toc      byte
		wantSize int
	}{
		// SILK NB configs 0-3
		{"SILK NB 10ms", 0x00, 480},
		{"SILK NB 20ms", 0x08, 960},
		{"SILK NB 40ms", 0x10, 1920},
		{"SILK NB 60ms", 0x18, 2880},
		// CELT FB configs 28-31
		{"CELT FB 2.5ms", 0xE0, 120},
		{"CELT FB 5ms", 0xE8, 240},
		{"CELT FB 10ms", 0xF0, 480},
		{"CELT FB 20ms", 0xF8, 960},
		// Hybrid SWB config 12
		{"Hybrid SWB 10ms", 0x60, 480},
		{"Hybrid SWB 20ms", 0x68, 960},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			packet := []byte{tc.toc, 0x00, 0x00} // TOC + some data
			got := getFrameDuration(packet)
			if got != tc.wantSize {
				t.Errorf("getFrameDuration([0x%02X...]) = %d, want %d", tc.toc, got, tc.wantSize)
			}
		})
	}

	t.Run("empty packet", func(t *testing.T) {
		got := getFrameDuration([]byte{})
		if got != 0 {
			t.Errorf("getFrameDuration([]) = %d, want 0", got)
		}
	})
}

// TestValidateStreamDurations tests duration validation across streams.
func TestValidateStreamDurations(t *testing.T) {
	t.Run("matching durations", func(t *testing.T) {
		// Both streams have CELT FB 20ms (config 31)
		packets := [][]byte{
			{0xF8, 0x01, 0x02}, // config 31 = 20ms
			{0xF8, 0x03, 0x04}, // config 31 = 20ms
		}
		dur, err := validateStreamDurations(packets)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if dur != 960 {
			t.Errorf("duration = %d, want 960", dur)
		}
	})

	t.Run("mismatched durations", func(t *testing.T) {
		packets := [][]byte{
			{0xF8, 0x01, 0x02}, // config 31 = 20ms (960)
			{0xF0, 0x03, 0x04}, // config 30 = 10ms (480)
		}
		_, err := validateStreamDurations(packets)
		if !errors.Is(err, ErrDurationMismatch) {
			t.Errorf("expected ErrDurationMismatch, got %v", err)
		}
	})

	t.Run("empty first packet", func(t *testing.T) {
		packets := [][]byte{
			{}, // empty
			{0xF8, 0x01, 0x02},
		}
		_, err := validateStreamDurations(packets)
		if !errors.Is(err, ErrPacketTooShort) {
			t.Errorf("expected ErrPacketTooShort, got %v", err)
		}
	})

	t.Run("no packets", func(t *testing.T) {
		_, err := validateStreamDurations([][]byte{})
		if !errors.Is(err, ErrInvalidStreamCount) {
			t.Errorf("expected ErrInvalidStreamCount, got %v", err)
		}
	})
}

// TestDecodePLC tests packet loss concealment.
func TestDecodePLC(t *testing.T) {
	streams, coupled, mapping, _ := DefaultMapping(2) // stereo
	dec, err := NewDecoder(48000, 2, streams, coupled, mapping)
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	// Call Decode with nil data (PLC)
	frameSize := 960 // 20ms
	samples, err := dec.Decode(nil, frameSize)
	if err != nil {
		t.Fatalf("Decode(nil) error: %v", err)
	}

	// Should return frameSize * channels samples
	expectedLen := frameSize * 2
	if len(samples) != expectedLen {
		t.Errorf("PLC output len = %d, want %d", len(samples), expectedLen)
	}

	// Call multiple times to verify fade
	for i := 0; i < 5; i++ {
		samples, err = dec.Decode(nil, frameSize)
		if err != nil {
			t.Fatalf("Decode(nil) iteration %d error: %v", i, err)
		}
		if len(samples) != expectedLen {
			t.Errorf("iteration %d: PLC output len = %d, want %d", i, len(samples), expectedLen)
		}
	}
}

// TestDecodeToInt16 tests int16 conversion wrapper.
func TestDecodeToInt16(t *testing.T) {
	streams, coupled, mapping, _ := DefaultMapping(1) // mono
	dec, err := NewDecoder(48000, 1, streams, coupled, mapping)
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	// Use PLC path for testing (nil data)
	samples, err := dec.DecodeToInt16(nil, 480)
	if err != nil {
		t.Fatalf("DecodeToInt16 error: %v", err)
	}

	// Should return frameSize * channels samples
	if len(samples) != 480 {
		t.Errorf("output len = %d, want 480", len(samples))
	}
}

// TestDecodeToFloat32 tests float32 conversion wrapper.
func TestDecodeToFloat32(t *testing.T) {
	streams, coupled, mapping, _ := DefaultMapping(1) // mono
	dec, err := NewDecoder(48000, 1, streams, coupled, mapping)
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	// Use PLC path for testing (nil data)
	samples, err := dec.DecodeToFloat32(nil, 480)
	if err != nil {
		t.Fatalf("DecodeToFloat32 error: %v", err)
	}

	// Should return frameSize * channels samples
	if len(samples) != 480 {
		t.Errorf("output len = %d, want 480", len(samples))
	}
}

// TestDecodeIntegration tests full decode path.
// This test is skipped because programmatic construction of valid
// multistream packets is complex (requires range-coded SILK/CELT frames).
func TestDecodeIntegration(t *testing.T) {
	t.Skip("Skipping integration test: programmatic multistream packet construction too complex (see Phase 4 experience)")

	// If we had valid multistream packet data, we would test:
	// streams, coupled, mapping, _ := DefaultMapping(6) // 5.1
	// dec, _ := NewDecoder(48000, 6, streams, coupled, mapping)
	// samples, err := dec.Decode(validMultistreamPacket, 960)
	// Verify: len(samples) == 960 * 6
}

// TestFloat64ToInt16 tests the sample conversion helper.
func TestFloat64ToInt16(t *testing.T) {
	tests := []struct {
		input float64
		want  int16
	}{
		{0.0, 0},
		{1.0, 32767},
		{-1.0, -32768},
		{0.5, 16384},
		{2.0, 32767},   // Clamped to max
		{-2.0, -32768}, // Clamped to min
	}

	for _, tc := range tests {
		input := []float64{tc.input}
		output := float64ToInt16(input)
		if output[0] != tc.want {
			t.Errorf("float64ToInt16(%f) = %d, want %d", tc.input, output[0], tc.want)
		}
	}
}

// TestFloat64ToFloat32 tests the sample conversion helper.
func TestFloat64ToFloat32(t *testing.T) {
	input := []float64{0.0, 0.5, -0.5, 1.0, -1.0}
	output := float64ToFloat32(input)

	if len(output) != len(input) {
		t.Fatalf("output len = %d, want %d", len(output), len(input))
	}

	for i, v := range input {
		if float64(output[i]) != v {
			t.Errorf("output[%d] = %f, want %f", i, output[i], v)
		}
	}
}

// TestDecoderReset tests the Reset method.
func TestDecoderReset(t *testing.T) {
	streams, coupled, mapping, _ := DefaultMapping(2)
	dec, err := NewDecoder(48000, 2, streams, coupled, mapping)
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}

	// Should not panic
	dec.Reset()
}

// TestDecoderPLCState verifies per-decoder PLC state behavior.
func TestDecoderPLCState(t *testing.T) {
	streams, coupled, mapping, _ := DefaultMapping(2)
	dec, err := NewDecoder(48000, 2, streams, coupled, mapping)
	if err != nil {
		t.Fatalf("NewDecoder error: %v", err)
	}
	if dec.plcState == nil {
		t.Fatal("decoder plcState is nil")
	}

	// Reset and verify
	dec.plcState.Reset()
	if dec.plcState.LostCount() != 0 {
		t.Errorf("LostCount after reset = %d, want 0", dec.plcState.LostCount())
	}
}
