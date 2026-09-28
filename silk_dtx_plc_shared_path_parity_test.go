package gopus

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// SILK low-bitrate DTX/loss-concealment shared-path parity.
//
// This test checks the SILK-MB/NB mono 10 ms, 8000 bps path, including active
// decode, padded empty frames, concealment, and recovery.
//
// Root cause (verified): at SILK low bitrate the encoder emits code-3 padded
// packets whose single inner frame is 1 byte (an empty/DTX frame). libopus
// opus_decode_frame routes any per-frame len<=1 to data=NULL → lost_flag=1
// (src/opus_decoder.c:315-321, 469), i.e. SILK loss concealment. gopus mirrors
// that routing (decoder_opus_frame.go's len(data)<=1 → PLC). Both therefore run
// silk_PLC_conceal for that frame. Active, concealed, and recovered PCM and
// final ranges match the selected C decoder exactly on every architecture.
func TestSILKLowBitrateDTXConcealmentSharedPathParity(t *testing.T) {
	libopustest.RequireOracle(t)
	const sampleRate = 48000
	const frames = 16

	type spec struct {
		name     string
		bw       Bandwidth
		frameMs  int
		bitrate  int
		channels int
		content  string
	}
	cases := []spec{
		{"silk_mb_ch1_10ms_8000bps_chirp", BandwidthMediumband, 10, 8000, 1, "chirp"},
		{"silk_nb_ch1_10ms_8000bps_chirp", BandwidthNarrowband, 10, 8000, 1, "chirp"},
		{"silk_mb_ch1_10ms_8000bps_ramp", BandwidthMediumband, 10, 8000, 1, "ramp"},
		{"silk_nb_ch1_10ms_8000bps_ramp", BandwidthNarrowband, 10, 8000, 1, "ramp"},
	}

	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			packets, frameSize := encodeSILKConcealmentProbePackets(t, c.bw, c.frameMs, c.bitrate, c.channels, c.content, frames)
			if len(packets) == 0 {
				t.Fatal("encoder produced no packets")
			}

			firstPLC := -1
			for i, p := range packets {
				if silkPacketInnerFrameIsConceal(p) {
					firstPLC = i
					break
				}
			}
			if firstPLC < 0 {
				t.Fatalf("%s: no DTX/concealment frame in stream", c.name)
			}

			// gopus stateful FLOAT decode + per-packet final range.
			gdec, err := NewDecoder(DefaultDecoderConfig(sampleRate, c.channels))
			if err != nil {
				t.Fatalf("new decoder: %v", err)
			}
			gPCM := make([]float32, 0, len(packets)*frameSize*c.channels)
			gRanges := make([]uint32, 0, len(packets))
			buf := make([]float32, frameSize*c.channels)
			for _, p := range packets {
				n, derr := gdec.Decode(p, buf)
				if derr != nil {
					t.Fatalf("gopus decode: %v", derr)
				}
				if n != frameSize {
					t.Fatalf("gopus samples=%d want %d", n, frameSize)
				}
				gPCM = append(gPCM, buf[:n*c.channels]...)
				gRanges = append(gRanges, gdec.FinalRange())
			}

			// libopus stateful FLOAT decode + per-packet final range.
			steps := make([]libopusAPIRateDecodeStep, len(packets))
			for i, p := range packets {
				steps[i] = libopusAPIRateDecodeStep{packet: p, frameSize: frameSize}
			}
			oPCM, oRanges, err := decodeWithLibopusReferenceAPIRateFloat32StepsRanges(sampleRate, c.channels, frameSize, steps)
			if err != nil {
				libopustest.HelperUnavailable(t, "stateful float reference", err)
				return
			}
			if len(gPCM) != len(oPCM) || len(gRanges) != len(oRanges) {
				t.Fatalf("%s: samples/ranges gopus=%d/%d libopus=%d/%d", c.name,
					len(gPCM), len(gRanges), len(oPCM), len(oRanges))
			}

			for i := range gRanges {
				if gRanges[i] != oRanges[i] {
					t.Fatalf("%s: frame %d final range=%08x want %08x", c.name, i, gRanges[i], oRanges[i])
				}
			}
			assertAPIRateFloat32BitsExact(t, gPCM, oPCM, c.name+" DTX/PLC sequence")
		})
	}
}

// encodeSILKConcealmentProbePackets encodes a deterministic SILK-only CBR stream
// at the given bandwidth/duration/bitrate. Low SILK bitrates emit code-3 padded
// packets whose inner frame is 1 byte (DTX), which the decoder routes to SILK
// loss concealment — the edge this test targets.
func encodeSILKConcealmentProbePackets(t *testing.T, bw Bandwidth, frameMs, bitrate, channels int, content string, frames int) ([][]byte, int) {
	t.Helper()
	frameSize := frameMs * 48 // 48 kHz API rate
	enc, err := NewEncoder(EncoderConfig{SampleRate: 48000, Channels: channels, Application: ApplicationVoIP})
	if err != nil {
		t.Fatalf("create encoder: %v", err)
	}
	if err := enc.SetFrameSize(frameSize); err != nil {
		t.Fatalf("set frame size: %v", err)
	}
	if err := enc.SetMode(EncoderModeSILK); err != nil {
		t.Fatalf("force SILK: %v", err)
	}
	if err := enc.SetBandwidth(bw); err != nil {
		t.Fatalf("set bandwidth: %v", err)
	}
	if err := enc.SetBitrate(bitrate); err != nil {
		t.Fatalf("set bitrate: %v", err)
	}
	if err := enc.SetBitrateMode(BitrateModeCBR); err != nil {
		t.Fatalf("set CBR: %v", err)
	}
	if channels == 2 {
		if err := enc.SetForceChannels(2); err != nil {
			t.Fatalf("force stereo: %v", err)
		}
	}

	packet := make([]byte, 4000)
	packets := make([][]byte, 0, frames)
	for f := range frames {
		pcm := make([]float32, frameSize*channels)
		for i := range frameSize {
			n := f*frameSize + i
			tt := float64(n) / 48000.0
			var s float64
			switch content {
			case "chirp":
				f0 := 100.0 + 2000.0*float64(n%4000)/4000.0
				s = 0.4 * math.Sin(2*math.Pi*f0*tt)
			case "ramp":
				s = float64((n*73)%20000-10000) / 24000.0
			default:
				s = 0.35 * math.Sin(2*math.Pi*220*tt)
			}
			for ch := range channels {
				pcm[i*channels+ch] = float32(s)
			}
		}
		nn, eerr := enc.Encode(pcm, packet)
		if eerr != nil {
			t.Fatalf("encode frame %d: %v", f, eerr)
		}
		if nn > 0 {
			packets = append(packets, append([]byte(nil), packet[:nn]...))
		}
	}
	return packets, frameSize
}

// silkPacketInnerFrameIsConceal reports whether a code-3 single-frame Opus packet
// carries a <=1-byte inner frame, which both libopus and gopus route to loss
// concealment (per-frame len<=1 → PLC). This is the encoder's CBR-padded DTX
// shape at low SILK bitrate.
func silkPacketInnerFrameIsConceal(p []byte) bool {
	if len(p) < 2 || (p[0]&0x03) != 3 {
		return false
	}
	m := int(p[1] & 0x3F)
	if m <= 0 {
		return false
	}
	hasPadding := (p[1] & 0x40) != 0
	off := 2
	padding := 0
	if hasPadding {
		for off < len(p) {
			pb := int(p[off])
			off++
			if pb == 255 {
				padding += 254
			} else {
				padding += pb
				break
			}
		}
	}
	frameDataLen := len(p) - off - padding
	if frameDataLen < 0 {
		return false
	}
	return frameDataLen/m <= 1
}
