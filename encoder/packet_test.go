// Package encoder_test tests for packet building functions.
package encoder_test

import (
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/types"
)

func TestBuildPacket(t *testing.T) {
	tests := []struct {
		name      string
		frameData []byte
		mode      types.Mode
		bandwidth types.Bandwidth
		frameSize int
		stereo    bool
		wantTOC   byte
		wantLen   int
	}{
		{
			name:      "hybrid_swb_10ms_mono",
			frameData: make([]byte, 50),
			mode:      types.ModeHybrid,
			bandwidth: types.BandwidthSuperwideband,
			frameSize: 480,
			stereo:    false,
			wantTOC:   0x60, // Config 12, mono, code 0
			wantLen:   51,   // 1 TOC + 50 data
		},
		{
			name:      "hybrid_swb_20ms_stereo",
			frameData: make([]byte, 100),
			mode:      types.ModeHybrid,
			bandwidth: types.BandwidthSuperwideband,
			frameSize: 960,
			stereo:    true,
			wantTOC:   0x6C, // Config 13, stereo, code 0
			wantLen:   101,
		},
		{
			name:      "hybrid_fb_10ms",
			frameData: make([]byte, 60),
			mode:      types.ModeHybrid,
			bandwidth: types.BandwidthFullband,
			frameSize: 480,
			stereo:    false,
			wantTOC:   0x70, // Config 14, mono, code 0
			wantLen:   61,
		},
		{
			name:      "hybrid_fb_20ms",
			frameData: make([]byte, 80),
			mode:      types.ModeHybrid,
			bandwidth: types.BandwidthFullband,
			frameSize: 960,
			stereo:    false,
			wantTOC:   0x78, // Config 15, mono, code 0
			wantLen:   81,
		},
		{
			name:      "celt_fb_20ms",
			frameData: make([]byte, 120),
			mode:      types.ModeCELT,
			bandwidth: types.BandwidthFullband,
			frameSize: 960,
			stereo:    false,
			wantTOC:   0xF8, // Config 31, mono, code 0
			wantLen:   121,
		},
		{
			name:      "silk_nb_20ms",
			frameData: make([]byte, 30),
			mode:      types.ModeSILK,
			bandwidth: types.BandwidthNarrowband,
			frameSize: 960,
			stereo:    false,
			wantTOC:   0x08, // Config 1, mono, code 0
			wantLen:   31,
		},
		{
			name:      "empty_frame",
			frameData: []byte{},
			mode:      types.ModeHybrid,
			bandwidth: types.BandwidthSuperwideband,
			frameSize: 960,
			stereo:    false,
			wantTOC:   0x68,
			wantLen:   1, // Just TOC
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			packet, err := encoder.BuildPacket(tt.frameData, tt.mode, tt.bandwidth, tt.frameSize, tt.stereo)
			if err != nil {
				t.Fatalf("BuildPacket failed: %v", err)
			}

			if len(packet) != tt.wantLen {
				t.Errorf("packet length = %d, want %d", len(packet), tt.wantLen)
			}

			if packet[0] != tt.wantTOC {
				t.Errorf("TOC = 0x%02X, want 0x%02X", packet[0], tt.wantTOC)
			}

			// Verify frame data was copied correctly
			for i, b := range tt.frameData {
				if packet[1+i] != b {
					t.Errorf("frame data mismatch at %d", i)
					break
				}
			}

			// Verify TOC parses correctly (cast types to gopus types for comparison)
			toc := gopus.ParseTOC(packet[0])
			if toc.Mode != types.Mode(tt.mode) {
				t.Errorf("parsed mode = %v, want %v", toc.Mode, tt.mode)
			}
			if toc.Bandwidth != types.Bandwidth(tt.bandwidth) {
				t.Errorf("parsed bandwidth = %v, want %v", toc.Bandwidth, tt.bandwidth)
			}
			if toc.FrameSize != tt.frameSize {
				t.Errorf("parsed frameSize = %d, want %d", toc.FrameSize, tt.frameSize)
			}
			if toc.Stereo != tt.stereo {
				t.Errorf("parsed stereo = %v, want %v", toc.Stereo, tt.stereo)
			}
			if toc.FrameCode != 0 {
				t.Errorf("parsed frameCode = %d, want 0", toc.FrameCode)
			}
		})
	}
}

func TestBuildPacketInvalidConfig(t *testing.T) {
	// Test invalid mode/bandwidth/frameSize combinations
	tests := []struct {
		name      string
		mode      types.Mode
		bandwidth types.Bandwidth
		frameSize int
	}{
		{"hybrid_wb", types.ModeHybrid, types.BandwidthWideband, 960},
		{"silk_fb", types.ModeSILK, types.BandwidthFullband, 960},
		{"celt_40ms", types.ModeCELT, types.BandwidthFullband, 1920},
		{"invalid_framesize", types.ModeSILK, types.BandwidthNarrowband, 100},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := encoder.BuildPacket([]byte{}, tt.mode, tt.bandwidth, tt.frameSize, false)
			if err != encoder.ErrInvalidConfig {
				t.Errorf("expected ErrInvalidConfig, got %v", err)
			}
		})
	}
}

func TestBuildMultiFramePacket(t *testing.T) {
	tests := []struct {
		name       string
		frames     [][]byte
		mode       types.Mode
		bandwidth  types.Bandwidth
		frameSize  int
		stereo     bool
		vbr        bool
		wantTOC    byte
		wantCode3  byte
		wantMinLen int
	}{
		{
			name:       "cbr_2_frames",
			frames:     [][]byte{make([]byte, 50), make([]byte, 50)},
			mode:       types.ModeHybrid,
			bandwidth:  types.BandwidthSuperwideband,
			frameSize:  960,
			stereo:     false,
			vbr:        false,
			wantTOC:    0x6B, // Config 13, mono, code 3
			wantCode3:  0x02, // CBR, no padding, M=2
			wantMinLen: 102,  // TOC + count + 2*50
		},
		{
			name:       "vbr_2_frames",
			frames:     [][]byte{make([]byte, 30), make([]byte, 50)},
			mode:       types.ModeHybrid,
			bandwidth:  types.BandwidthSuperwideband,
			frameSize:  960,
			stereo:     false,
			vbr:        true,
			wantTOC:    0x6B, // Config 13, mono, code 3
			wantCode3:  0x82, // VBR, no padding, M=2
			wantMinLen: 83,   // TOC + count + frame1Len(1) + 30 + 50
		},
		{
			name:       "vbr_3_frames",
			frames:     [][]byte{make([]byte, 20), make([]byte, 30), make([]byte, 40)},
			mode:       types.ModeCELT,
			bandwidth:  types.BandwidthFullband,
			frameSize:  960,
			stereo:     true,
			vbr:        true,
			wantTOC:    0xFF, // Config 31, stereo, code 3
			wantCode3:  0x83, // VBR, no padding, M=3
			wantMinLen: 94,   // TOC + count + 2 frame lens + 20+30+40
		},
		{
			name:       "single_frame_code3",
			frames:     [][]byte{make([]byte, 100)},
			mode:       types.ModeHybrid,
			bandwidth:  types.BandwidthFullband,
			frameSize:  480,
			stereo:     false,
			vbr:        false,
			wantTOC:    0x73, // Config 14, mono, code 3
			wantCode3:  0x01, // CBR, no padding, M=1
			wantMinLen: 102,  // TOC + count + 100
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			packet, err := encoder.BuildMultiFramePacket(tt.frames, tt.mode, tt.bandwidth, tt.frameSize, tt.stereo, tt.vbr)
			if err != nil {
				t.Fatalf("BuildMultiFramePacket failed: %v", err)
			}

			if len(packet) < tt.wantMinLen {
				t.Errorf("packet length = %d, want >= %d", len(packet), tt.wantMinLen)
			}

			if packet[0] != tt.wantTOC {
				t.Errorf("TOC = 0x%02X, want 0x%02X", packet[0], tt.wantTOC)
			}

			if packet[1] != tt.wantCode3 {
				t.Errorf("code3 byte = 0x%02X, want 0x%02X", packet[1], tt.wantCode3)
			}

			// Verify TOC parses correctly
			toc := gopus.ParseTOC(packet[0])
			if toc.FrameCode != 3 {
				t.Errorf("parsed frameCode = %d, want 3", toc.FrameCode)
			}

			// Verify packet can be parsed back
			info, err := gopus.ParsePacket(packet)
			if err != nil {
				t.Fatalf("ParsePacket failed: %v", err)
			}

			if info.FrameCount != len(tt.frames) {
				t.Errorf("parsed frame count = %d, want %d", info.FrameCount, len(tt.frames))
			}
		})
	}
}

func TestBuildMultiFramePacketErrors(t *testing.T) {
	tests := []struct {
		name      string
		frames    [][]byte
		mode      types.Mode
		bandwidth types.Bandwidth
		frameSize int
		wantErr   error
	}{
		{
			name:      "empty_frames",
			frames:    [][]byte{},
			mode:      types.ModeHybrid,
			bandwidth: types.BandwidthSuperwideband,
			frameSize: 960,
			wantErr:   encoder.ErrInvalidFrameCount,
		},
		{
			name:      "too_many_frames",
			frames:    make([][]byte, 49), // Max is 48
			mode:      types.ModeHybrid,
			bandwidth: types.BandwidthSuperwideband,
			frameSize: 960,
			wantErr:   encoder.ErrInvalidFrameCount,
		},
		{
			name:      "invalid_config",
			frames:    [][]byte{make([]byte, 50)},
			mode:      types.ModeHybrid,
			bandwidth: types.BandwidthWideband,
			frameSize: 960,
			wantErr:   encoder.ErrInvalidConfig,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := encoder.BuildMultiFramePacket(tt.frames, tt.mode, tt.bandwidth, tt.frameSize, false, false)
			if err != tt.wantErr {
				t.Errorf("got error %v, want %v", err, tt.wantErr)
			}
		})
	}
}

func TestBuildMultiFramePacketVBRTwoByteLength(t *testing.T) {
	// Test VBR packet with frame length >= 252 (two-byte encoding)
	frames := [][]byte{
		make([]byte, 300), // > 252, needs two-byte encoding
		make([]byte, 100),
	}

	packet, err := encoder.BuildMultiFramePacket(frames, types.ModeHybrid, types.BandwidthSuperwideband, 960, false, true)
	if err != nil {
		t.Fatalf("BuildMultiFramePacket failed: %v", err)
	}

	// Verify packet can be parsed
	info, err := gopus.ParsePacket(packet)
	if err != nil {
		t.Fatalf("ParsePacket failed: %v", err)
	}

	if info.FrameCount != 2 {
		t.Errorf("frame count = %d, want 2", info.FrameCount)
	}

	if info.FrameSizes[0] != 300 {
		t.Errorf("frame 0 size = %d, want 300", info.FrameSizes[0])
	}

	if info.FrameSizes[1] != 100 {
		t.Errorf("frame 1 size = %d, want 100", info.FrameSizes[1])
	}
}

func TestWriteFrameLength(t *testing.T) {
	tests := []struct {
		length     int
		wantBytes  int
		wantFirst  byte
		wantSecond byte // Only checked if wantBytes == 2
	}{
		{0, 1, 0, 0},
		{100, 1, 100, 0},
		{251, 1, 251, 0},
		{252, 2, 252, 0},    // 252 + 4*0 = 252
		{253, 2, 253, 0},    // 253 + 4*0 = 253
		{255, 2, 255, 0},    // 255 + 4*0 = 255
		{256, 2, 252, 1},    // 252 + 4*1 = 256
		{300, 2, 252, 12},   // 252 + 4*12 = 300
		{1020, 2, 252, 192}, // 252 + 4*192 = 1020
		{1275, 2, 255, 255}, // 255 + 4*255 = 1275 (max two-byte)
	}

	for _, tt := range tests {
		buf := make([]byte, 2)
		n := encoder.WriteFrameLength(buf, tt.length)

		if n != tt.wantBytes {
			t.Errorf("writeFrameLength(%d) = %d bytes, want %d", tt.length, n, tt.wantBytes)
		}

		if buf[0] != tt.wantFirst {
			t.Errorf("writeFrameLength(%d) first byte = %d, want %d", tt.length, buf[0], tt.wantFirst)
		}

		if n == 2 && buf[1] != tt.wantSecond {
			t.Errorf("writeFrameLength(%d) second byte = %d, want %d", tt.length, buf[1], tt.wantSecond)
		}

		// Verify round-trip through ParsePacket
		if tt.length > 0 && tt.length <= 1275 {
			packet := make([]byte, 3+tt.length+50)
			packet[0] = 0x02 // Code 2
			copy(packet[1:], buf[:n])
			copy(packet[1+n:], make([]byte, tt.length+50))

			info, err := gopus.ParsePacket(packet)
			if err != nil {
				t.Errorf("ParsePacket failed for length %d: %v", tt.length, err)
				continue
			}

			if info.FrameSizes[0] != tt.length {
				t.Errorf("round-trip length %d: got %d", tt.length, info.FrameSizes[0])
			}
		}
	}
}
