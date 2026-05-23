package gopus

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

type libopusPacketLBRRCase struct {
	name   string
	packet []byte
	group  string
}

var libopusPacketLBRRHelper libopustest.HelperCache

func getLibopusPacketLBRRHelperPath() (string, error) {
	return libopusPacketLBRRHelper.CHelperPath(libopustest.CHelperConfig{
		Label:      "packet LBRR",
		OutputBase: "gopus_libopus_packet_lbrr",
		SourceFile: "libopus_packet_lbrr_info.c",
		CFlags:     []string{"-DHAVE_CONFIG_H", "-O2"},
		Libs:       []string{libopustest.RefPath(".libs", "libopus.a"), "-lm"},
		DeadStrip:  true,
	})
}

func probeLibopusPacketLBRR(cases []libopusPacketLBRRCase) ([]int, error) {
	binPath, err := getLibopusPacketLBRRHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayload("GPLI", uint32(len(cases)))
	for _, tc := range cases {
		payload.U32(uint32(len(tc.packet)))
		payload.Raw(tc.packet)
	}

	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "packet LBRR", "GPLO")
	if err != nil {
		return nil, err
	}
	count := reader.Count(len(cases))
	reader.ExpectRemaining(4 * count)
	out := make([]int, count)
	for i := range out {
		out[i] = int(reader.I32())
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return out, nil
}

func TestPacketHasLBRRMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	cases := packetLBRROracleCases(t)
	want, err := probeLibopusPacketLBRR(cases)
	if err != nil {
		libopustest.HelperUnavailable(t, "packet LBRR", err)
	}

	positiveByGroup := make(map[string]bool)
	for i, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			wantLBRR := want[i] > 0
			if got := PacketHasLBRR(tc.packet); got != wantLBRR {
				t.Fatalf("PacketHasLBRR=%v want %v (libopus ret=%d)", got, wantLBRR, want[i])
			}
			toc, _, err := packetFrameCount(tc.packet)
			if err != nil || len(tc.packet) == 0 {
				return
			}
			firstFrameData, err := extractFirstFramePayload(tc.packet, toc)
			if err != nil {
				return
			}
			if got := packetHasLBRR(firstFrameData, toc); got != wantLBRR {
				t.Fatalf("packetHasLBRR=%v want %v (libopus ret=%d)", got, wantLBRR, want[i])
			}
		})
		if want[i] > 0 {
			positiveByGroup[tc.group] = true
		}
	}

	for _, group := range []string{"silk_fec_mono", "silk_fec_stereo"} {
		if !positiveByGroup[group] {
			t.Fatalf("libopus did not report LBRR for %s oracle cases", group)
		}
	}
}

func packetLBRROracleCases(t *testing.T) []libopusPacketLBRRCase {
	t.Helper()
	cases := []libopusPacketLBRRCase{
		{name: "empty", packet: nil, group: "malformed"},
		{name: "toc_only_silk", packet: []byte{GenerateTOC(0, false, 0)}, group: "malformed"},
		{name: "code3_zero_frames", packet: []byte{GenerateTOC(0, false, 3), 0}, group: "malformed"},
		{name: "code2_truncated_length", packet: []byte{GenerateTOC(0, false, 2), 255}, group: "malformed"},
	}

	for _, channels := range []int{1, 2} {
		for _, frameSize := range []int{480, 960, 1920, 2880} {
			group := "silk_no_fec_mono"
			if channels == 2 {
				group = "silk_no_fec_stereo"
			}
			cases = append(cases, libopusPacketLBRRCase{
				name:   "silk_no_fec_ch" + itoaSmall(channels) + "_fs" + itoaSmall(frameSize),
				packet: encodeAPIRateSILKPacketFrameSize(t, channels, frameSize),
				group:  group,
			})
		}
	}
	for _, channels := range []int{1, 2} {
		for _, frameSize := range []int{480, 960} {
			cases = append(cases, libopusPacketLBRRCase{
				name:   "hybrid_no_fec_ch" + itoaSmall(channels) + "_fs" + itoaSmall(frameSize),
				packet: encodeAPIRateHybridPacketFrameSize(t, channels, frameSize),
				group:  "hybrid_no_fec",
			})
		}
	}
	for _, channels := range []int{1, 2} {
		for _, frameSize := range []int{480, 960, 1920, 2880} {
			cases = append(cases, libopusPacketLBRRCase{
				name:   "celt_ch" + itoaSmall(channels) + "_fs" + itoaSmall(frameSize),
				packet: encodeAPIRateCELTPacketFrameSize(t, channels, frameSize),
				group:  "celt",
			})
		}
	}

	cases = append(cases, paddedLBRROracleCases(t)...)
	cases = append(cases, fecLBRROracleCases(t, EncoderModeSILK, ModeSILK, BandwidthWideband, 24000, "silk_fec")...)
	cases = append(cases, fecLBRROracleCases(t, EncoderModeHybrid, ModeHybrid, BandwidthSuperwideband, 32000, "hybrid_fec")...)
	return cases
}

func paddedLBRROracleCases(t *testing.T) []libopusPacketLBRRCase {
	t.Helper()
	first := encodeAPIRateSILKPacketFrameSize(t, 1, 960)
	second := encodeAPIRateSILKPacketFrameSize(t, 1, 960)
	code3CBR := buildPaddedLBRROraclePacket(t, first, [][]byte{
		firstAPIRateFramePayload(t, first),
		firstAPIRateFramePayload(t, second),
	}, len(first)+len(second)+12)

	celtVBR := encodeAPIRateCELTPacketVariants(t, 1, 960, []int{32000, 48000, 64000}, 3)
	code3VBR := buildPaddedLBRROraclePacket(t, celtVBR[0], firstAPIRateFramePayloads(t, celtVBR), 0)

	return []libopusPacketLBRRCase{
		{name: "silk_code3_cbr_padding_no_fec", packet: code3CBR, group: "silk_no_fec_padded"},
		{name: "celt_code3_vbr_no_fec", packet: code3VBR, group: "celt_vbr"},
	}
}

func buildPaddedLBRROraclePacket(t *testing.T, basePacket []byte, frames [][]byte, targetLen int) []byte {
	t.Helper()
	data := make([]byte, maxPacketBytesPerStream)
	withPadding := targetLen > 0
	n, err := buildRepacketizedPacketWithOptions(basePacket[0]&0xFC, frames, data, targetLen, withPadding, nil)
	if err != nil {
		t.Fatalf("buildRepacketizedPacketWithOptions: %v", err)
	}
	return append([]byte(nil), data[:n]...)
}

func fecLBRROracleCases(t *testing.T, mode EncoderMode, wantMode Mode, bandwidth Bandwidth, bitrate int, prefix string) []libopusPacketLBRRCase {
	t.Helper()
	var cases []libopusPacketLBRRCase
	for _, channels := range []int{1, 2} {
		group := prefix + "_mono"
		if channels == 2 {
			group = prefix + "_stereo"
		}
		for _, frameSize := range []int{960, 1920, 2880} {
			packets := encodeLBRROracleFECSequence(t, mode, wantMode, bandwidth, bitrate, channels, frameSize)
			for i, packet := range packets {
				cases = append(cases, libopusPacketLBRRCase{
					name:   prefix + "_ch" + itoaSmall(channels) + "_fs" + itoaSmall(frameSize) + "_pkt" + itoaSmall(i),
					packet: packet,
					group:  group,
				})
			}
		}
	}
	return cases
}

func encodeLBRROracleFECSequence(t *testing.T, mode EncoderMode, wantMode Mode, bandwidth Bandwidth, bitrate, channels, frameSize int) [][]byte {
	t.Helper()
	const sampleRate = 48000
	app := ApplicationVoIP
	if mode == EncoderModeSILK {
		app = ApplicationRestrictedSilk
	}
	enc, err := NewEncoder(EncoderConfig{
		SampleRate:  sampleRate,
		Channels:    channels,
		Application: app,
	})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if err := enc.SetMode(mode); err != nil {
		t.Fatalf("SetMode: %v", err)
	}
	if err := enc.SetFrameSize(frameSize); err != nil {
		t.Fatalf("SetFrameSize: %v", err)
	}
	if err := enc.SetBandwidth(bandwidth); err != nil {
		t.Fatalf("SetBandwidth: %v", err)
	}
	if err := enc.SetBitrate(bitrate * channels); err != nil {
		t.Fatalf("SetBitrate: %v", err)
	}
	if err := enc.SetSignal(SignalVoice); err != nil {
		t.Fatalf("SetSignal: %v", err)
	}
	enc.SetFEC(true)
	if err := enc.SetPacketLoss(20); err != nil {
		t.Fatalf("SetPacketLoss: %v", err)
	}
	if channels == 2 {
		if err := enc.SetForceChannels(2); err != nil {
			t.Fatalf("SetForceChannels: %v", err)
		}
	}

	packets := make([][]byte, 0, 8)
	for frameIndex := 0; frameIndex < 8; frameIndex++ {
		pcm := make([]float32, frameSize*channels)
		for i := 0; i < frameSize; i++ {
			tm := float64(frameIndex*frameSize+i) / sampleRate
			pcm[i*channels] = 0.38*float32(math.Sin(2*math.Pi*220*tm)) +
				0.14*float32(math.Sin(2*math.Pi*440*tm+0.11))
			if channels == 2 {
				pcm[i*channels+1] = 0.33*float32(math.Sin(2*math.Pi*330*tm+0.07)) +
					0.12*float32(math.Sin(2*math.Pi*660*tm+0.19))
			}
		}
		packet, err := enc.EncodeFloat32(pcm)
		if err != nil {
			t.Fatalf("Encode frame %d: %v", frameIndex, err)
		}
		if len(packet) == 0 {
			t.Fatalf("Encode frame %d produced no packet", frameIndex)
		}
		toc := ParseTOC(packet[0])
		if toc.Mode != wantMode {
			t.Fatalf("Encode frame %d mode=%v want %v", frameIndex, toc.Mode, wantMode)
		}
		packets = append(packets, append([]byte(nil), packet...))
	}
	return packets
}
