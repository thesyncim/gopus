package multistream

import (
	"math"
	"testing"
)

func assertAmbisonicsMapping(t *testing.T, name string, channels int, want []byte, fn func(int) ([]byte, error)) {
	t.Helper()

	mapping, err := fn(channels)
	if err != nil {
		t.Fatalf("%s(%d) error: %v", name, channels, err)
	}
	if len(mapping) != len(want) {
		t.Fatalf("%s(%d) len = %d, want %d", name, channels, len(mapping), len(want))
	}
	for i, expect := range want {
		if mapping[i] != expect {
			t.Errorf("%s(%d)[%d] = %d, want %d", name, channels, i, mapping[i], expect)
		}
	}
}

func assertNewEncoderAmbisonics(t *testing.T, family int, channels, wantStreams, wantCoupled int) {
	t.Helper()

	enc, err := NewEncoderAmbisonics(48000, channels, family)
	if err != nil {
		t.Fatalf("NewEncoderAmbisonics(%d, %d) error: %v", channels, family, err)
	}

	if enc.Channels() != channels {
		t.Errorf("Channels() = %d, want %d", enc.Channels(), channels)
	}
	if enc.Streams() != wantStreams {
		t.Errorf("Streams() = %d, want %d", enc.Streams(), wantStreams)
	}
	if enc.CoupledStreams() != wantCoupled {
		t.Errorf("CoupledStreams() = %d, want %d", enc.CoupledStreams(), wantCoupled)
	}
	if enc.MappingFamily() != family {
		t.Errorf("MappingFamily() = %d, want %d", enc.MappingFamily(), family)
	}
}

func encodeAmbisonicsAndCheck(t *testing.T, label string, channels, family int, baseFreq, amplitude float64) {
	t.Helper()

	enc, err := NewEncoderAmbisonics(48000, channels, family)
	if err != nil {
		t.Fatalf("NewEncoderAmbisonics error: %v", err)
	}

	frameSize := 960 // 20ms at 48kHz
	pcm := make([]float64, frameSize*channels)

	for i := 0; i < frameSize; i++ {
		for ch := 0; ch < channels; ch++ {
			pcm[i*channels+ch] = amplitude * math.Sin(2*math.Pi*float64(baseFreq*float64(ch+1))*float64(i)/48000)
		}
	}

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

	t.Logf("Encoded %s packet (family %d): %d bytes, %d streams", label, family, len(packet), enc.Streams())

	streamPackets, err := parseMultistreamPacket(packet, enc.Streams())
	if err != nil {
		t.Fatalf("parseMultistreamPacket error: %v", err)
	}
	if len(streamPackets) != enc.Streams() {
		t.Errorf("parsed %d streams, want %d", len(streamPackets), enc.Streams())
	}
}

// TestIsqrt32 tests the integer square root function.
func TestIsqrt32(t *testing.T) {
	tests := []struct {
		input int
		want  int
	}{
		{0, 0},
		{1, 1},
		{2, 1},
		{3, 1},
		{4, 2},
		{5, 2},
		{8, 2},
		{9, 3},
		{10, 3},
		{15, 3},
		{16, 4},
		{17, 4},
		{24, 4},
		{25, 5},
		{35, 5},
		{36, 6},
		{48, 6},
		{49, 7},
		{63, 7},
		{64, 8},
		{80, 8},
		{81, 9},
		{99, 9},
		{100, 10},
		{120, 10},
		{121, 11},
		{143, 11},
		{144, 12},
		{168, 12},
		{169, 13},
		{195, 13},
		{196, 14},
		{224, 14},
		{225, 15},
		{227, 15},  // Max ambisonics channel count
		{256, 16},
		{1000, 31},
		{10000, 100},
		{65536, 256},
		{-1, 0},    // Negative input
		{-100, 0},  // Negative input
	}

	for _, tt := range tests {
		got := isqrt32(tt.input)
		if got != tt.want {
			t.Errorf("isqrt32(%d) = %d, want %d", tt.input, got, tt.want)
		}
	}
}

// TestIsqrt32_MatchesMath verifies isqrt32 matches math.Sqrt for a range of values.
func TestIsqrt32_MatchesMath(t *testing.T) {
	for i := 0; i <= 10000; i++ {
		got := isqrt32(i)
		want := int(math.Sqrt(float64(i)))
		if got != want {
			t.Errorf("isqrt32(%d) = %d, want %d (from math.Sqrt)", i, got, want)
		}
	}
}

// TestValidAmbisonicsChannelCounts tests that valid channel counts are accepted.
func TestValidAmbisonicsChannelCounts(t *testing.T) {
	// Valid counts: (order+1)^2 and (order+1)^2 + 2
	// Order 0: 1, 3 (but 3 is (1)^2 + 2, not valid since sqrt(3)=1, 1*1=1, 3-1=2)
	// Wait, 3 is not valid. Let's check: sqrt(3)=1, acn=1, nondiegetic=3-1=2. That's valid!
	// But order 0 + nondiegetic would be 1+2=3. Let me verify with isqrt32.
	// isqrt32(3) = 1, acn = 1*1 = 1, nondiegetic = 3-1 = 2. Valid!

	validCounts := []struct {
		channels       int
		wantStreams    int
		wantCoupled    int
		wantOrder      int
		wantNondiegetic int
	}{
		// Pure ambisonics (no non-diegetic)
		{1, 1, 0, 0, 0},    // Order 0 (W only)
		{4, 4, 0, 1, 0},    // Order 1 (FOA)
		{9, 9, 0, 2, 0},    // Order 2 (SOA)
		{16, 16, 0, 3, 0},  // Order 3 (TOA)
		{25, 25, 0, 4, 0},  // Order 4
		{36, 36, 0, 5, 0},  // Order 5
		{49, 49, 0, 6, 0},  // Order 6
		{64, 64, 0, 7, 0},  // Order 7
		{81, 81, 0, 8, 0},  // Order 8
		{100, 100, 0, 9, 0}, // Order 9
		{121, 121, 0, 10, 0}, // Order 10
		{144, 144, 0, 11, 0}, // Order 11
		{169, 169, 0, 12, 0}, // Order 12
		{196, 196, 0, 13, 0}, // Order 13
		{225, 225, 0, 14, 0}, // Order 14

		// Ambisonics with non-diegetic stereo pair
		{3, 2, 1, 0, 2},    // Order 0 + stereo non-diegetic
		{6, 5, 1, 1, 2},    // Order 1 + stereo non-diegetic
		{11, 10, 1, 2, 2},  // Order 2 + stereo non-diegetic
		{18, 17, 1, 3, 2},  // Order 3 + stereo non-diegetic
		{27, 26, 1, 4, 2},  // Order 4 + stereo non-diegetic
		{38, 37, 1, 5, 2},  // Order 5 + stereo non-diegetic
		{51, 50, 1, 6, 2},  // Order 6 + stereo non-diegetic
		{66, 65, 1, 7, 2},  // Order 7 + stereo non-diegetic
		{83, 82, 1, 8, 2},  // Order 8 + stereo non-diegetic
		{102, 101, 1, 9, 2}, // Order 9 + stereo non-diegetic
		{123, 122, 1, 10, 2}, // Order 10 + stereo non-diegetic
		{146, 145, 1, 11, 2}, // Order 11 + stereo non-diegetic
		{171, 170, 1, 12, 2}, // Order 12 + stereo non-diegetic
		{198, 197, 1, 13, 2}, // Order 13 + stereo non-diegetic
		{227, 226, 1, 14, 2}, // Order 14 + stereo non-diegetic (max)
	}

	for _, tc := range validCounts {
		t.Run("", func(t *testing.T) {
			// Test ValidateAmbisonics
			streams, coupled, err := ValidateAmbisonics(tc.channels)
			if err != nil {
				t.Errorf("ValidateAmbisonics(%d) unexpected error: %v", tc.channels, err)
				return
			}
			if streams != tc.wantStreams {
				t.Errorf("ValidateAmbisonics(%d) streams = %d, want %d", tc.channels, streams, tc.wantStreams)
			}
			if coupled != tc.wantCoupled {
				t.Errorf("ValidateAmbisonics(%d) coupled = %d, want %d", tc.channels, coupled, tc.wantCoupled)
			}

			// Test GetAmbisonicsOrder
			order, nondiegetic, err := GetAmbisonicsOrder(tc.channels)
			if err != nil {
				t.Errorf("GetAmbisonicsOrder(%d) unexpected error: %v", tc.channels, err)
				return
			}
			if order != tc.wantOrder {
				t.Errorf("GetAmbisonicsOrder(%d) order = %d, want %d", tc.channels, order, tc.wantOrder)
			}
			if nondiegetic != tc.wantNondiegetic {
				t.Errorf("GetAmbisonicsOrder(%d) nondiegetic = %d, want %d", tc.channels, nondiegetic, tc.wantNondiegetic)
			}

			// Test IsValidAmbisonicsChannelCount
			if !IsValidAmbisonicsChannelCount(tc.channels) {
				t.Errorf("IsValidAmbisonicsChannelCount(%d) = false, want true", tc.channels)
			}
		})
	}
}

// TestInvalidAmbisonicsChannelCounts tests that invalid channel counts are rejected.
func TestInvalidAmbisonicsChannelCounts(t *testing.T) {
	invalidCounts := []int{
		0,   // Zero channels
		2,   // Not a perfect square, not (perfect square + 2)
		5,   // sqrt(5)=2, acn=4, nondiegetic=1 (not 0 or 2)
		7,   // sqrt(7)=2, acn=4, nondiegetic=3 (not 0 or 2)
		8,   // sqrt(8)=2, acn=4, nondiegetic=4 (not 0 or 2)
		10,  // sqrt(10)=3, acn=9, nondiegetic=1 (not 0 or 2)
		12,  // sqrt(12)=3, acn=9, nondiegetic=3 (not 0 or 2)
		13,  // sqrt(13)=3, acn=9, nondiegetic=4 (not 0 or 2)
		14,  // sqrt(14)=3, acn=9, nondiegetic=5 (not 0 or 2)
		15,  // sqrt(15)=3, acn=9, nondiegetic=6 (not 0 or 2)
		17,  // sqrt(17)=4, acn=16, nondiegetic=1 (not 0 or 2)
		19,  // sqrt(19)=4, acn=16, nondiegetic=3 (not 0 or 2)
		228, // Exceeds maximum (227)
		256, // Exceeds maximum
		-1,  // Negative
	}

	for _, channels := range invalidCounts {
		t.Run("", func(t *testing.T) {
			// Test ValidateAmbisonics
			_, _, err := ValidateAmbisonics(channels)
			if err == nil {
				t.Errorf("ValidateAmbisonics(%d) expected error, got nil", channels)
			}

			// Test GetAmbisonicsOrder
			_, _, err = GetAmbisonicsOrder(channels)
			if err == nil {
				t.Errorf("GetAmbisonicsOrder(%d) expected error, got nil", channels)
			}

			// Test IsValidAmbisonicsChannelCount
			if IsValidAmbisonicsChannelCount(channels) {
				t.Errorf("IsValidAmbisonicsChannelCount(%d) = true, want false", channels)
			}
		})
	}
}

// TestAmbisonicsMapping tests channel mapping generation for family 2.
func TestAmbisonicsMapping(t *testing.T) {
	tests := []struct {
		channels    int
		wantMapping []byte
	}{
		// Pure ambisonics: all mono streams
		// mapping[i] = i + (coupled_streams * 2) = i + 0 = i
		{1, []byte{0}},
		{4, []byte{0, 1, 2, 3}},
		{9, []byte{0, 1, 2, 3, 4, 5, 6, 7, 8}},

		// With non-diegetic: first (streams-1) are mono, last 2 are coupled
		// For 6 channels: streams=5, coupled=1
		// mono streams: 5-1=4
		// mapping[0..3] = i + (1*2) = i + 2
		// mapping[4..5] = i - 4 (for coupled stream)
		// So: mapping = [2, 3, 4, 5, 0, 1]
		{6, []byte{2, 3, 4, 5, 0, 1}},

		// For 11 channels: streams=10, coupled=1
		// mono streams: 10-1=9
		// mapping[0..8] = i + 2
		// mapping[9..10] = i - 9
		{11, []byte{2, 3, 4, 5, 6, 7, 8, 9, 10, 0, 1}},
	}

	for _, tc := range tests {
		t.Run("", func(t *testing.T) {
			assertAmbisonicsMapping(t, "AmbisonicsMapping", tc.channels, tc.wantMapping, AmbisonicsMapping)
		})
	}
}

// TestAmbisonicsMappingFamily3 tests channel mapping generation for family 3.
func TestAmbisonicsMappingFamily3(t *testing.T) {
	tests := []struct {
		channels    int
		wantMapping []byte
	}{
		{1, []byte{0}},
		{4, []byte{0, 1, 2, 3}},
		{9, []byte{0, 1, 2, 3, 4, 5, 6, 7, 8}},
		{6, []byte{0, 1, 2, 3, 4, 5}},
		{11, []byte{0, 1, 2, 3, 4, 5, 6, 7, 8, 9, 10}},
	}

	for _, tc := range tests {
		t.Run("", func(t *testing.T) {
			assertAmbisonicsMapping(t, "AmbisonicsMappingFamily3", tc.channels, tc.wantMapping, AmbisonicsMappingFamily3)
		})
	}
}

// TestValidateAmbisonicsFamily3 tests family 3 stream calculation.
func TestValidateAmbisonicsFamily3(t *testing.T) {
	tests := []struct {
		channels    int
		wantStreams int
		wantCoupled int
	}{
		// streams = (channels + 1) / 2
		// coupled = channels / 2
		{1, 1, 0},   // 1 mono stream
		{4, 2, 2},   // 2 streams, both coupled
		{6, 3, 3},   // 3 streams, all coupled
		{9, 5, 4},   // 5 streams, 4 coupled + 1 mono
		{11, 6, 5},  // 6 streams, 5 coupled + 1 mono
		{16, 8, 8},  // 8 streams, all coupled
		{18, 9, 9},  // 9 streams, all coupled
		{25, 13, 12}, // 13 streams, 12 coupled + 1 mono
	}

	for _, tc := range tests {
		t.Run("", func(t *testing.T) {
			streams, coupled, err := ValidateAmbisonicsFamily3(tc.channels)
			if err != nil {
				t.Fatalf("ValidateAmbisonicsFamily3(%d) error: %v", tc.channels, err)
			}
			if streams != tc.wantStreams {
				t.Errorf("ValidateAmbisonicsFamily3(%d) streams = %d, want %d", tc.channels, streams, tc.wantStreams)
			}
			if coupled != tc.wantCoupled {
				t.Errorf("ValidateAmbisonicsFamily3(%d) coupled = %d, want %d", tc.channels, coupled, tc.wantCoupled)
			}
		})
	}
}

// TestAmbisonicsChannelCount tests channel count calculation from order.
func TestAmbisonicsChannelCount(t *testing.T) {
	tests := []struct {
		order           int
		withNondiegetic bool
		want            int
	}{
		{0, false, 1},
		{0, true, 3},
		{1, false, 4},
		{1, true, 6},
		{2, false, 9},
		{2, true, 11},
		{3, false, 16},
		{3, true, 18},
		{4, false, 25},
		{4, true, 27},
		{14, false, 225},
		{14, true, 227},
		{-1, false, 0},  // Invalid order
		{15, false, 0},  // Invalid order (too high)
	}

	for _, tc := range tests {
		got := AmbisonicsChannelCount(tc.order, tc.withNondiegetic)
		if got != tc.want {
			t.Errorf("AmbisonicsChannelCount(%d, %v) = %d, want %d", tc.order, tc.withNondiegetic, got, tc.want)
		}
	}
}

// TestNewEncoderAmbisonics_Family2 tests encoder creation for family 2.
func TestNewEncoderAmbisonics_Family2(t *testing.T) {
	tests := []struct {
		name        string
		channels    int
		wantStreams int
		wantCoupled int
	}{
		{"FOA", 4, 4, 0},
		{"FOA+nondiegetic", 6, 5, 1},
		{"SOA", 9, 9, 0},
		{"SOA+nondiegetic", 11, 10, 1},
		{"TOA", 16, 16, 0},
		{"TOA+nondiegetic", 18, 17, 1},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertNewEncoderAmbisonics(t, 2, tc.channels, tc.wantStreams, tc.wantCoupled)
		})
	}
}

// TestNewEncoderAmbisonics_Family3 tests encoder creation for family 3.
func TestNewEncoderAmbisonics_Family3(t *testing.T) {
	tests := []struct {
		name        string
		channels    int
		wantStreams int
		wantCoupled int
	}{
		{"FOA", 4, 2, 2},
		{"FOA+nondiegetic", 6, 3, 3},
		{"SOA", 9, 5, 4},
		{"SOA+nondiegetic", 11, 6, 5},
		{"TOA", 16, 8, 8},
		{"TOA+nondiegetic", 18, 9, 9},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			assertNewEncoderAmbisonics(t, 3, tc.channels, tc.wantStreams, tc.wantCoupled)
		})
	}
}

// TestNewEncoderAmbisonics_InvalidFamily tests that invalid mapping families are rejected.
func TestNewEncoderAmbisonics_InvalidFamily(t *testing.T) {
	invalidFamilies := []int{0, 1, 4, 255, -1}

	for _, family := range invalidFamilies {
		t.Run("", func(t *testing.T) {
			_, err := NewEncoderAmbisonics(48000, 4, family)
			if err == nil {
				t.Errorf("NewEncoderAmbisonics(4, %d) expected error, got nil", family)
			}
		})
	}
}

// TestNewEncoderAmbisonics_InvalidChannels tests that invalid channel counts are rejected.
func TestNewEncoderAmbisonics_InvalidChannels(t *testing.T) {
	// Note: 3 is valid (order 0 + 2 non-diegetic = 1 + 2 = 3)
	invalidChannels := []int{0, 2, 5, 7, 8, 10, 228}

	for _, channels := range invalidChannels {
		t.Run("family2", func(t *testing.T) {
			_, err := NewEncoderAmbisonics(48000, channels, 2)
			if err == nil {
				t.Errorf("NewEncoderAmbisonics(%d, 2) expected error, got nil", channels)
			}
		})
		t.Run("family3", func(t *testing.T) {
			_, err := NewEncoderAmbisonics(48000, channels, 3)
			if err == nil {
				t.Errorf("NewEncoderAmbisonics(%d, 3) expected error, got nil", channels)
			}
		})
	}
}

// TestEncoderAmbisonics_Encode tests that ambisonics encoding produces valid output.
func TestEncoderAmbisonics_Encode(t *testing.T) {
	t.Run("FOA Family 2", func(t *testing.T) {
		encodeAmbisonicsAndCheck(t, "FOA", 4, 2, 220, 1.0)
	})

	t.Run("FOA Family 3", func(t *testing.T) {
		encodeAmbisonicsAndCheck(t, "FOA", 4, 3, 220, 1.0)
	})

	t.Run("SOA Family 2", func(t *testing.T) {
		encodeAmbisonicsAndCheck(t, "SOA", 9, 2, 110, 0.3)
	})
}

// TestMappingFamily_DefaultEncoder tests that default encoder has correct mapping family.
func TestMappingFamily_DefaultEncoder(t *testing.T) {
	enc, err := NewEncoderDefault(48000, 6) // 5.1 surround
	if err != nil {
		t.Fatalf("NewEncoderDefault error: %v", err)
	}

	if enc.MappingFamily() != 1 {
		t.Errorf("MappingFamily() = %d, want 1 (Vorbis)", enc.MappingFamily())
	}
}
