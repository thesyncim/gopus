package gopus

import (
	"testing"
)

func TestParseTOC(t *testing.T) {
	tests := []struct {
		name      string
		toc       byte
		config    uint8
		mode      Mode
		bandwidth Bandwidth
		frameSize int
		stereo    bool
		frameCode uint8
	}{
		// Basic config 0 variations
		{"config0_mono_code0", 0x00, 0, ModeSILK, BandwidthNarrowband, 480, false, 0},
		{"config0_stereo_code0", 0x04, 0, ModeSILK, BandwidthNarrowband, 480, true, 0},
		{"config0_mono_code1", 0x01, 0, ModeSILK, BandwidthNarrowband, 480, false, 1},
		{"config0_mono_code2", 0x02, 0, ModeSILK, BandwidthNarrowband, 480, false, 2},
		{"config0_mono_code3", 0x03, 0, ModeSILK, BandwidthNarrowband, 480, false, 3},
		{"config0_stereo_code3", 0x07, 0, ModeSILK, BandwidthNarrowband, 480, true, 3},

		// SILK NB configs 0-3
		{"silk_nb_10ms", 0x00, 0, ModeSILK, BandwidthNarrowband, 480, false, 0},
		{"silk_nb_20ms", 0x08, 1, ModeSILK, BandwidthNarrowband, 960, false, 0},
		{"silk_nb_40ms", 0x10, 2, ModeSILK, BandwidthNarrowband, 1920, false, 0},
		{"silk_nb_60ms", 0x18, 3, ModeSILK, BandwidthNarrowband, 2880, false, 0},

		// SILK MB configs 4-7
		{"silk_mb_10ms", 0x20, 4, ModeSILK, BandwidthMediumband, 480, false, 0},
		{"silk_mb_20ms", 0x28, 5, ModeSILK, BandwidthMediumband, 960, false, 0},
		{"silk_mb_40ms", 0x30, 6, ModeSILK, BandwidthMediumband, 1920, false, 0},
		{"silk_mb_60ms", 0x38, 7, ModeSILK, BandwidthMediumband, 2880, false, 0},

		// SILK WB configs 8-11
		{"silk_wb_10ms", 0x40, 8, ModeSILK, BandwidthWideband, 480, false, 0},
		{"silk_wb_20ms", 0x48, 9, ModeSILK, BandwidthWideband, 960, false, 0},
		{"silk_wb_40ms", 0x50, 10, ModeSILK, BandwidthWideband, 1920, false, 0},
		{"silk_wb_60ms", 0x58, 11, ModeSILK, BandwidthWideband, 2880, false, 0},

		// Hybrid SWB configs 12-13
		{"hybrid_swb_10ms", 0x60, 12, ModeHybrid, BandwidthSuperwideband, 480, false, 0},
		{"hybrid_swb_20ms", 0x68, 13, ModeHybrid, BandwidthSuperwideband, 960, false, 0},

		// Hybrid FB configs 14-15
		{"hybrid_fb_10ms", 0x70, 14, ModeHybrid, BandwidthFullband, 480, false, 0},
		{"hybrid_fb_20ms", 0x78, 15, ModeHybrid, BandwidthFullband, 960, false, 0},

		// CELT NB configs 16-19
		{"celt_nb_2.5ms", 0x80, 16, ModeCELT, BandwidthNarrowband, 120, false, 0},
		{"celt_nb_5ms", 0x88, 17, ModeCELT, BandwidthNarrowband, 240, false, 0},
		{"celt_nb_10ms", 0x90, 18, ModeCELT, BandwidthNarrowband, 480, false, 0},
		{"celt_nb_20ms", 0x98, 19, ModeCELT, BandwidthNarrowband, 960, false, 0},

		// CELT WB configs 20-23
		{"celt_wb_2.5ms", 0xA0, 20, ModeCELT, BandwidthWideband, 120, false, 0},
		{"celt_wb_5ms", 0xA8, 21, ModeCELT, BandwidthWideband, 240, false, 0},
		{"celt_wb_10ms", 0xB0, 22, ModeCELT, BandwidthWideband, 480, false, 0},
		{"celt_wb_20ms", 0xB8, 23, ModeCELT, BandwidthWideband, 960, false, 0},

		// CELT SWB configs 24-27
		{"celt_swb_2.5ms", 0xC0, 24, ModeCELT, BandwidthSuperwideband, 120, false, 0},
		{"celt_swb_5ms", 0xC8, 25, ModeCELT, BandwidthSuperwideband, 240, false, 0},
		{"celt_swb_10ms", 0xD0, 26, ModeCELT, BandwidthSuperwideband, 480, false, 0},
		{"celt_swb_20ms", 0xD8, 27, ModeCELT, BandwidthSuperwideband, 960, false, 0},

		// CELT FB configs 28-31
		{"celt_fb_2.5ms", 0xE0, 28, ModeCELT, BandwidthFullband, 120, false, 0},
		{"celt_fb_5ms", 0xE8, 29, ModeCELT, BandwidthFullband, 240, false, 0},
		{"celt_fb_10ms", 0xF0, 30, ModeCELT, BandwidthFullband, 480, false, 0},
		{"celt_fb_20ms", 0xF8, 31, ModeCELT, BandwidthFullband, 960, false, 0},

		// Config 31 with all variations
		{"config31_stereo_code0", 0xFC, 31, ModeCELT, BandwidthFullband, 960, true, 0},
		{"config31_mono_code1", 0xF9, 31, ModeCELT, BandwidthFullband, 960, false, 1},
		{"config31_stereo_code2", 0xFE, 31, ModeCELT, BandwidthFullband, 960, true, 2},
		{"config31_stereo_code3", 0xFF, 31, ModeCELT, BandwidthFullband, 960, true, 3},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			toc := ParseTOC(tt.toc)

			if toc.Config != tt.config {
				t.Errorf("Config: got %d, want %d", toc.Config, tt.config)
			}
			if toc.Mode != tt.mode {
				t.Errorf("Mode: got %d, want %d", toc.Mode, tt.mode)
			}
			if toc.Bandwidth != tt.bandwidth {
				t.Errorf("Bandwidth: got %d, want %d", toc.Bandwidth, tt.bandwidth)
			}
			if toc.FrameSize != tt.frameSize {
				t.Errorf("FrameSize: got %d, want %d", toc.FrameSize, tt.frameSize)
			}
			if toc.Stereo != tt.stereo {
				t.Errorf("Stereo: got %v, want %v", toc.Stereo, tt.stereo)
			}
			if toc.FrameCode != tt.frameCode {
				t.Errorf("FrameCode: got %d, want %d", toc.FrameCode, tt.frameCode)
			}
		})
	}
}

func TestParsePacketCode0(t *testing.T) {
	tests := []struct {
		name      string
		data      []byte
		frameSize int
	}{
		{"1_byte_frame", []byte{0x00, 0xAA}, 1},
		{"10_byte_frame", make10BytePacket(), 10},
		{"100_byte_frame", make100BytePacket(), 100},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := ParsePacket(tt.data)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if info.FrameCount != 1 {
				t.Errorf("FrameCount: got %d, want 1", info.FrameCount)
			}
			if len(info.FrameSizes) != 1 {
				t.Errorf("len(FrameSizes): got %d, want 1", len(info.FrameSizes))
			}
			if info.FrameSizes[0] != tt.frameSize {
				t.Errorf("FrameSizes[0]: got %d, want %d", info.FrameSizes[0], tt.frameSize)
			}
		})
	}
}

func make10BytePacket() []byte {
	packet := make([]byte, 11) // TOC + 10 bytes
	packet[0] = 0x00           // Code 0
	return packet
}

func make100BytePacket() []byte {
	packet := make([]byte, 101) // TOC + 100 bytes
	packet[0] = 0x00            // Code 0
	return packet
}

func TestParsePacketCode1(t *testing.T) {
	tests := []struct {
		name       string
		data       []byte
		frameSize  int
		expectErr  bool
		errMessage string
	}{
		{"two_10_byte_frames", makeCode1Packet(20), 10, false, ""},
		{"two_50_byte_frames", makeCode1Packet(100), 50, false, ""},
		{"odd_length_error", []byte{0x01, 0xAA, 0xBB, 0xCC}, 0, true, "invalid packet"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := ParsePacket(tt.data)

			if tt.expectErr {
				if err == nil {
					t.Fatal("expected error, got nil")
				}
				return
			}

			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if info.FrameCount != 2 {
				t.Errorf("FrameCount: got %d, want 2", info.FrameCount)
			}
			if len(info.FrameSizes) != 2 {
				t.Errorf("len(FrameSizes): got %d, want 2", len(info.FrameSizes))
			}
			if info.FrameSizes[0] != tt.frameSize {
				t.Errorf("FrameSizes[0]: got %d, want %d", info.FrameSizes[0], tt.frameSize)
			}
			if info.FrameSizes[1] != tt.frameSize {
				t.Errorf("FrameSizes[1]: got %d, want %d", info.FrameSizes[1], tt.frameSize)
			}
		})
	}
}

func makeCode1Packet(frameDataLen int) []byte {
	packet := make([]byte, 1+frameDataLen) // TOC + frame data
	packet[0] = 0x01                       // Code 1
	return packet
}

func TestParsePacketCode2(t *testing.T) {
	tests := []struct {
		name       string
		data       []byte
		frame1Size int
		frame2Size int
	}{
		{
			"small_first_frame",
			// TOC, frame1_len=10, then 10+20 bytes of frame data
			append([]byte{0x02, 10}, make([]byte, 30)...),
			10, 20,
		},
		{
			"frame1_len_251",
			// TOC, frame1_len=251 (single byte), then 251+100 bytes
			append([]byte{0x02, 251}, make([]byte, 351)...),
			251, 100,
		},
		{
			"two_byte_encoding_252",
			// TOC, frame1_len=252 encoded as [252, 0], then frame data
			// length = 4*0 + 252 = 252
			append([]byte{0x02, 252, 0}, make([]byte, 352)...),
			252, 100,
		},
		{
			"two_byte_encoding_256",
			// TOC, frame1_len=256 encoded as [252, 1]
			// length = 4*1 + 252 = 256
			append([]byte{0x02, 252, 1}, make([]byte, 356)...),
			256, 100,
		},
		{
			"two_byte_encoding_large",
			// TOC, frame1_len=1020 encoded as [252, 192]
			// length = 4*192 + 252 = 768 + 252 = 1020
			append([]byte{0x02, 252, 192}, make([]byte, 1120)...),
			1020, 100,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := ParsePacket(tt.data)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if info.FrameCount != 2 {
				t.Errorf("FrameCount: got %d, want 2", info.FrameCount)
			}
			if info.FrameSizes[0] != tt.frame1Size {
				t.Errorf("FrameSizes[0]: got %d, want %d", info.FrameSizes[0], tt.frame1Size)
			}
			if info.FrameSizes[1] != tt.frame2Size {
				t.Errorf("FrameSizes[1]: got %d, want %d", info.FrameSizes[1], tt.frame2Size)
			}
		})
	}
}

func TestParsePacketCode3CBR(t *testing.T) {
	tests := []struct {
		name       string
		data       []byte
		frameCount int
		frameSize  int
		padding    int
	}{
		{
			"cbr_2_frames",
			// TOC=0x03, frameCount=0x02 (CBR, no padding, M=2), frameLen=50
			append([]byte{0x03, 0x02}, make([]byte, 100)...),
			2, 50, 0,
		},
		{
			"cbr_3_frames",
			// TOC=0x03, frameCount=0x03 (CBR, no padding, M=3), frameLen=30
			append([]byte{0x03, 0x03}, make([]byte, 90)...),
			3, 30, 0,
		},
		{
			"cbr_1_frame",
			// TOC=0x03, frameCount=0x01 (CBR, no padding, M=1), no frameLen byte
			append([]byte{0x03, 0x01}, make([]byte, 50)...),
			1, 50, 0,
		},
		{
			"cbr_with_padding",
			// TOC=0x03, frameCount=0x42 (CBR, padding, M=2), padding=10, frameLen=50
			append([]byte{0x03, 0x42, 10}, make([]byte, 110)...),
			2, 50, 10,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := ParsePacket(tt.data)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if info.FrameCount != tt.frameCount {
				t.Errorf("FrameCount: got %d, want %d", info.FrameCount, tt.frameCount)
			}
			if info.Padding != tt.padding {
				t.Errorf("Padding: got %d, want %d", info.Padding, tt.padding)
			}
			for i, size := range info.FrameSizes {
				if size != tt.frameSize {
					t.Errorf("FrameSizes[%d]: got %d, want %d", i, size, tt.frameSize)
				}
			}
		})
	}
}

func TestParsePacketCode3VBR(t *testing.T) {
	tests := []struct {
		name       string
		data       []byte
		frameCount int
		frameSizes []int
		padding    int
	}{
		{
			"vbr_2_frames",
			// TOC=0x03, frameCount=0x82 (VBR, no padding, M=2), frame1Len=30
			// Total data: header(3) + frame1(30) + frame2(remainder)
			append([]byte{0x03, 0x82, 30}, make([]byte, 80)...),
			2, []int{30, 50}, 0,
		},
		{
			"vbr_3_frames",
			// TOC=0x03, frameCount=0x83 (VBR, no padding, M=3), frame1Len=20, frame2Len=30
			append([]byte{0x03, 0x83, 20, 30}, make([]byte, 100)...),
			3, []int{20, 30, 50}, 0,
		},
		{
			"vbr_with_padding",
			// TOC=0x03, frameCount=0xC2 (VBR, padding, M=2), padding=5, frame1Len=30
			append([]byte{0x03, 0xC2, 5, 30}, make([]byte, 85)...),
			2, []int{30, 50}, 5,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			info, err := ParsePacket(tt.data)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if info.FrameCount != tt.frameCount {
				t.Errorf("FrameCount: got %d, want %d", info.FrameCount, tt.frameCount)
			}
			if info.Padding != tt.padding {
				t.Errorf("Padding: got %d, want %d", info.Padding, tt.padding)
			}
			if len(info.FrameSizes) != len(tt.frameSizes) {
				t.Fatalf("len(FrameSizes): got %d, want %d", len(info.FrameSizes), len(tt.frameSizes))
			}
			for i, expected := range tt.frameSizes {
				if info.FrameSizes[i] != expected {
					t.Errorf("FrameSizes[%d]: got %d, want %d", i, info.FrameSizes[i], expected)
				}
			}
		})
	}
}

func TestTwoByteFrameLength(t *testing.T) {
	tests := []struct {
		name     string
		encoded  []byte
		expected int
		bytes    int
	}{
		{"length_0", []byte{0}, 0, 1},
		{"length_100", []byte{100}, 100, 1},
		{"length_251", []byte{251}, 251, 1},
		{"length_252", []byte{252, 0}, 252, 2},     // 4*0 + 252
		{"length_255", []byte{255, 0}, 255, 2},     // 4*0 + 255
		{"length_256", []byte{252, 1}, 256, 2},     // 4*1 + 252
		{"length_259", []byte{255, 1}, 259, 2},     // 4*1 + 255
		{"length_1020", []byte{252, 192}, 1020, 2}, // 4*192 + 252
		{"length_1275", []byte{255, 255}, 1275, 2}, // 4*255 + 255 (max two-byte)
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			// Test via a code 2 packet
			packetData := append([]byte{0x02}, tt.encoded...)
			packetData = append(packetData, make([]byte, tt.expected+50)...)

			info, err := ParsePacket(packetData)
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			if info.FrameSizes[0] != tt.expected {
				t.Errorf("FrameSizes[0]: got %d, want %d", info.FrameSizes[0], tt.expected)
			}
		})
	}
}

func TestParsePacketErrors(t *testing.T) {
	tests := []struct {
		name string
		data []byte
		err  error
	}{
		{"empty_packet", []byte{}, ErrPacketTooShort},
		{"code2_truncated", []byte{0x02}, ErrPacketTooShort},
		{"code3_truncated", []byte{0x03}, ErrPacketTooShort},
		{"code3_m_zero", []byte{0x03, 0x00}, ErrInvalidFrameCount},
		{"code3_m_49", []byte{0x03, 49}, ErrInvalidFrameCount},
		{"code3_m_63", []byte{0x03, 63}, ErrInvalidFrameCount},
		{
			"code2_two_byte_truncated",
			[]byte{0x02, 252}, // Two-byte encoding but missing second byte
			ErrPacketTooShort,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := ParsePacket(tt.data)
			if err != tt.err {
				t.Errorf("error: got %v, want %v", err, tt.err)
			}
		})
	}
}

func TestParsePacketCode3MaxFrames(t *testing.T) {
	// Test M=48 (maximum allowed)
	// TOC=0x03, frameCount=0xB0 (VBR, no padding, M=48=0x30)
	// Need 47 frame lengths + last frame is remainder
	header := []byte{0x03, 0xB0} // VBR, M=48
	frameLens := make([]byte, 47)
	for i := range frameLens {
		frameLens[i] = 10 // Each frame is 10 bytes
	}
	frameData := make([]byte, 48*10) // 48 frames of 10 bytes each
	packet := append(header, frameLens...)
	packet = append(packet, frameData...)

	info, err := ParsePacket(packet)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if info.FrameCount != 48 {
		t.Errorf("FrameCount: got %d, want 48", info.FrameCount)
	}
}

func TestParsePacketCode3ContinuationPadding(t *testing.T) {
	// Test continuation bytes in padding
	// Padding = 254 + 254 + 10 = 518 (each 255 adds 254)
	header := []byte{0x03, 0x42} // CBR, padding, M=2
	padding := []byte{255, 255, 10}
	frameData := make([]byte, 100+518) // 2*50 frames + 518 padding

	packet := append(header, padding...)
	packet = append(packet, frameData...)

	info, err := ParsePacket(packet)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if info.Padding != 518 {
		t.Errorf("Padding: got %d, want 518", info.Padding)
	}
	if info.FrameCount != 2 {
		t.Errorf("FrameCount: got %d, want 2", info.FrameCount)
	}
}

func TestGenerateTOC(t *testing.T) {
	tests := []struct {
		name      string
		config    uint8
		stereo    bool
		frameCode uint8
		expected  byte
	}{
		// Basic cases
		{"config0_mono_code0", 0, false, 0, 0x00},
		{"config0_stereo_code0", 0, true, 0, 0x04},
		{"config0_mono_code1", 0, false, 1, 0x01},
		{"config0_mono_code2", 0, false, 2, 0x02},
		{"config0_mono_code3", 0, false, 3, 0x03},
		{"config0_stereo_code3", 0, true, 3, 0x07},

		// Hybrid configs (12-15)
		{"hybrid_swb_10ms", 12, false, 0, 0x60},
		{"hybrid_swb_20ms", 13, false, 0, 0x68},
		{"hybrid_fb_10ms", 14, false, 0, 0x70},
		{"hybrid_fb_20ms", 15, false, 0, 0x78},

		// CELT FB config 31
		{"celt_fb_20ms", 31, false, 0, 0xF8},
		{"celt_fb_20ms_stereo", 31, true, 0, 0xFC},
		{"celt_fb_20ms_code3", 31, true, 3, 0xFF},

		// Verify masking works for out-of-range values
		{"config_masked", 0x3F, false, 0, 0xF8},    // 0x3F & 0x1F = 0x1F = 31
		{"frameCode_masked", 0, false, 0x0F, 0x03}, // 0x0F & 0x03 = 3
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := GenerateTOC(tt.config, tt.stereo, tt.frameCode)
			if got != tt.expected {
				t.Errorf("GenerateTOC(%d, %v, %d) = 0x%02X, want 0x%02X",
					tt.config, tt.stereo, tt.frameCode, got, tt.expected)
			}
		})
	}
}

func TestGenerateTOCRoundTrip(t *testing.T) {
	// Test round-trip for all 32 configs with all stereo/frameCode combinations
	for config := uint8(0); config < 32; config++ {
		for _, stereo := range []bool{false, true} {
			for frameCode := uint8(0); frameCode < 4; frameCode++ {
				toc := GenerateTOC(config, stereo, frameCode)
				parsed := ParseTOC(toc)

				if parsed.Config != config {
					t.Errorf("config=%d stereo=%v code=%d: Config mismatch: got %d",
						config, stereo, frameCode, parsed.Config)
				}
				if parsed.Stereo != stereo {
					t.Errorf("config=%d stereo=%v code=%d: Stereo mismatch: got %v",
						config, stereo, frameCode, parsed.Stereo)
				}
				if parsed.FrameCode != frameCode {
					t.Errorf("config=%d stereo=%v code=%d: FrameCode mismatch: got %d",
						config, stereo, frameCode, parsed.FrameCode)
				}
			}
		}
	}
}

func TestConfigFromParams(t *testing.T) {
	tests := []struct {
		name      string
		mode      Mode
		bandwidth Bandwidth
		frameSize int
		expected  int
	}{
		// SILK NB
		{"silk_nb_10ms", ModeSILK, BandwidthNarrowband, 480, 0},
		{"silk_nb_20ms", ModeSILK, BandwidthNarrowband, 960, 1},
		{"silk_nb_40ms", ModeSILK, BandwidthNarrowband, 1920, 2},
		{"silk_nb_60ms", ModeSILK, BandwidthNarrowband, 2880, 3},

		// SILK MB
		{"silk_mb_10ms", ModeSILK, BandwidthMediumband, 480, 4},
		{"silk_mb_20ms", ModeSILK, BandwidthMediumband, 960, 5},

		// SILK WB
		{"silk_wb_10ms", ModeSILK, BandwidthWideband, 480, 8},
		{"silk_wb_20ms", ModeSILK, BandwidthWideband, 960, 9},

		// Hybrid SWB (configs 12-13)
		{"hybrid_swb_10ms", ModeHybrid, BandwidthSuperwideband, 480, 12},
		{"hybrid_swb_20ms", ModeHybrid, BandwidthSuperwideband, 960, 13},

		// Hybrid FB (configs 14-15)
		{"hybrid_fb_10ms", ModeHybrid, BandwidthFullband, 480, 14},
		{"hybrid_fb_20ms", ModeHybrid, BandwidthFullband, 960, 15},

		// CELT NB
		{"celt_nb_2.5ms", ModeCELT, BandwidthNarrowband, 120, 16},
		{"celt_nb_5ms", ModeCELT, BandwidthNarrowband, 240, 17},
		{"celt_nb_10ms", ModeCELT, BandwidthNarrowband, 480, 18},
		{"celt_nb_20ms", ModeCELT, BandwidthNarrowband, 960, 19},

		// CELT FB
		{"celt_fb_2.5ms", ModeCELT, BandwidthFullband, 120, 28},
		{"celt_fb_5ms", ModeCELT, BandwidthFullband, 240, 29},
		{"celt_fb_10ms", ModeCELT, BandwidthFullband, 480, 30},
		{"celt_fb_20ms", ModeCELT, BandwidthFullband, 960, 31},

		// Invalid combinations
		{"invalid_hybrid_wb", ModeHybrid, BandwidthWideband, 960, -1},
		{"invalid_silk_fb", ModeSILK, BandwidthFullband, 960, -1},
		{"invalid_celt_40ms", ModeCELT, BandwidthFullband, 1920, -1},
		{"invalid_framesize", ModeSILK, BandwidthNarrowband, 100, -1},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ConfigFromParams(tt.mode, tt.bandwidth, tt.frameSize)
			if got != tt.expected {
				t.Errorf("ConfigFromParams(%v, %v, %d) = %d, want %d",
					tt.mode, tt.bandwidth, tt.frameSize, got, tt.expected)
			}
		})
	}
}

func TestValidConfig(t *testing.T) {
	// Valid configs 0-31
	for i := uint8(0); i < 32; i++ {
		if !ValidConfig(i) {
			t.Errorf("ValidConfig(%d) = false, want true", i)
		}
	}

	// Invalid configs 32+
	for i := uint8(32); i < 255; i++ {
		if ValidConfig(i) {
			t.Errorf("ValidConfig(%d) = true, want false", i)
		}
	}
}
