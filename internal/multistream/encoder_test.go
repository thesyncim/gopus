package multistream

import (
	"math"
	"testing"
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
		{1, 1, 0, 1, nil},   // Mono
		{2, 1, 1, 2, nil},   // Stereo
		{3, 2, 1, 3, nil},   // 3.0
		{4, 2, 2, 4, nil},   // Quad
		{5, 3, 2, 5, nil},   // 5.0
		{6, 4, 2, 6, nil},   // 5.1
		{7, 5, 2, 7, nil},   // 6.1
		{8, 5, 3, 8, nil},   // 7.1
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
		length       int
		wantBytes    int
		wantFirst    byte
		wantSecond   byte
	}{
		{0, 1, 0, 0},
		{1, 1, 1, 0},
		{100, 1, 100, 0},
		{251, 1, 251, 0},
		{252, 2, 252, 0},  // 4*0 + 252 = 252
		{253, 2, 253, 0},  // 4*0 + 253 = 253
		{254, 2, 254, 0},  // 4*0 + 254 = 254
		{255, 2, 255, 0},  // 4*0 + 255 = 255
		{256, 2, 252, 1},  // 4*1 + 252 = 256
		{257, 2, 253, 1},  // 4*1 + 253 = 257
		{260, 2, 252, 2},  // 4*2 + 252 = 260
		{500, 2, 252, 62}, // 4*62 + 252 = 500
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
			{1, 2},       // 2 bytes
			{3, 4, 5},    // 3 bytes
			{6},          // 1 byte
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
