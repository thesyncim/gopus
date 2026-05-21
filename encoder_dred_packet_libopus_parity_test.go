//go:build gopus_dred || gopus_extra_controls

package gopus

import (
	"bytes"
	"fmt"
	"math"
	"sync"
	"testing"

	encpkg "github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/internal/libopustest"
)

var (
	libopusDREDEncoderModelBlobHelperOnce sync.Once
	libopusDREDEncoderModelBlobHelperPath string
	libopusDREDEncoderModelBlobHelperErr  error
)

func getLibopusDREDEncoderModelBlobHelperPath() (string, error) {
	libopusDREDEncoderModelBlobHelperOnce.Do(func() {
		libopusDREDEncoderModelBlobHelperPath, libopusDREDEncoderModelBlobHelperErr = buildLibopusDREDHelper("libopus_dred_encoder_model_blob.c", "gopus_libopus_dred_encoder_model_blob", true)
	})
	if libopusDREDEncoderModelBlobHelperErr != nil {
		return "", libopusDREDEncoderModelBlobHelperErr
	}
	return libopusDREDEncoderModelBlobHelperPath, nil
}

func probeLibopusDREDEncoderModelBlob() ([]byte, error) {
	binPath, err := getLibopusDREDEncoderModelBlobHelperPath()
	if err != nil {
		return nil, err
	}
	return runModelBlobHelper(binPath)
}

func probeLibopusPitchDNNModelBlob() ([]byte, error) {
	binPath, err := getLibopusPitchDNNModelBlobHelperPath()
	if err != nil {
		return nil, err
	}
	return runModelBlobHelper(binPath)
}

func probeLibopusEncoderNeuralModelBlob() ([]byte, error) {
	pitchBlob, err := probeLibopusPitchDNNModelBlob()
	if err != nil {
		return nil, err
	}
	dredBlob, err := probeLibopusDREDEncoderModelBlob()
	if err != nil {
		return nil, err
	}
	blob := make([]byte, 0, len(pitchBlob)+len(dredBlob))
	blob = append(blob, pitchBlob...)
	blob = append(blob, dredBlob...)
	return blob, nil
}

func requireLibopusEncoderNeuralModelBlob(t *testing.T) []byte {
	t.Helper()
	blob, err := probeLibopusEncoderNeuralModelBlob()
	if err != nil {
		libopustest.HelperUnavailable(t, "encoder neural model", err)
	}
	return blob
}

func encoderDREDBitrateForFrameSize(frameSize int) int {
	bitrate := 40000
	if frameSize > 0 && frameSize < 960 {
		bitrate = (40000 * 960) / frameSize
	}
	if bitrate > 320000 {
		bitrate = 320000
	}
	return bitrate
}

func encoderDREDVoicedSample(frameIdx, sampleIdx, frameSize, sampleRate int) float32 {
	n := frameIdx*frameSize + sampleIdx
	t := float64(n) / float64(sampleRate)
	env := 0.82 + 0.18*math.Sin(2*math.Pi*1.3*t)
	s := 0.0
	s += 0.28 * math.Sin(2*math.Pi*110*t)
	s += 0.17 * math.Sin(2*math.Pi*220*t+0.11)
	s += 0.09 * math.Sin(2*math.Pi*330*t+0.23)
	s += 0.05 * math.Sin(2*math.Pi*440*t+0.37)
	return float32(env * s)
}

func encoderDREDFrame(frameIdx, frameSize, sampleRate, channels int) []float32 {
	pcm := make([]float32, frameSize*channels)
	for i := 0; i < frameSize; i++ {
		s := encoderDREDVoicedSample(frameIdx, i, frameSize, sampleRate)
		for ch := 0; ch < channels; ch++ {
			pcm[i*channels+ch] = s
		}
	}
	return pcm
}

func encoderModeToPublic(mode encpkg.Mode) (Mode, error) {
	switch mode {
	case encpkg.ModeSILK:
		return ModeSILK, nil
	case encpkg.ModeHybrid:
		return ModeHybrid, nil
	case encpkg.ModeCELT:
		return ModeCELT, nil
	default:
		return 0, fmt.Errorf("unsupported encoder mode %v", mode)
	}
}

func encodeUntilDREDPacket(t *testing.T, mode encpkg.Mode, bandwidth Bandwidth, frameSize, channels int) ([]byte, []byte, int) {
	packet, payload, offset, _ := encodeUntilDREDPacketWithFrameIndex(t, mode, bandwidth, frameSize, channels)
	return packet, payload, offset
}

func encodeUntilDREDPacketWithFrameIndex(t *testing.T, mode encpkg.Mode, bandwidth Bandwidth, frameSize, channels int) ([]byte, []byte, int, int) {
	return encodeUntilDREDPacketWithSettings(t, encoderDREDPacketSettings{
		mode:      mode,
		bandwidth: bandwidth,
		frameSize: frameSize,
		channels:  channels,
		bitrate:   encoderDREDBitrateForFrameSize(frameSize),
	})
}

type encoderDREDPacketSettings struct {
	mode      encpkg.Mode
	bandwidth Bandwidth
	frameSize int
	channels  int
	bitrate   int
	cbr       bool
}

func encodeUntilDREDPacketWithSettings(t *testing.T, settings encoderDREDPacketSettings) ([]byte, []byte, int, int) {
	t.Helper()
	if settings.frameSize <= 0 {
		settings.frameSize = 960
	}
	if settings.channels <= 0 {
		settings.channels = 1
	}
	if settings.bitrate <= 0 {
		settings.bitrate = encoderDREDBitrateForFrameSize(settings.frameSize)
	}

	cfg := EncoderConfig{
		SampleRate:  48000,
		Channels:    settings.channels,
		Application: ApplicationAudio,
	}
	enc, err := NewEncoder(cfg)
	if err != nil {
		t.Fatalf("NewEncoder error: %v", err)
	}
	if err := enc.SetFrameSize(settings.frameSize); err != nil {
		t.Fatalf("SetFrameSize error: %v", err)
	}
	if err := enc.SetBandwidth(settings.bandwidth); err != nil {
		t.Fatalf("SetBandwidth error: %v", err)
	}
	if err := enc.SetBitrate(settings.bitrate); err != nil {
		t.Fatalf("SetBitrate error: %v", err)
	}
	if settings.cbr {
		if err := enc.SetBitrateMode(BitrateModeCBR); err != nil {
			t.Fatalf("SetBitrateMode(CBR) error: %v", err)
		}
	}
	if err := enc.SetSignal(SignalMusic); err != nil {
		t.Fatalf("SetSignal error: %v", err)
	}
	if err := enc.SetPacketLoss(20); err != nil {
		t.Fatalf("SetPacketLoss error: %v", err)
	}
	if err := enc.SetDNNBlob(requireLibopusEncoderNeuralModelBlob(t)); err != nil {
		t.Fatalf("SetDNNBlob error: %v", err)
	}
	if err := enc.SetDREDDuration(80); err != nil {
		t.Fatalf("SetDREDDuration error: %v", err)
	}
	enc.enc.SetMode(settings.mode)

	wantMode, err := encoderModeToPublic(settings.mode)
	if err != nil {
		t.Fatal(err)
	}

	packet := make([]byte, maxPacketBytesPerStream)
	for frameIdx := 0; frameIdx < 640; frameIdx++ {
		pcm := encoderDREDFrame(frameIdx, settings.frameSize, cfg.SampleRate, cfg.Channels)
		n, err := enc.Encode(pcm, packet)
		if err != nil {
			t.Fatalf("Encode(frame=%d) error: %v", frameIdx, err)
		}
		gotPacket := append([]byte(nil), packet[:n]...)
		toc := ParseTOC(gotPacket[0])
		packetDuration, err := opusPacketDurationSamples(gotPacket)
		if err != nil {
			t.Fatalf("parse packet duration frame=%d: %v", frameIdx, err)
		}
		if toc.Mode != wantMode || toc.Bandwidth != settings.bandwidth || packetDuration != settings.frameSize {
			continue
		}
		payload, frameOffset, ok, err := findDREDPayload(gotPacket)
		if err != nil {
			t.Fatalf("findDREDPayload(frame=%d) error: %v", frameIdx, err)
		}
		if ok {
			return gotPacket, append([]byte(nil), payload...), frameOffset, frameIdx
		}
	}
	t.Fatalf("no DRED packet emitted for mode=%v bandwidth=%v frameSize=%d", settings.mode, settings.bandwidth, settings.frameSize)
	return nil, nil, 0, 0
}

func TestEncoderDREDEmissionFrameIndexMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range []struct {
		name      string
		mode      encpkg.Mode
		public    Mode
		bandwidth Bandwidth
		channels  int
	}{
		{name: "silk", mode: encpkg.ModeSILK, public: ModeSILK, bandwidth: BandwidthWideband, channels: 1},
		{name: "hybrid", mode: encpkg.ModeHybrid, public: ModeHybrid, bandwidth: BandwidthFullband, channels: 1},
		{name: "celt", mode: encpkg.ModeCELT, public: ModeCELT, bandwidth: BandwidthFullband, channels: 1},
		{name: "stereo_celt", mode: encpkg.ModeCELT, public: ModeCELT, bandwidth: BandwidthFullband, channels: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: 960,
				ForceMode: tc.public,
				Bandwidth: tc.bandwidth,
				Channels:  tc.channels,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "DRED packet", err)
			}
			_, _, _, gotFrameIndex := encodeUntilDREDPacketWithFrameIndex(t, tc.mode, tc.bandwidth, 960, tc.channels)
			if gotFrameIndex != packetInfo.frameIndex {
				t.Fatalf("DRED frame index=%d want %d", gotFrameIndex, packetInfo.frameIndex)
			}
		})
	}
}

func assertSilkMonoPrimaryFrameByteExact(t *testing.T, frameSize int) {
	t.Helper()
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: frameSize, ForceMode: ModeSILK, Bandwidth: BandwidthWideband,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "DRED packet", err)
	}
	gotPacket, _, _ := encodeUntilDREDPacket(t, encpkg.ModeSILK, BandwidthWideband, frameSize, 1)
	_, gotFrames, _, _, err := parsePacketFramesAndPadding(gotPacket)
	if err != nil {
		t.Fatalf("parsePacketFramesAndPadding(got): %v", err)
	}
	_, wantFrames, _, _, err := parsePacketFramesAndPadding(packetInfo.packet)
	if err != nil {
		t.Fatalf("parsePacketFramesAndPadding(want): %v", err)
	}
	if len(gotFrames) != len(wantFrames) {
		t.Fatalf("frame count=%d want %d", len(gotFrames), len(wantFrames))
	}
	for i := range wantFrames {
		if !bytes.Equal(gotFrames[i], wantFrames[i]) {
			t.Fatalf("frame %d DIVERGES\n got=%x\nwant=%x", i, gotFrames[i], wantFrames[i])
		}
	}
}

func TestEncoderSilkMono20msPrimaryFrameByteExactMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	assertSilkMonoPrimaryFrameByteExact(t, 960)
}

func TestEncoderSilkMono40msPrimaryFrameByteExactMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	assertSilkMonoPrimaryFrameByteExact(t, 1920)
}

func TestEncoderSilkMono60msPrimaryFrameByteExactMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	assertSilkMonoPrimaryFrameByteExact(t, 2880)
}

func TestEncoderCarriedDREDPayloadMatchesLibopusSilkWideband20ms(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 960,
		ForceMode: ModeSILK,
		Bandwidth: BandwidthWideband,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "DRED packet", err)
	}
	wantPayload, wantOffset, ok, err := findDREDPayload(packetInfo.packet)
	if err != nil {
		t.Fatalf("findDREDPayload(libopus) error: %v", err)
	}
	if !ok {
		t.Fatal("libopus silk packet missing DRED payload")
	}

	gotPacket, gotPayload, gotOffset := encodeUntilDREDPacket(t, encpkg.ModeSILK, BandwidthWideband, 960, 1)
	if ParseTOC(gotPacket[0]).Mode != ModeSILK {
		t.Fatalf("got packet mode=%v want %v", ParseTOC(gotPacket[0]).Mode, ModeSILK)
	}
	if len(gotPacket) != len(packetInfo.packet) {
		t.Fatalf("packet length=%d want %d", len(gotPacket), len(packetInfo.packet))
	}
	if gotOffset != wantOffset {
		t.Fatalf("frameOffset=%d want %d", gotOffset, wantOffset)
	}
	if !bytes.Equal(gotPayload, wantPayload) {
		t.Fatalf("DRED payload mismatch\n got=%x\nwant=%x", gotPayload, wantPayload)
	}
	assertDREDPacketPrimaryFrameSizesMatchLibopus(t, gotPacket, packetInfo.packet)
}

func TestEncoderCarriedDREDPayloadMatchesLibopusSilkWideband20msCBR(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 960,
		ForceMode: ModeSILK,
		Bandwidth: BandwidthWideband,
		Bitrate:   24000,
		CBR:       true,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "CBR DRED packet", err)
	}
	wantPayload, wantOffset, ok, err := findDREDPayload(packetInfo.packet)
	if err != nil {
		t.Fatalf("findDREDPayload(libopus) error: %v", err)
	}
	if !ok {
		t.Fatal("libopus CBR silk packet missing DRED payload")
	}

	gotPacket, gotPayload, gotOffset, gotFrameIndex := encodeUntilDREDPacketWithSettings(t, encoderDREDPacketSettings{
		mode:      encpkg.ModeSILK,
		bandwidth: BandwidthWideband,
		frameSize: 960,
		channels:  1,
		bitrate:   24000,
		cbr:       true,
	})
	if ParseTOC(gotPacket[0]).Mode != ModeSILK {
		t.Fatalf("got packet mode=%v want %v", ParseTOC(gotPacket[0]).Mode, ModeSILK)
	}
	if gotFrameIndex != packetInfo.frameIndex {
		t.Fatalf("DRED frame index=%d want %d", gotFrameIndex, packetInfo.frameIndex)
	}
	if len(gotPacket) != len(packetInfo.packet) {
		t.Fatalf("packet length=%d want %d", len(gotPacket), len(packetInfo.packet))
	}
	if gotOffset != wantOffset {
		t.Fatalf("frameOffset=%d want %d", gotOffset, wantOffset)
	}
	if !bytes.Equal(gotPayload, wantPayload) {
		t.Fatalf("DRED payload mismatch\n got=%x\nwant=%x", gotPayload, wantPayload)
	}
}

func TestEncoderCarriedDREDPayloadMatchesLibopusSilkWideband40ms(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 1920,
		ForceMode: ModeSILK,
		Bandwidth: BandwidthWideband,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "40 ms DRED packet", err)
	}
	wantPayload, wantOffset, ok, err := findDREDPayload(packetInfo.packet)
	if err != nil {
		t.Fatalf("findDREDPayload(libopus) error: %v", err)
	}
	if !ok {
		t.Fatal("libopus 40 ms silk packet missing DRED payload")
	}

	gotPacket, gotPayload, gotOffset := encodeUntilDREDPacket(t, encpkg.ModeSILK, BandwidthWideband, 1920, 1)
	if ParseTOC(gotPacket[0]).Mode != ModeSILK {
		t.Fatalf("got packet mode=%v want %v", ParseTOC(gotPacket[0]).Mode, ModeSILK)
	}
	if len(gotPacket) != len(packetInfo.packet) {
		t.Fatalf("packet length=%d want %d", len(gotPacket), len(packetInfo.packet))
	}
	if gotOffset != wantOffset {
		t.Fatalf("frameOffset=%d want %d", gotOffset, wantOffset)
	}
	if !bytes.Equal(gotPayload, wantPayload) {
		t.Fatalf("DRED payload mismatch\n got=%x\nwant=%x", gotPayload, wantPayload)
	}
	assertDREDPacketPrimaryFrameSizesMatchLibopus(t, gotPacket, packetInfo.packet)
}

func TestEncoderCarriedDREDPayloadMatchesLibopusSilkWideband60ms(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 2880,
		ForceMode: ModeSILK,
		Bandwidth: BandwidthWideband,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "60 ms DRED packet", err)
	}
	wantPayload, wantOffset, ok, err := findDREDPayload(packetInfo.packet)
	if err != nil {
		t.Fatalf("findDREDPayload(libopus) error: %v", err)
	}
	if !ok {
		t.Fatal("libopus 60 ms silk packet missing DRED payload")
	}

	gotPacket, gotPayload, gotOffset := encodeUntilDREDPacket(t, encpkg.ModeSILK, BandwidthWideband, 2880, 1)
	if ParseTOC(gotPacket[0]).Mode != ModeSILK {
		t.Fatalf("got packet mode=%v want %v", ParseTOC(gotPacket[0]).Mode, ModeSILK)
	}
	if len(gotPacket) != len(packetInfo.packet) {
		t.Fatalf("packet length=%d want %d", len(gotPacket), len(packetInfo.packet))
	}
	if gotOffset != wantOffset {
		t.Fatalf("frameOffset=%d want %d", gotOffset, wantOffset)
	}
	if !bytes.Equal(gotPayload, wantPayload) {
		t.Fatalf("DRED payload mismatch\n got=%x\nwant=%x", gotPayload, wantPayload)
	}
	assertDREDPacketPrimaryFrameSizesMatchLibopus(t, gotPacket, packetInfo.packet)
}

func TestEncoderCarriedDREDPayloadMatchesLibopusSilkWidebandLongFrames(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, frameSize := range []int{3840, 4800, 5760} {
		t.Run(fmt.Sprintf("%d_samples", frameSize), func(t *testing.T) {
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: frameSize,
				ForceMode: ModeSILK,
				Bandwidth: BandwidthWideband,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "long SILK DRED packet", err)
			}
			wantPayload, wantOffset, ok, err := findDREDPayload(packetInfo.packet)
			if err != nil {
				t.Fatalf("findDREDPayload(libopus) error: %v", err)
			}
			if !ok {
				t.Fatal("libopus long SILK packet missing DRED payload")
			}

			gotPacket, gotPayload, gotOffset, gotFrameIndex := encodeUntilDREDPacketWithFrameIndex(t, encpkg.ModeSILK, BandwidthWideband, frameSize, 1)
			if ParseTOC(gotPacket[0]).Mode != ModeSILK {
				t.Fatalf("got packet mode=%v want %v", ParseTOC(gotPacket[0]).Mode, ModeSILK)
			}
			if gotFrameIndex != packetInfo.frameIndex {
				t.Fatalf("DRED frame index=%d want %d", gotFrameIndex, packetInfo.frameIndex)
			}
			if len(gotPacket) != len(packetInfo.packet) {
				t.Fatalf("packet length=%d want %d", len(gotPacket), len(packetInfo.packet))
			}
			if gotOffset != wantOffset {
				t.Fatalf("frameOffset=%d want %d", gotOffset, wantOffset)
			}
			if !bytes.Equal(gotPayload, wantPayload) {
				t.Fatalf("DRED payload mismatch\n got=%x\nwant=%x", gotPayload, wantPayload)
			}
			assertDREDPacketPrimaryFrameSizesMatchLibopus(t, gotPacket, packetInfo.packet)
		})
	}
}

func TestEncoderCarriedDREDPayloadMatchesLibopusHybridFullband20msPayloadOnly(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 960,
		ForceMode: ModeHybrid,
		Bandwidth: BandwidthFullband,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "hybrid DRED packet", err)
	}
	wantPayload, wantOffset, ok, err := findDREDPayload(packetInfo.packet)
	if err != nil {
		t.Fatalf("findDREDPayload(libopus) error: %v", err)
	}
	if !ok {
		t.Fatal("libopus hybrid packet missing DRED payload")
	}

	gotPacket, gotPayload, gotOffset := encodeUntilDREDPacket(t, encpkg.ModeHybrid, BandwidthFullband, 960, 1)
	if ParseTOC(gotPacket[0]).Mode != ModeHybrid {
		t.Fatalf("got packet mode=%v want %v", ParseTOC(gotPacket[0]).Mode, ModeHybrid)
	}
	if gotOffset != wantOffset {
		t.Fatalf("frameOffset=%d want %d", gotOffset, wantOffset)
	}
	if !bytes.Equal(gotPayload, wantPayload) {
		t.Fatalf("DRED payload mismatch\n got=%x\nwant=%x", gotPayload, wantPayload)
	}
}

func TestEncoderCarriedDREDPayloadMatchesLibopusHybridFullband40msPayloadOnly(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 1920,
		ForceMode: ModeHybrid,
		Bandwidth: BandwidthFullband,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "40 ms hybrid DRED packet", err)
	}
	wantPayload, wantOffset, ok, err := findDREDPayload(packetInfo.packet)
	if err != nil {
		t.Fatalf("findDREDPayload(libopus) error: %v", err)
	}
	if !ok {
		t.Fatal("libopus 40 ms hybrid packet missing DRED payload")
	}

	gotPacket, gotPayload, gotOffset := encodeUntilDREDPacket(t, encpkg.ModeHybrid, BandwidthFullband, 1920, 1)
	if ParseTOC(gotPacket[0]).Mode != ModeHybrid {
		t.Fatalf("got packet mode=%v want %v", ParseTOC(gotPacket[0]).Mode, ModeHybrid)
	}
	if gotOffset != wantOffset {
		t.Fatalf("frameOffset=%d want %d", gotOffset, wantOffset)
	}
	if !bytes.Equal(gotPayload, wantPayload) {
		t.Fatalf("DRED payload mismatch\n got=%x\nwant=%x", gotPayload, wantPayload)
	}
}

func TestEncoderCarriedDREDPayloadMatchesLibopusHybridFullband40msStereoPayloadOnly(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 1920,
		ForceMode: ModeHybrid,
		Bandwidth: BandwidthFullband,
		Channels:  2,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "40 ms stereo hybrid DRED packet", err)
	}
	wantPayload, wantOffset, ok, err := findDREDPayload(packetInfo.packet)
	if err != nil {
		t.Fatalf("findDREDPayload(libopus) error: %v", err)
	}
	if !ok {
		t.Fatal("libopus 40 ms stereo hybrid packet missing DRED payload")
	}

	gotPacket, gotPayload, gotOffset := encodeUntilDREDPacket(t, encpkg.ModeHybrid, BandwidthFullband, 1920, 2)
	toc := ParseTOC(gotPacket[0])
	if toc.Mode != ModeHybrid || !toc.Stereo {
		t.Fatalf("got packet toc=%+v want hybrid stereo", toc)
	}
	if gotOffset != wantOffset {
		t.Fatalf("frameOffset=%d want %d", gotOffset, wantOffset)
	}
	if !bytes.Equal(gotPayload, wantPayload) {
		t.Fatalf("DRED payload mismatch\n got=%x\nwant=%x", gotPayload, wantPayload)
	}
}

func TestEncoderCarriedDREDPayloadMatchesLibopusCELTFullband20ms(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 960,
		ForceMode: ModeCELT,
		Bandwidth: BandwidthFullband,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "CELT DRED packet", err)
	}
	wantPayload, wantOffset, ok, err := findDREDPayload(packetInfo.packet)
	if err != nil {
		t.Fatalf("findDREDPayload(libopus) error: %v", err)
	}
	if !ok {
		t.Fatal("libopus CELT packet missing DRED payload")
	}

	gotPacket, gotPayload, gotOffset := encodeUntilDREDPacket(t, encpkg.ModeCELT, BandwidthFullband, 960, 1)
	if ParseTOC(gotPacket[0]).Mode != ModeCELT {
		t.Fatalf("got packet mode=%v want %v", ParseTOC(gotPacket[0]).Mode, ModeCELT)
	}
	if gotOffset != wantOffset {
		t.Fatalf("frameOffset=%d want %d", gotOffset, wantOffset)
	}
	if !bytes.Equal(gotPayload, wantPayload) {
		t.Fatalf("DRED payload mismatch\n got=%x\nwant=%x", gotPayload, wantPayload)
	}
}

func TestEncoderCarriedDREDPayloadMatchesLibopusCELTFullbandLongFrames(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, tc := range []struct {
		frameSize int
		channels  int
	}{
		{frameSize: 1920, channels: 1},
		{frameSize: 2880, channels: 1},
		{frameSize: 1920, channels: 2},
	} {
		tc := tc
		t.Run(fmt.Sprintf("%d_samples_%dch", tc.frameSize, tc.channels), func(t *testing.T) {
			packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
				FrameSize: tc.frameSize,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthFullband,
				Channels:  tc.channels,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "CELT DRED packet", err)
			}
			wantPayload, wantOffset, ok, err := findDREDPayload(packetInfo.packet)
			if err != nil {
				t.Fatalf("findDREDPayload(libopus) error: %v", err)
			}
			if !ok {
				t.Fatal("libopus CELT packet missing DRED payload")
			}

			gotPacket, gotPayload, gotOffset, gotFrameIndex := encodeUntilDREDPacketWithFrameIndex(t, encpkg.ModeCELT, BandwidthFullband, tc.frameSize, tc.channels)
			toc := ParseTOC(gotPacket[0])
			if toc.Mode != ModeCELT || toc.Stereo != (tc.channels == 2) {
				t.Fatalf("got packet toc=%+v want celt channels=%d", toc, tc.channels)
			}
			if gotFrameIndex != packetInfo.frameIndex {
				t.Fatalf("DRED frame index=%d want %d", gotFrameIndex, packetInfo.frameIndex)
			}
			if gotOffset != wantOffset {
				t.Fatalf("frameOffset=%d want %d", gotOffset, wantOffset)
			}
			if !bytes.Equal(gotPayload, wantPayload) {
				t.Fatalf("DRED payload mismatch\n got=%x\nwant=%x", gotPayload, wantPayload)
			}
		})
	}
}

func TestEncoderSilkStereo20msPrimaryFrameByteExactMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 960, ForceMode: ModeSILK, Bandwidth: BandwidthWideband, Channels: 2,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "DRED packet", err)
	}
	gotPacket, _, _ := encodeUntilDREDPacket(t, encpkg.ModeSILK, BandwidthWideband, 960, 2)
	_, gotFrames, _, _, err := parsePacketFramesAndPadding(gotPacket)
	if err != nil {
		t.Fatalf("parsePacketFramesAndPadding(got): %v", err)
	}
	_, wantFrames, _, _, err := parsePacketFramesAndPadding(packetInfo.packet)
	if err != nil {
		t.Fatalf("parsePacketFramesAndPadding(want): %v", err)
	}
	if len(gotFrames) != len(wantFrames) {
		t.Fatalf("frame count=%d want %d", len(gotFrames), len(wantFrames))
	}
	for i := range wantFrames {
		if !bytes.Equal(gotFrames[i], wantFrames[i]) {
			t.Fatalf("frame %d DIVERGES\n got=%x\nwant=%x", i, gotFrames[i], wantFrames[i])
		}
	}
}

func TestEncoderCarriedDREDPayloadMatchesLibopusSilkWideband20msStereo(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 960,
		ForceMode: ModeSILK,
		Bandwidth: BandwidthWideband,
		Channels:  2,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "stereo silk DRED packet", err)
	}
	wantPayload, wantOffset, ok, err := findDREDPayload(packetInfo.packet)
	if err != nil {
		t.Fatalf("findDREDPayload(libopus) error: %v", err)
	}
	if !ok {
		t.Fatal("libopus stereo silk packet missing DRED payload")
	}

	gotPacket, gotPayload, gotOffset := encodeUntilDREDPacket(t, encpkg.ModeSILK, BandwidthWideband, 960, 2)
	toc := ParseTOC(gotPacket[0])
	if toc.Mode != ModeSILK || !toc.Stereo {
		t.Fatalf("got packet toc=%+v want silk stereo", toc)
	}
	if len(gotPacket) != len(packetInfo.packet) {
		t.Fatalf("packet length=%d want %d", len(gotPacket), len(packetInfo.packet))
	}
	if gotOffset != wantOffset {
		t.Fatalf("frameOffset=%d want %d", gotOffset, wantOffset)
	}
	if !bytes.Equal(gotPayload, wantPayload) {
		t.Fatalf("DRED payload mismatch\n got=%x\nwant=%x", gotPayload, wantPayload)
	}
	assertDREDPacketPrimaryFrameSizesMatchLibopus(t, gotPacket, packetInfo.packet)
}

func TestEncoderCarriedDREDPayloadMatchesLibopusSilkWideband40msStereo(t *testing.T) {
	libopustest.RequireOracle(t)
	packetInfo, err := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize: 1920,
		ForceMode: ModeSILK,
		Bandwidth: BandwidthWideband,
		Channels:  2,
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "stereo 40 ms silk DRED packet", err)
	}
	wantPayload, wantOffset, ok, err := findDREDPayload(packetInfo.packet)
	if err != nil {
		t.Fatalf("findDREDPayload(libopus) error: %v", err)
	}
	if !ok {
		t.Fatal("libopus stereo 40 ms silk packet missing DRED payload")
	}

	gotPacket, gotPayload, gotOffset := encodeUntilDREDPacket(t, encpkg.ModeSILK, BandwidthWideband, 1920, 2)
	toc := ParseTOC(gotPacket[0])
	if toc.Mode != ModeSILK || !toc.Stereo {
		t.Fatalf("got packet toc=%+v want silk stereo", toc)
	}
	if len(gotPacket) != len(packetInfo.packet) {
		t.Fatalf("packet length=%d want %d", len(gotPacket), len(packetInfo.packet))
	}
	if gotOffset != wantOffset {
		t.Fatalf("frameOffset=%d want %d", gotOffset, wantOffset)
	}
	if !bytes.Equal(gotPayload, wantPayload) {
		t.Fatalf("DRED payload mismatch\n got=%x\nwant=%x", gotPayload, wantPayload)
	}
	assertDREDPacketPrimaryFrameSizesMatchLibopus(t, gotPacket, packetInfo.packet)
}
