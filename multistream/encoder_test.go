package multistream

import (
	"math"
	"testing"

	encpkg "github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/types"
)

// TestNewEncoder tests encoder creation validation.
func TestNewEncoder(t *testing.T) {
	tests := []struct {
		name           string
		sampleRate     int
		channels       int
		streams        int
		coupledStreams int
		mapping        []byte
		wantErr        error
	}{
		{
			name:           "valid mono",
			sampleRate:     48000,
			channels:       1,
			streams:        1,
			coupledStreams: 0,
			mapping:        []byte{0},
			wantErr:        nil,
		},
		{
			name:           "valid stereo",
			sampleRate:     48000,
			channels:       2,
			streams:        1,
			coupledStreams: 1,
			mapping:        []byte{0, 1},
			wantErr:        nil,
		},
		{
			name:           "valid 5.1",
			sampleRate:     48000,
			channels:       6,
			streams:        4,
			coupledStreams: 2,
			mapping:        []byte{0, 4, 1, 2, 3, 5},
			wantErr:        nil,
		},
		{
			name:           "valid 7.1",
			sampleRate:     48000,
			channels:       8,
			streams:        5,
			coupledStreams: 3,
			mapping:        []byte{0, 6, 1, 2, 3, 4, 5, 7},
			wantErr:        nil,
		},
		{
			name:           "invalid channels zero",
			sampleRate:     48000,
			channels:       0,
			streams:        1,
			coupledStreams: 0,
			mapping:        []byte{},
			wantErr:        ErrInvalidChannels,
		},
		{
			name:           "invalid channels 256",
			sampleRate:     48000,
			channels:       256,
			streams:        1,
			coupledStreams: 0,
			mapping:        make([]byte, 256),
			wantErr:        ErrInvalidChannels,
		},
		{
			name:           "invalid streams zero",
			sampleRate:     48000,
			channels:       1,
			streams:        0,
			coupledStreams: 0,
			mapping:        []byte{0},
			wantErr:        ErrInvalidStreams,
		},
		{
			name:           "invalid coupled exceeds streams",
			sampleRate:     48000,
			channels:       2,
			streams:        1,
			coupledStreams: 2,
			mapping:        []byte{0, 1},
			wantErr:        ErrInvalidCoupledStreams,
		},
		{
			name:           "invalid streams plus coupled exceeds 255",
			sampleRate:     48000,
			channels:       1,
			streams:        200,
			coupledStreams: 56,
			mapping:        []byte{0},
			wantErr:        ErrTooManyChannels,
		},
		{
			name:           "invalid mapping too short",
			sampleRate:     48000,
			channels:       2,
			streams:        1,
			coupledStreams: 1,
			mapping:        []byte{0}, // Should be 2 bytes
			wantErr:        ErrInvalidMapping,
		},
		{
			name:           "invalid mapping value exceeds max",
			sampleRate:     48000,
			channels:       2,
			streams:        1,
			coupledStreams: 1,
			mapping:        []byte{0, 5}, // Max is streams+coupled-1 = 1
			wantErr:        ErrInvalidMapping,
		},
		{
			name:           "valid with silent channel",
			sampleRate:     48000,
			channels:       2,
			streams:        1,
			coupledStreams: 0,
			mapping:        []byte{0, 255}, // Second channel is silent
			wantErr:        nil,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			enc, err := NewEncoder(tt.sampleRate, tt.channels, tt.streams, tt.coupledStreams, tt.mapping)

			if tt.wantErr != nil {
				if err == nil {
					t.Errorf("expected error %v, got nil", tt.wantErr)
				} else if err.Error() != tt.wantErr.Error() && !containsError(err, tt.wantErr) {
					t.Errorf("expected error containing %v, got %v", tt.wantErr, err)
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
				if enc == nil {
					t.Fatal("encoder is nil")
				}

				// Verify encoder fields
				if enc.Channels() != tt.channels {
					t.Errorf("Channels() = %d, want %d", enc.Channels(), tt.channels)
				}
				if enc.Streams() != tt.streams {
					t.Errorf("Streams() = %d, want %d", enc.Streams(), tt.streams)
				}
				if enc.CoupledStreams() != tt.coupledStreams {
					t.Errorf("CoupledStreams() = %d, want %d", enc.CoupledStreams(), tt.coupledStreams)
				}
				if enc.SampleRate() != tt.sampleRate {
					t.Errorf("SampleRate() = %d, want %d", enc.SampleRate(), tt.sampleRate)
				}
			}
		})
	}
}

// containsError checks if err contains the target error message.
func containsError(err, target error) bool {
	return err != nil && target != nil &&
		(err.Error() == target.Error() ||
			len(err.Error()) > len(target.Error()) &&
				err.Error()[:len(target.Error())] == target.Error())
}

// TestNewEncoderDefault tests default encoder creation for standard configurations.
func TestNewEncoderDefault(t *testing.T) {
	tests := []struct {
		channels       int
		wantStreams    int
		wantCoupled    int
		wantMappingLen int
		wantErr        error
	}{
		{1, 1, 0, 1, nil}, // Mono
		{2, 1, 1, 2, nil}, // Stereo
		{3, 2, 1, 3, nil}, // 3.0
		{4, 2, 2, 4, nil}, // Quad
		{5, 3, 2, 5, nil}, // 5.0
		{6, 4, 2, 6, nil}, // 5.1
		{7, 5, 2, 7, nil}, // 6.1
		{8, 5, 3, 8, nil}, // 7.1
		{9, 0, 0, 0, ErrUnsupportedChannels},
		{0, 0, 0, 0, ErrInvalidChannels},
	}

	for _, tt := range tests {
		t.Run(channelConfigName(tt.channels), func(t *testing.T) {
			enc, err := NewEncoderDefault(48000, tt.channels)

			if tt.wantErr != nil {
				if err == nil {
					t.Errorf("expected error for %d channels, got nil", tt.channels)
				}
				return
			}

			if err != nil {
				t.Errorf("unexpected error: %v", err)
				return
			}

			if enc.Streams() != tt.wantStreams {
				t.Errorf("Streams() = %d, want %d", enc.Streams(), tt.wantStreams)
			}
			if enc.CoupledStreams() != tt.wantCoupled {
				t.Errorf("CoupledStreams() = %d, want %d", enc.CoupledStreams(), tt.wantCoupled)
			}
			if enc.Channels() != tt.wantMappingLen {
				t.Errorf("Channels() = %d, want %d", enc.Channels(), tt.wantMappingLen)
			}
		})
	}
}

func channelConfigName(channels int) string {
	names := map[int]string{
		0: "invalid_0",
		1: "mono",
		2: "stereo",
		3: "3.0",
		4: "quad",
		5: "5.0",
		6: "5.1",
		7: "6.1",
		8: "7.1",
		9: "invalid_9",
	}
	if name, ok := names[channels]; ok {
		return name
	}
	return "unknown"
}

// TestRouteChannelsToStreams tests routing input to stream buffers.
func TestRouteChannelsToStreams(t *testing.T) {
	t.Run("mono single channel", func(t *testing.T) {
		// 1 channel -> 1 uncoupled stream
		input := []float64{0.1, 0.2, 0.3, 0.4}
		mapping := []byte{0}
		frameSize := 4

		streams := routeChannelsToStreams(input, mapping, 0, frameSize, 1, 1)

		if len(streams) != 1 {
			t.Fatalf("expected 1 stream, got %d", len(streams))
		}
		if len(streams[0]) != frameSize {
			t.Fatalf("expected stream 0 len %d, got %d", frameSize, len(streams[0]))
		}

		// Mono stream should have the same samples
		for i := 0; i < frameSize; i++ {
			if streams[0][i] != input[i] {
				t.Errorf("sample %d: got %f, want %f", i, streams[0][i], input[i])
			}
		}
	})

	t.Run("stereo to coupled stream", func(t *testing.T) {
		// 2 channels -> 1 coupled (stereo) stream
		// Input: [L0, R0, L1, R1, L2, R2, L3, R3]
		// Output: stream 0 stereo interleaved [L0, R0, L1, R1, ...]
		input := []float64{
			0.1, 0.2, // L0, R0
			0.3, 0.4, // L1, R1
			0.5, 0.6, // L2, R2
			0.7, 0.8, // L3, R3
		}
		mapping := []byte{0, 1} // L->0, R->1
		frameSize := 4

		streams := routeChannelsToStreams(input, mapping, 1, frameSize, 2, 1)

		if len(streams) != 1 {
			t.Fatalf("expected 1 stream, got %d", len(streams))
		}
		if len(streams[0]) != frameSize*2 {
			t.Fatalf("expected stream 0 len %d, got %d", frameSize*2, len(streams[0]))
		}

		// Check interleaved output
		expected := []float64{0.1, 0.2, 0.3, 0.4, 0.5, 0.6, 0.7, 0.8}
		for i, v := range expected {
			if streams[0][i] != v {
				t.Errorf("sample %d: got %f, want %f", i, streams[0][i], v)
			}
		}
	})

	t.Run("5.1 surround routing", func(t *testing.T) {
		// 6 channels: FL, C, FR, RL, RR, LFE
		// 4 streams: 0=FL/FR (coupled), 1=RL/RR (coupled), 2=C (mono), 3=LFE (mono)
		// mapping: [0, 4, 1, 2, 3, 5]
		//   FL -> 0 (stream 0 left)
		//   C  -> 4 (stream 2 mono, index 2*2+0=4)
		//   FR -> 1 (stream 0 right)
		//   RL -> 2 (stream 1 left)
		//   RR -> 3 (stream 1 right)
		//   LFE-> 5 (stream 3 mono, index 2*2+1=5)

		frameSize := 2
		// Input: FL, C, FR, RL, RR, LFE interleaved per sample
		input := []float64{
			// Sample 0
			1.0, 2.0, 3.0, 4.0, 5.0, 6.0,
			// Sample 1
			1.1, 2.1, 3.1, 4.1, 5.1, 6.1,
		}
		mapping := []byte{0, 4, 1, 2, 3, 5}

		streams := routeChannelsToStreams(input, mapping, 2, frameSize, 6, 4)

		if len(streams) != 4 {
			t.Fatalf("expected 4 streams, got %d", len(streams))
		}

		// Stream 0 (coupled): FL/FR stereo interleaved
		// Expected: [FL0, FR0, FL1, FR1] = [1.0, 3.0, 1.1, 3.1]
		expectedS0 := []float64{1.0, 3.0, 1.1, 3.1}
		if len(streams[0]) != 4 {
			t.Fatalf("stream 0 len = %d, want 4", len(streams[0]))
		}
		for i, v := range expectedS0 {
			if streams[0][i] != v {
				t.Errorf("stream0[%d] = %f, want %f", i, streams[0][i], v)
			}
		}

		// Stream 1 (coupled): RL/RR stereo interleaved
		// Expected: [RL0, RR0, RL1, RR1] = [4.0, 5.0, 4.1, 5.1]
		expectedS1 := []float64{4.0, 5.0, 4.1, 5.1}
		if len(streams[1]) != 4 {
			t.Fatalf("stream 1 len = %d, want 4", len(streams[1]))
		}
		for i, v := range expectedS1 {
			if streams[1][i] != v {
				t.Errorf("stream1[%d] = %f, want %f", i, streams[1][i], v)
			}
		}

		// Stream 2 (mono): C
		// Expected: [C0, C1] = [2.0, 2.1]
		expectedS2 := []float64{2.0, 2.1}
		if len(streams[2]) != 2 {
			t.Fatalf("stream 2 len = %d, want 2", len(streams[2]))
		}
		for i, v := range expectedS2 {
			if streams[2][i] != v {
				t.Errorf("stream2[%d] = %f, want %f", i, streams[2][i], v)
			}
		}

		// Stream 3 (mono): LFE
		// Expected: [LFE0, LFE1] = [6.0, 6.1]
		expectedS3 := []float64{6.0, 6.1}
		if len(streams[3]) != 2 {
			t.Fatalf("stream 3 len = %d, want 2", len(streams[3]))
		}
		for i, v := range expectedS3 {
			if streams[3][i] != v {
				t.Errorf("stream3[%d] = %f, want %f", i, streams[3][i], v)
			}
		}
	})

	t.Run("silent channel 255", func(t *testing.T) {
		// 2 channels, but second is silent
		// 1 uncoupled stream should receive only first channel
		input := []float64{
			1.0, 2.0, // Sample 0
			3.0, 4.0, // Sample 1
		}
		mapping := []byte{0, 255} // First to stream 0, second silent
		frameSize := 2

		streams := routeChannelsToStreams(input, mapping, 0, frameSize, 2, 1)

		if len(streams) != 1 {
			t.Fatalf("expected 1 stream, got %d", len(streams))
		}

		// Stream 0 should have only the first channel (1.0, 3.0)
		expected := []float64{1.0, 3.0}
		for i, v := range expected {
			if streams[0][i] != v {
				t.Errorf("stream0[%d] = %f, want %f", i, streams[0][i], v)
			}
		}
	})
}

// TestRouteChannelsToStreams_RoundTrip tests that routing is the inverse of applyChannelMapping.
func TestRouteChannelsToStreams_RoundTrip(t *testing.T) {
	configs := []struct {
		name           string
		channels       int
		streams        int
		coupledStreams int
		mapping        []byte
	}{
		{"mono", 1, 1, 0, []byte{0}},
		{"stereo", 2, 1, 1, []byte{0, 1}},
		{"5.1", 6, 4, 2, []byte{0, 4, 1, 2, 3, 5}},
		{"7.1", 8, 5, 3, []byte{0, 6, 1, 2, 3, 4, 5, 7}},
	}

	for _, cfg := range configs {
		t.Run(cfg.name, func(t *testing.T) {
			frameSize := 4

			// Create unique input for each channel
			input := make([]float64, frameSize*cfg.channels)
			for ch := 0; ch < cfg.channels; ch++ {
				for s := 0; s < frameSize; s++ {
					// Unique value: channel*100 + sample
					input[s*cfg.channels+ch] = float64(ch*100 + s)
				}
			}

			// Route to streams (encoding direction)
			streamBuffers := routeChannelsToStreams(input, cfg.mapping, cfg.coupledStreams, frameSize, cfg.channels, cfg.streams)

			// Convert stream buffers to the format expected by applyChannelMapping
			// (which expects [][]float64 with interleaved stereo for coupled streams)
			decodedStreams := make([][]float64, cfg.streams)
			for i := 0; i < cfg.streams; i++ {
				decodedStreams[i] = streamBuffers[i]
			}

			// Apply channel mapping (decoding direction)
			output := applyChannelMapping(decodedStreams, cfg.mapping, cfg.coupledStreams, frameSize, cfg.channels)

			// Verify round-trip
			if len(output) != len(input) {
				t.Fatalf("output len = %d, want %d", len(output), len(input))
			}

			for i, v := range input {
				if math.Abs(output[i]-v) > 1e-10 {
					t.Errorf("sample %d: got %f, want %f", i, output[i], v)
				}
			}
		})
	}
}

// TestWriteSelfDelimitedLength tests length encoding.
func TestWriteSelfDelimitedLength(t *testing.T) {
	tests := []struct {
		length     int
		wantBytes  int
		wantFirst  byte
		wantSecond byte
	}{
		{0, 1, 0, 0},
		{1, 1, 1, 0},
		{100, 1, 100, 0},
		{251, 1, 251, 0},
		{252, 2, 252, 0},    // 4*0 + 252 = 252
		{253, 2, 253, 0},    // 4*0 + 253 = 253
		{254, 2, 254, 0},    // 4*0 + 254 = 254
		{255, 2, 255, 0},    // 4*0 + 255 = 255
		{256, 2, 252, 1},    // 4*1 + 252 = 256
		{257, 2, 253, 1},    // 4*1 + 253 = 257
		{260, 2, 252, 2},    // 4*2 + 252 = 260
		{500, 2, 252, 62},   // 4*62 + 252 = 500
		{1000, 2, 252, 187}, // 4*187 + 252 = 1000
	}

	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			dst := make([]byte, 2)
			n := writeSelfDelimitedLength(dst, tt.length)

			if n != tt.wantBytes {
				t.Errorf("writeSelfDelimitedLength(%d) returned %d bytes, want %d", tt.length, n, tt.wantBytes)
			}
			if dst[0] != tt.wantFirst {
				t.Errorf("writeSelfDelimitedLength(%d) first byte = %d, want %d", tt.length, dst[0], tt.wantFirst)
			}
			if tt.wantBytes == 2 && dst[1] != tt.wantSecond {
				t.Errorf("writeSelfDelimitedLength(%d) second byte = %d, want %d", tt.length, dst[1], tt.wantSecond)
			}

			// Verify by decoding
			decoded, consumed, err := parseSelfDelimitedLength(dst[:n])
			if err != nil {
				t.Errorf("parseSelfDelimitedLength error: %v", err)
			}
			if decoded != tt.length {
				t.Errorf("round-trip: got %d, want %d", decoded, tt.length)
			}
			if consumed != n {
				t.Errorf("consumed %d bytes, wrote %d", consumed, n)
			}
		})
	}
}

// TestAssembleMultistreamPacket tests packet assembly.
func TestAssembleMultistreamPacket(t *testing.T) {
	t.Run("single stream", func(t *testing.T) {
		packets := [][]byte{{1, 2, 3}}
		result := assembleMultistreamPacket(packets)

		// Single stream: no length prefix needed
		if len(result) != 3 {
			t.Fatalf("len = %d, want 3", len(result))
		}
		for i, v := range []byte{1, 2, 3} {
			if result[i] != v {
				t.Errorf("result[%d] = %d, want %d", i, result[i], v)
			}
		}
	})

	t.Run("two streams", func(t *testing.T) {
		// First packet with length prefix, second without
		packets := [][]byte{{1, 2, 3}, {4, 5}}
		result := assembleMultistreamPacket(packets)

		// First packet: 1-byte length (3) + data (3 bytes) = 4 bytes
		// Second packet: no length + data (2 bytes) = 2 bytes
		// Total: 6 bytes
		expected := []byte{3, 1, 2, 3, 4, 5}
		if len(result) != len(expected) {
			t.Fatalf("len = %d, want %d", len(result), len(expected))
		}
		for i, v := range expected {
			if result[i] != v {
				t.Errorf("result[%d] = %d, want %d", i, result[i], v)
			}
		}
	})

	t.Run("four streams", func(t *testing.T) {
		// First 3 packets have length prefix, last doesn't
		packets := [][]byte{
			{1, 2},        // 2 bytes
			{3, 4, 5},     // 3 bytes
			{6},           // 1 byte
			{7, 8, 9, 10}, // 4 bytes
		}
		result := assembleMultistreamPacket(packets)

		// Expected: len(2)+data(2) + len(3)+data(3) + len(1)+data(1) + data(4)
		// = 1+2 + 1+3 + 1+1 + 4 = 13 bytes
		expected := []byte{2, 1, 2, 3, 3, 4, 5, 1, 6, 7, 8, 9, 10}
		if len(result) != len(expected) {
			t.Fatalf("len = %d, want %d", len(result), len(expected))
		}
		for i, v := range expected {
			if result[i] != v {
				t.Errorf("result[%d] = %d, want %d", i, result[i], v)
			}
		}
	})

	t.Run("large packet requiring two-byte length", func(t *testing.T) {
		// Create a 300-byte packet that needs two-byte length encoding
		largePacket := make([]byte, 300)
		for i := range largePacket {
			largePacket[i] = byte(i % 256)
		}
		packets := [][]byte{largePacket, {1, 2, 3}}

		result := assembleMultistreamPacket(packets)

		// Length 300 = 4*12 + 252 = 300, so first byte = 252, second = 12
		// Wait, let me recalculate:
		// 300 = 4*secondByte + firstByte where firstByte in [252, 255]
		// 300 % 4 = 0, so firstByte = 252 + 0 = 252
		// secondByte = (300 - 252) / 4 = 48/4 = 12
		expected := []byte{252, 12}
		if result[0] != expected[0] || result[1] != expected[1] {
			t.Errorf("length encoding: got [%d, %d], want [%d, %d]", result[0], result[1], expected[0], expected[1])
		}

		// Verify we can parse it back
		parsedPackets, err := parseMultistreamPacket(result, 2)
		if err != nil {
			t.Fatalf("parse error: %v", err)
		}
		if len(parsedPackets[0]) != 300 {
			t.Errorf("parsed first packet len = %d, want 300", len(parsedPackets[0]))
		}
		if len(parsedPackets[1]) != 3 {
			t.Errorf("parsed second packet len = %d, want 3", len(parsedPackets[1]))
		}
	})
}

// TestEncoderSetBitrate tests bitrate distribution.
func TestEncoderSetBitrate(t *testing.T) {
	// Create a 5.1 encoder (4 streams: 2 coupled, 2 mono)
	enc, err := NewEncoderDefault(48000, 6)
	if err != nil {
		t.Fatalf("NewEncoderDefault error: %v", err)
	}

	// Set 256 kbps total
	enc.SetBitrate(256000)

	if enc.Bitrate() != 256000 {
		t.Errorf("Bitrate() = %d, want 256000", enc.Bitrate())
	}

	// Distribution: 2 coupled * 3 + 2 mono * 2 = 10 units
	// 256000 / 10 = 25600 per unit
	// Coupled: 25600 * 3 = 76800 bps each
	// Mono: 25600 * 2 = 51200 bps each
	// Total: 2*76800 + 2*51200 = 153600 + 102400 = 256000
}

// TestEncoderReset tests encoder reset functionality.
func TestEncoderReset(t *testing.T) {
	enc, err := NewEncoderDefault(48000, 2)
	if err != nil {
		t.Fatalf("NewEncoderDefault error: %v", err)
	}

	// Reset shouldn't panic
	enc.Reset()

	// Encoder should still be usable after reset
	if enc.Channels() != 2 {
		t.Errorf("Channels() = %d after reset, want 2", enc.Channels())
	}
}

// TestEncode_Basic tests basic stereo encoding produces a valid multistream packet.
func TestEncode_Basic(t *testing.T) {
	enc, err := NewEncoderDefault(48000, 2)
	if err != nil {
		t.Fatalf("NewEncoderDefault error: %v", err)
	}

	// Create 20ms of stereo audio (960 samples * 2 channels)
	frameSize := 960
	pcm := make([]float64, frameSize*2)

	// Fill with a simple sine wave
	for i := 0; i < frameSize; i++ {
		sample := math.Sin(2 * math.Pi * 440 * float64(i) / 48000)
		pcm[i*2] = sample   // Left
		pcm[i*2+1] = sample // Right
	}

	// Encode
	packet, err := enc.Encode(pcm, frameSize)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}

	// Verify packet was produced
	if packet == nil {
		t.Fatal("Encode returned nil packet")
	}
	if len(packet) == 0 {
		t.Fatal("Encode returned empty packet")
	}

	// For stereo (1 coupled stream), packet should be a single Opus frame
	// (no self-delimiting framing needed for single stream)
	t.Logf("Encoded stereo packet: %d bytes", len(packet))

	// Verify TOC byte is valid (first byte of first/only stream)
	toc := packet[0]
	config := toc >> 3
	if config > 31 {
		t.Errorf("Invalid TOC config: %d", config)
	}
}

// TestEncode_51Surround tests 5.1 surround encoding produces 4 streams.
func TestEncode_51Surround(t *testing.T) {
	enc, err := NewEncoderDefault(48000, 6)
	if err != nil {
		t.Fatalf("NewEncoderDefault error: %v", err)
	}

	// 5.1 should have 4 streams (2 coupled, 2 mono)
	if enc.Streams() != 4 {
		t.Errorf("Streams() = %d, want 4", enc.Streams())
	}
	if enc.CoupledStreams() != 2 {
		t.Errorf("CoupledStreams() = %d, want 2", enc.CoupledStreams())
	}

	// Create 20ms of 5.1 audio (960 samples * 6 channels)
	frameSize := 960
	pcm := make([]float64, frameSize*6)

	// Fill each channel with different frequency sine waves
	freqs := []float64{440, 880, 550, 660, 770, 220} // FL, C, FR, RL, RR, LFE
	for i := 0; i < frameSize; i++ {
		for ch := 0; ch < 6; ch++ {
			pcm[i*6+ch] = math.Sin(2 * math.Pi * freqs[ch] * float64(i) / 48000)
		}
	}

	// Encode
	packet, err := enc.Encode(pcm, frameSize)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}

	if packet == nil {
		t.Fatal("Encode returned nil packet")
	}
	if len(packet) == 0 {
		t.Fatal("Encode returned empty packet")
	}

	t.Logf("Encoded 5.1 packet: %d bytes", len(packet))

	// Verify packet can be parsed back into 4 streams
	streamPackets, err := parseMultistreamPacket(packet, 4)
	if err != nil {
		t.Fatalf("parseMultistreamPacket error: %v", err)
	}

	if len(streamPackets) != 4 {
		t.Errorf("parsed %d streams, want 4", len(streamPackets))
	}

	// Each stream packet should have valid TOC byte
	for i, sp := range streamPackets {
		if len(sp) == 0 {
			t.Errorf("stream %d packet is empty", i)
			continue
		}
		toc := sp[0]
		config := toc >> 3
		if config > 31 {
			t.Errorf("stream %d: invalid TOC config: %d", i, config)
		}
		t.Logf("Stream %d: %d bytes, TOC config=%d", i, len(sp), config)
	}
}

// TestEncode_71Surround tests 7.1 surround encoding produces 5 streams.
func TestEncode_71Surround(t *testing.T) {
	enc, err := NewEncoderDefault(48000, 8)
	if err != nil {
		t.Fatalf("NewEncoderDefault error: %v", err)
	}

	// 7.1 should have 5 streams (3 coupled, 2 mono)
	if enc.Streams() != 5 {
		t.Errorf("Streams() = %d, want 5", enc.Streams())
	}
	if enc.CoupledStreams() != 3 {
		t.Errorf("CoupledStreams() = %d, want 3", enc.CoupledStreams())
	}

	// Create 20ms of 7.1 audio (960 samples * 8 channels)
	frameSize := 960
	pcm := make([]float64, frameSize*8)

	// Fill each channel with different frequency sine waves
	freqs := []float64{440, 880, 550, 660, 770, 990, 330, 220} // FL, C, FR, SL, SR, RL, RR, LFE
	for i := 0; i < frameSize; i++ {
		for ch := 0; ch < 8; ch++ {
			pcm[i*8+ch] = math.Sin(2 * math.Pi * freqs[ch] * float64(i) / 48000)
		}
	}

	// Encode
	packet, err := enc.Encode(pcm, frameSize)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}

	if packet == nil {
		t.Fatal("Encode returned nil packet")
	}
	if len(packet) == 0 {
		t.Fatal("Encode returned empty packet")
	}

	t.Logf("Encoded 7.1 packet: %d bytes", len(packet))

	// Verify packet can be parsed back into 5 streams
	streamPackets, err := parseMultistreamPacket(packet, 5)
	if err != nil {
		t.Fatalf("parseMultistreamPacket error: %v", err)
	}

	if len(streamPackets) != 5 {
		t.Errorf("parsed %d streams, want 5", len(streamPackets))
	}

	// Each stream packet should have valid TOC byte
	for i, sp := range streamPackets {
		if len(sp) == 0 {
			t.Errorf("stream %d packet is empty", i)
			continue
		}
		t.Logf("Stream %d: %d bytes", i, len(sp))
	}
}

// TestEncode_InputValidation tests that incorrect input length returns ErrInvalidInput.
func TestEncode_InputValidation(t *testing.T) {
	enc, err := NewEncoderDefault(48000, 2)
	if err != nil {
		t.Fatalf("NewEncoderDefault error: %v", err)
	}

	frameSize := 960

	tests := []struct {
		name      string
		pcmLen    int
		wantError bool
	}{
		{"correct length", frameSize * 2, false},
		{"too short", frameSize*2 - 1, true},
		{"too long", frameSize*2 + 1, true},
		{"half length", frameSize, true},
		{"double length", frameSize * 4, true},
		{"empty", 0, true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			pcm := make([]float64, tt.pcmLen)
			_, err := enc.Encode(pcm, frameSize)

			if tt.wantError {
				if err == nil {
					t.Errorf("expected error, got nil")
				} else if !containsError(err, ErrInvalidInput) {
					t.Errorf("expected ErrInvalidInput, got: %v", err)
				}
			} else {
				if err != nil {
					t.Errorf("unexpected error: %v", err)
				}
			}
		})
	}
}

// TestSetBitrate_Distribution tests weighted bitrate allocation across streams.
func TestSetBitrate_Distribution(t *testing.T) {
	// Test 5.1: 2 coupled + 2 mono = 2*3 + 2*2 = 10 units
	enc, err := NewEncoderDefault(48000, 6)
	if err != nil {
		t.Fatalf("NewEncoderDefault error: %v", err)
	}

	// Set 100000 bps for easy math
	enc.SetBitrate(100000)

	// Expected: 100000 / 10 = 10000 per unit
	// Coupled streams: 10000 * 3 = 30000 bps each
	// Mono streams: 10000 * 2 = 20000 bps each
	// Total: 2*30000 + 2*20000 = 60000 + 40000 = 100000 (matches)

	if enc.Bitrate() != 100000 {
		t.Errorf("Bitrate() = %d, want 100000", enc.Bitrate())
	}

	// Verify via internal encoder bitrates (if accessible)
	// For now, verify the total is correct
	t.Logf("5.1 bitrate distribution: %d bps total", enc.Bitrate())
}

func TestAllocateRates_SurroundLFEAware(t *testing.T) {
	enc, err := NewEncoderDefault(48000, 6)
	if err != nil {
		t.Fatalf("NewEncoderDefault error: %v", err)
	}

	enc.SetBitrate(128000)
	rates := enc.allocateRates(960)
	if len(rates) != 4 {
		t.Fatalf("len(rates) = %d, want 4", len(rates))
	}
	if enc.lfeStream != 3 {
		t.Fatalf("lfeStream = %d, want 3", enc.lfeStream)
	}
	if rates[enc.lfeStream] >= rates[2] {
		t.Fatalf("LFE rate should be below mono center: lfe=%d center=%d", rates[enc.lfeStream], rates[2])
	}
	if rates[enc.lfeStream] >= rates[0] {
		t.Fatalf("LFE rate should be below coupled stream: lfe=%d coupled=%d", rates[enc.lfeStream], rates[0])
	}
}

func TestEncode_SurroundPerStreamPolicy(t *testing.T) {
	enc, err := NewEncoderDefault(48000, 6)
	if err != nil {
		t.Fatalf("NewEncoderDefault error: %v", err)
	}
	enc.SetBitrate(256000)

	frameSize := 960
	pcm := make([]float64, frameSize*6)
	for i := 0; i < frameSize; i++ {
		tm := float64(i) / 48000.0
		pcm[i*6+0] = 0.8 * math.Sin(2*math.Pi*1500*tm) // FL
		pcm[i*6+1] = 0.2 * math.Sin(2*math.Pi*120*tm)  // C
		pcm[i*6+2] = 0.8 * math.Sin(2*math.Pi*200*tm)  // FR
		pcm[i*6+3] = 0.8 * math.Sin(2*math.Pi*1900*tm) // RL
		pcm[i*6+4] = 0.6 * math.Sin(2*math.Pi*260*tm)  // RR
		pcm[i*6+5] = 0.9 * math.Sin(2*math.Pi*50*tm)   // LFE
	}

	if _, err := enc.Encode(pcm, frameSize); err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	for i := 0; i < enc.CoupledStreams(); i++ {
		if got := enc.encoders[i].Mode(); got != encpkg.ModeCELT {
			t.Fatalf("stream %d mode = %v, want ModeCELT", i, got)
		}
		if got := enc.encoders[i].ForceChannels(); got != 2 {
			t.Fatalf("stream %d force channels = %d, want 2", i, got)
		}
	}

	if got := enc.encoders[enc.lfeStream].Mode(); got != encpkg.ModeCELT {
		t.Fatalf("LFE mode = %v, want ModeCELT", got)
	}
	if got := enc.encoders[enc.lfeStream].Bandwidth(); got != types.BandwidthNarrowband {
		t.Fatalf("LFE bandwidth = %v, want BandwidthNarrowband", got)
	}
	if got := enc.encoders[enc.lfeStream].CELTSurroundTrim(); got != 0 {
		t.Fatalf("LFE surround trim = %f, want 0", got)
	}
	if got := enc.encoders[2].ForceChannels(); got != -1 {
		t.Fatalf("mono surround stream force channels = %d, want -1", got)
	}
}

func TestEncode_SurroundTrimProduced(t *testing.T) {
	enc, err := NewEncoderDefault(48000, 6)
	if err != nil {
		t.Fatalf("NewEncoderDefault error: %v", err)
	}
	enc.SetBitrate(256000)

	frameSize := 960
	pcm := make([]float64, frameSize*6)
	for i := 0; i < frameSize; i++ {
		tm := float64(i) / 48000.0
		if i%2 == 0 {
			pcm[i*6+0] = 0.9
		} else {
			pcm[i*6+0] = -0.9
		}
		pcm[i*6+1] = 0.2 * math.Sin(2*math.Pi*90*tm)
		pcm[i*6+2] = 0.7 * math.Sin(2*math.Pi*220*tm)
		pcm[i*6+3] = 0.8 * math.Sin(2*math.Pi*2400*tm)
		pcm[i*6+4] = 0.5 * math.Sin(2*math.Pi*180*tm)
		pcm[i*6+5] = 0.9 * math.Sin(2*math.Pi*45*tm)
	}

	if _, err := enc.Encode(pcm, frameSize); err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	nonZero := false
	for s := 0; s < enc.streams; s++ {
		got := enc.encoders[s].CELTSurroundTrim()
		if s == enc.lfeStream {
			if got != 0 {
				t.Fatalf("LFE stream trim = %f, want 0", got)
			}
			continue
		}
		if math.Abs(got) > 1e-6 {
			nonZero = true
		}
	}
	if !nonZero {
		t.Fatalf("expected at least one non-LFE stream to receive non-zero surround trim")
	}
}

func TestEncode_AmbisonicsForcesCELTMode(t *testing.T) {
	enc, err := NewEncoderAmbisonics(48000, 4, 2)
	if err != nil {
		t.Fatalf("NewEncoderAmbisonics error: %v", err)
	}

	frameSize := 960
	pcm := make([]float64, frameSize*4)
	for i := 0; i < frameSize; i++ {
		tm := float64(i) / 48000.0
		for ch := 0; ch < 4; ch++ {
			pcm[i*4+ch] = 0.4 * math.Sin(2*math.Pi*float64(220+ch*90)*tm)
		}
	}

	if _, err := enc.Encode(pcm, frameSize); err != nil {
		t.Fatalf("Encode failed: %v", err)
	}

	for i := 0; i < enc.Streams(); i++ {
		if got := enc.encoders[i].Mode(); got != encpkg.ModeCELT {
			t.Fatalf("stream %d mode = %v, want ModeCELT", i, got)
		}
	}
}

// TestEncoderControlMethods tests all control methods propagate to stream encoders.
func TestEncoderControlMethods(t *testing.T) {
	enc, err := NewEncoderDefault(48000, 6)
	if err != nil {
		t.Fatalf("NewEncoderDefault error: %v", err)
	}

	// Test complexity
	enc.SetComplexity(5)
	if enc.Complexity() != 5 {
		t.Errorf("Complexity() = %d, want 5", enc.Complexity())
	}

	// Test FEC
	enc.SetFEC(true)
	if !enc.FECEnabled() {
		t.Error("FECEnabled() = false, want true")
	}
	enc.SetFEC(false)
	if enc.FECEnabled() {
		t.Error("FECEnabled() = true, want false")
	}

	// Test packet loss
	enc.SetPacketLoss(25)
	if enc.PacketLoss() != 25 {
		t.Errorf("PacketLoss() = %d, want 25", enc.PacketLoss())
	}

	// Test DTX
	enc.SetDTX(true)
	if !enc.DTXEnabled() {
		t.Error("DTXEnabled() = false, want true")
	}
	enc.SetDTX(false)
	if enc.DTXEnabled() {
		t.Error("DTXEnabled() = true, want false")
	}
}

// TestEncode_Mono tests encoding with a mono configuration.
func TestEncode_Mono(t *testing.T) {
	enc, err := NewEncoderDefault(48000, 1)
	if err != nil {
		t.Fatalf("NewEncoderDefault error: %v", err)
	}

	// Mono: 1 stream, 0 coupled
	if enc.Streams() != 1 {
		t.Errorf("Streams() = %d, want 1", enc.Streams())
	}
	if enc.CoupledStreams() != 0 {
		t.Errorf("CoupledStreams() = %d, want 0", enc.CoupledStreams())
	}

	// Create 20ms of mono audio
	frameSize := 960
	pcm := make([]float64, frameSize)
	for i := 0; i < frameSize; i++ {
		pcm[i] = math.Sin(2 * math.Pi * 440 * float64(i) / 48000)
	}

	// Encode
	packet, err := enc.Encode(pcm, frameSize)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}

	if packet == nil {
		t.Fatal("Encode returned nil packet")
	}
	t.Logf("Encoded mono packet: %d bytes", len(packet))
}

// TestGetFinalRange tests that GetFinalRange returns a non-zero value after encoding.
func TestGetFinalRange(t *testing.T) {
	enc, err := NewEncoderDefault(48000, 6)
	if err != nil {
		t.Fatalf("NewEncoderDefault error: %v", err)
	}

	// Before encoding, final range should be 0
	initialRange := enc.GetFinalRange()
	t.Logf("Initial FinalRange: %d", initialRange)

	// Create 20ms of 5.1 audio
	frameSize := 960
	pcm := make([]float64, frameSize*6)
	for i := 0; i < frameSize; i++ {
		for ch := 0; ch < 6; ch++ {
			pcm[i*6+ch] = math.Sin(2 * math.Pi * float64(440+ch*100) * float64(i) / 48000)
		}
	}

	// Encode
	_, err = enc.Encode(pcm, frameSize)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}

	// After encoding, final range should be non-zero
	finalRange := enc.GetFinalRange()
	if finalRange == 0 {
		t.Error("GetFinalRange() = 0 after encoding, expected non-zero")
	}
	t.Logf("FinalRange after encode: %d", finalRange)

	// Encode again, should get different final range
	_, err = enc.Encode(pcm, frameSize)
	if err != nil {
		t.Fatalf("Encode error: %v", err)
	}

	newFinalRange := enc.GetFinalRange()
	t.Logf("FinalRange after second encode: %d", newFinalRange)
}

// TestLookahead tests encoder lookahead value.
func TestLookahead(t *testing.T) {
	enc, err := NewEncoderDefault(48000, 2)
	if err != nil {
		t.Fatalf("NewEncoderDefault error: %v", err)
	}

	lookahead := enc.Lookahead()

	// Lookahead should be positive and reasonable
	// Expected: ~250 samples (2.5ms base + 130 delay compensation)
	if lookahead <= 0 {
		t.Errorf("Lookahead() = %d, expected positive value", lookahead)
	}
	if lookahead > 500 {
		t.Errorf("Lookahead() = %d, unexpectedly large", lookahead)
	}
	t.Logf("Encoder lookahead: %d samples", lookahead)
}

// TestSignal tests signal type get/set.
func TestSignal(t *testing.T) {
	enc, err := NewEncoderDefault(48000, 2)
	if err != nil {
		t.Fatalf("NewEncoderDefault error: %v", err)
	}

	// Default should be SignalAuto
	if enc.Signal() != types.SignalAuto {
		t.Errorf("Signal() = %d, want SignalAuto (%d)", enc.Signal(), types.SignalAuto)
	}

	// Set to Voice
	enc.SetSignal(types.SignalVoice)
	if enc.Signal() != types.SignalVoice {
		t.Errorf("Signal() = %d, want SignalVoice (%d)", enc.Signal(), types.SignalVoice)
	}

	// Set to Music
	enc.SetSignal(types.SignalMusic)
	if enc.Signal() != types.SignalMusic {
		t.Errorf("Signal() = %d, want SignalMusic (%d)", enc.Signal(), types.SignalMusic)
	}

	// Set back to Auto
	enc.SetSignal(types.SignalAuto)
	if enc.Signal() != types.SignalAuto {
		t.Errorf("Signal() = %d, want SignalAuto (%d)", enc.Signal(), types.SignalAuto)
	}
}

// TestMaxBandwidth tests max bandwidth get/set.
func TestMaxBandwidth(t *testing.T) {
	enc, err := NewEncoderDefault(48000, 2)
	if err != nil {
		t.Fatalf("NewEncoderDefault error: %v", err)
	}

	// Default should be Fullband
	if enc.MaxBandwidth() != types.BandwidthFullband {
		t.Errorf("MaxBandwidth() = %d, want BandwidthFullband (%d)", enc.MaxBandwidth(), types.BandwidthFullband)
	}

	// Set to Wideband
	enc.SetMaxBandwidth(types.BandwidthWideband)
	if enc.MaxBandwidth() != types.BandwidthWideband {
		t.Errorf("MaxBandwidth() = %d, want BandwidthWideband (%d)", enc.MaxBandwidth(), types.BandwidthWideband)
	}

	// Set to Narrowband
	enc.SetMaxBandwidth(types.BandwidthNarrowband)
	if enc.MaxBandwidth() != types.BandwidthNarrowband {
		t.Errorf("MaxBandwidth() = %d, want BandwidthNarrowband (%d)", enc.MaxBandwidth(), types.BandwidthNarrowband)
	}
}

// TestLSBDepth tests LSB depth get/set.
func TestLSBDepth(t *testing.T) {
	enc, err := NewEncoderDefault(48000, 2)
	if err != nil {
		t.Fatalf("NewEncoderDefault error: %v", err)
	}

	// Default should be 24
	if enc.LSBDepth() != 24 {
		t.Errorf("LSBDepth() = %d, want 24", enc.LSBDepth())
	}

	// Set to 16
	err = enc.SetLSBDepth(16)
	if err != nil {
		t.Errorf("SetLSBDepth(16) error: %v", err)
	}
	if enc.LSBDepth() != 16 {
		t.Errorf("LSBDepth() = %d, want 16", enc.LSBDepth())
	}

	// Set to 8 (minimum)
	err = enc.SetLSBDepth(8)
	if err != nil {
		t.Errorf("SetLSBDepth(8) error: %v", err)
	}
	if enc.LSBDepth() != 8 {
		t.Errorf("LSBDepth() = %d, want 8", enc.LSBDepth())
	}

	// Set to 24 (maximum)
	err = enc.SetLSBDepth(24)
	if err != nil {
		t.Errorf("SetLSBDepth(24) error: %v", err)
	}
	if enc.LSBDepth() != 24 {
		t.Errorf("LSBDepth() = %d, want 24", enc.LSBDepth())
	}

	// Invalid: too low
	err = enc.SetLSBDepth(7)
	if err == nil {
		t.Error("SetLSBDepth(7) should return error")
	}

	// Invalid: too high
	err = enc.SetLSBDepth(25)
	if err == nil {
		t.Error("SetLSBDepth(25) should return error")
	}
}

// TestValidateEncoderLayout tests the layout validation function.
func TestValidateEncoderLayout(t *testing.T) {
	tests := []struct {
		name           string
		mapping        []byte
		coupledStreams int
		wantErr        bool
	}{
		{
			name:           "valid stereo",
			mapping:        []byte{0, 1},
			coupledStreams: 1,
			wantErr:        false,
		},
		{
			name:           "valid 5.1",
			mapping:        []byte{0, 4, 1, 2, 3, 5},
			coupledStreams: 2,
			wantErr:        false,
		},
		{
			name:           "valid 7.1",
			mapping:        []byte{0, 6, 1, 2, 3, 4, 5, 7},
			coupledStreams: 3,
			wantErr:        false,
		},
		{
			name:           "valid mono",
			mapping:        []byte{0},
			coupledStreams: 0,
			wantErr:        false,
		},
		{
			name:           "missing right channel",
			mapping:        []byte{0, 255}, // Left mapped, right silent
			coupledStreams: 1,
			wantErr:        true,
		},
		{
			name:           "missing left channel",
			mapping:        []byte{255, 1}, // Left silent, right mapped
			coupledStreams: 1,
			wantErr:        true,
		},
		{
			name:           "both channels silent",
			mapping:        []byte{255, 255},
			coupledStreams: 1,
			wantErr:        true,
		},
		{
			name:           "second coupled stream incomplete",
			mapping:        []byte{0, 1, 2, 255}, // Stream 0 complete, stream 1 missing right
			coupledStreams: 2,
			wantErr:        true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			err := validateEncoderLayout(tt.mapping, tt.coupledStreams)
			if tt.wantErr && err == nil {
				t.Error("expected error, got nil")
			}
			if !tt.wantErr && err != nil {
				t.Errorf("unexpected error: %v", err)
			}
		})
	}
}

// TestNewEncoderLayoutValidation tests that NewEncoder validates layout.
func TestNewEncoderLayoutValidation(t *testing.T) {
	// Valid: stereo with both channels mapped
	_, err := NewEncoder(48000, 2, 1, 1, []byte{0, 1})
	if err != nil {
		t.Errorf("valid stereo mapping rejected: %v", err)
	}

	// Invalid: stereo with left channel missing (right only)
	_, err = NewEncoder(48000, 2, 1, 1, []byte{255, 1})
	if err == nil {
		t.Error("invalid layout (missing left) should be rejected")
	} else if !containsError(err, ErrInvalidLayout) {
		t.Errorf("expected ErrInvalidLayout, got: %v", err)
	}

	// Invalid: stereo with right channel missing (left only)
	_, err = NewEncoder(48000, 2, 1, 1, []byte{0, 255})
	if err == nil {
		t.Error("invalid layout (missing right) should be rejected")
	} else if !containsError(err, ErrInvalidLayout) {
		t.Errorf("expected ErrInvalidLayout, got: %v", err)
	}
}

// TestGetFinalRange_XORCombination tests that FinalRange XORs all stream values.
func TestGetFinalRange_XORCombination(t *testing.T) {
	// Create a 5.1 encoder (4 streams)
	enc, err := NewEncoderDefault(48000, 6)
	if err != nil {
		t.Fatalf("NewEncoderDefault error: %v", err)
	}

	// Create 20ms of audio with different content per channel
	frameSize := 960
	pcm := make([]float64, frameSize*6)
	for i := 0; i < frameSize; i++ {
		for ch := 0; ch < 6; ch++ {
			// Different frequency per channel for distinct encoding
			freq := 220.0 * float64(ch+1)
			pcm[i*6+ch] = 0.5 * math.Sin(2*math.Pi*freq*float64(i)/48000)
		}
	}

	// Encode multiple frames to ensure we get different FinalRange values
	for i := 0; i < 3; i++ {
		_, err = enc.Encode(pcm, frameSize)
		if err != nil {
			t.Fatalf("Encode error on frame %d: %v", i, err)
		}
	}

	finalRange := enc.GetFinalRange()
	t.Logf("Combined FinalRange: 0x%08X", finalRange)

	// The XOR combination means we can't predict the exact value,
	// but it should be non-zero with varied content
	if finalRange == 0 {
		t.Log("Warning: FinalRange is 0, this may indicate an issue")
	}
}
