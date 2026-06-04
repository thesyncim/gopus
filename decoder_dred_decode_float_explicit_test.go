//go:build gopus_dred || gopus_extra_controls

package gopus

import (
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/lpcnetplc"
)

func TestDecoderExplicitDREDWarmup48kStateMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, _, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityState(t)
	if packetInfo.sampleRate != 48000 || n <= 0 {
		t.Skipf("48 kHz warmup parity requires 48 kHz packet, got sampleRate=%d frame=%d", packetInfo.sampleRate, n)
	}
	want, err := probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, -1, n, n)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED decode", err)
	}
	if dec.celtDecoder == nil {
		t.Fatal("decoder missing CELT state after seed packet")
	}
	gotPreemph := dec.celtDecoder.SnapshotPreemphasisState()
	assertFloat32ApproxEqual(t, gotPreemph[:], want.celt48k.WarmupPreemphMem[:], "warmup celt preemph_memD", 1e-4)
	var gotPLCUpdate [4 * lpcnetplc.FrameSize]float32
	_, gotPLCPreemph := dec.celtDecoder.FillPLCUpdate16kMonoWithPreemphasisMem(gotPLCUpdate[:])
	assertFloat32ApproxEqual(t, []float32{gotPLCPreemph}, []float32{want.celt48k.WarmupPLCPreemph}, "warmup plc_preemphasis_mem", 1e-4)
	assertFloat32ApproxEqual(t, gotPLCUpdate[:], want.celt48k.WarmupPLCUpdate[:], "warmup plc_update", 1e-4)
}

// The ordinary cached Decode(nil) path follows libopus FRAME_PLC_NEURAL,
// while the explicit DRED API follows FRAME_DRED. These legacy equality tests
// remain as disabled scaffolding until they are rewritten against the separate
// live-sequence and explicit libopus oracles.

func TestDecoderExplicitDREDFirstConcealFrameBootstraps48kRuntime(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, dred, packetInfo, _, n := prepareExplicitDREDDecodeParityState(t)
	if packetInfo.sampleRate != 48000 || n < lpcnetplc.FrameSize {
		t.Skipf("48 kHz bootstrap regression requires 48 kHz packet and >=%d samples, got sampleRate=%d frame=%d", lpcnetplc.FrameSize, packetInfo.sampleRate, n)
	}
	if got := dec.primeDREDCELTEntryHistory(dec.prevMode, true); got == 0 {
		t.Fatal("primeDREDCELTEntryHistory() returned 0")
	}
	window := dec.queueExplicitDREDRecovery(dred, n, n)
	if window.NeededFeatureFrames == 0 {
		t.Fatal("queueExplicitDREDRecovery produced empty window")
	}
	var frame [lpcnetplc.FrameSize]float32
	if !requireDecoderDREDState(t, dec).dredPLC.GenerateConcealedFrameFloatWithAnalysis(&requireDecoderDREDState(t, dec).dredAnalysis, &requireDecoderDREDState(t, dec).dredPredictor, &requireDecoderDREDState(t, dec).dredFARGAN, frame[:]) {
		t.Fatal("ConcealFrameFloatWithAnalysis returned false after 48 kHz bootstrap")
	}
}

func TestDecoderExplicitDREDThreeConcealFramesBootstraps48kRuntime(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, dred, packetInfo, _, n := prepareExplicitDREDDecodeParityState(t)
	if packetInfo.sampleRate != 48000 || n < 3*lpcnetplc.FrameSize {
		t.Skipf("48 kHz triple-frame regression requires 48 kHz packet and >=%d samples, got sampleRate=%d frame=%d", 3*lpcnetplc.FrameSize, packetInfo.sampleRate, n)
	}
	if got := dec.primeDREDCELTEntryHistory(dec.prevMode, true); got == 0 {
		t.Fatal("primeDREDCELTEntryHistory() returned 0")
	}
	window := dec.queueExplicitDREDRecovery(dred, n, n)
	if window.NeededFeatureFrames == 0 {
		t.Fatal("queueExplicitDREDRecovery produced empty window")
	}
	var frame [lpcnetplc.FrameSize]float32
	for i := 0; i < 3; i++ {
		if !requireDecoderDREDState(t, dec).dredPLC.GenerateConcealedFrameFloatWithAnalysis(&requireDecoderDREDState(t, dec).dredAnalysis, &requireDecoderDREDState(t, dec).dredPredictor, &requireDecoderDREDState(t, dec).dredFARGAN, frame[:]) {
			t.Fatalf("GenerateConcealedFrameFloatWithAnalysis returned false at frame %d (plc=%+v fargan=%+v)", i, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), requireDecoderDREDState(t, dec).dredFARGAN.Snapshot())
		}
	}
}

func TestDecoderExplicitDREDThreeConcealFramesManualStep48kRuntime(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, dred, packetInfo, _, n := prepareExplicitDREDDecodeParityState(t)
	if packetInfo.sampleRate != 48000 || n < 3*lpcnetplc.FrameSize {
		t.Skipf("48 kHz manual-step regression requires 48 kHz packet and >=%d samples, got sampleRate=%d frame=%d", 3*lpcnetplc.FrameSize, packetInfo.sampleRate, n)
	}
	if got := dec.primeDREDCELTEntryHistory(dec.prevMode, true); got == 0 {
		t.Fatal("primeDREDCELTEntryHistory() returned 0")
	}
	window := dec.queueExplicitDREDRecovery(dred, n, n)
	if window.NeededFeatureFrames == 0 {
		t.Fatal("queueExplicitDREDRecovery produced empty window")
	}
	if !requireDecoderDREDState(t, dec).dredPLC.PrimeFirstLossWithAnalysis(&requireDecoderDREDState(t, dec).dredAnalysis, &requireDecoderDREDState(t, dec).dredPredictor, &requireDecoderDREDState(t, dec).dredFARGAN) {
		t.Fatal("PrimeFirstLossWithAnalysis returned false")
	}
	var (
		frame    [lpcnetplc.FrameSize]float32
		features [lpcnetplc.NumFeatures]float32
	)
	for i := 0; i < 3; i++ {
		requireDecoderDREDState(t, dec).dredPLC.ConcealmentFeatureStep(&requireDecoderDREDState(t, dec).dredPredictor)
		if got := requireDecoderDREDState(t, dec).dredPLC.FillCurrentFeatures(features[:]); got != len(features) {
			t.Fatalf("FillCurrentFeatures()=%d want %d", got, len(features))
		}
		if got := requireDecoderDREDState(t, dec).dredFARGAN.Synthesize(frame[:], features[:]); got != lpcnetplc.FrameSize {
			t.Fatalf("Synthesize()=%d want %d at frame %d (fargan=%+v)", got, lpcnetplc.FrameSize, i, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot())
		}
		requireDecoderDREDState(t, dec).dredPLC.QueueFeatures(features[:])
		requireDecoderDREDState(t, dec).dredPLC.FinishConcealedFrameFloat(frame[:])
	}
}

func TestDecoderExplicitDREDThreeConcealFramesMixedHelpers48kRuntime(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, dred, packetInfo, _, n := prepareExplicitDREDDecodeParityState(t)
	if packetInfo.sampleRate != 48000 || n < 3*lpcnetplc.FrameSize {
		t.Skipf("48 kHz mixed-helper regression requires 48 kHz packet and >=%d samples, got sampleRate=%d frame=%d", 3*lpcnetplc.FrameSize, packetInfo.sampleRate, n)
	}
	if got := dec.primeDREDCELTEntryHistory(dec.prevMode, true); got == 0 {
		t.Fatal("primeDREDCELTEntryHistory() returned 0")
	}
	window := dec.queueExplicitDREDRecovery(dred, n, n)
	if window.NeededFeatureFrames == 0 {
		t.Fatal("queueExplicitDREDRecovery produced empty window")
	}
	var frame [lpcnetplc.FrameSize]float32
	if !requireDecoderDREDState(t, dec).dredPLC.ConcealFrameFloatWithAnalysis(&requireDecoderDREDState(t, dec).dredAnalysis, &requireDecoderDREDState(t, dec).dredPredictor, &requireDecoderDREDState(t, dec).dredFARGAN, frame[:]) {
		t.Fatal("ConcealFrameFloatWithAnalysis(first) returned false")
	}
	for i := 1; i < 3; i++ {
		st := requireDecoderDREDState(t, dec)
		before := st.dredPLC.Snapshot()
		gotFEC := st.dredPLC.ConcealFrameFloat(&st.dredPredictor, &st.dredFARGAN, frame[:])
		wantFEC := before.FECReadPos != before.FECFillPos && before.FECSkip == 0
		if gotFEC != wantFEC {
			t.Fatalf("ConcealFrameFloat gotFEC=%v want %v at frame %d (fecRead=%d fecFill=%d fecSkip=%d)", gotFEC, wantFEC, i, before.FECReadPos, before.FECFillPos, before.FECSkip)
		}
		after := st.dredPLC.Snapshot()
		if after.AnalysisPos >= before.AnalysisPos {
			t.Fatalf("ConcealFrameFloat did not advance at frame %d (analysisPos=%d want <%d)", i, after.AnalysisPos, before.AnalysisPos)
		}
	}
}

func TestDecoderExplicitDREDDecodeMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityState(t)

	want, err := probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, -1, n, n)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED decode", err)
	}
	requireLibopusDREDDecodeParsed(t, want, "libopus decoder DRED")
	if want.ret != n {
		t.Fatalf("libopus decoder DRED decode ret=%d want %d", want.ret, n)
	}
	if want.channels != 1 {
		t.Fatalf("libopus decoder DRED decode channels=%d want 1", want.channels)
	}

	pcm := make([]float32, dec.maxPacketSamples)
	got, err := dec.decodeExplicitDREDFloat(dred, n, pcm, n)
	if err != nil {
		t.Fatalf("decodeExplicitDREDFloat error: %v", err)
	}
	if got != n {
		t.Fatalf("decodeExplicitDREDFloat=%d want %d", got, n)
	}

	assertDecodedPCMQuality(t, pcm[:n], want.pcm[:n], dec.SampleRate(), dec.Channels(), "explicit libopus pcm")
	assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "explicit libopus plc")
	assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "explicit libopus fargan")
}

// TestDecoderExplicitStereoDREDDecodeMatchesLibopus exercises the stereo DRED
// runtime against libopus. The neural signal is mono, while CELT stereo state
// and overlap crossfade still follow celt_decode_lost().
func TestDecoderExplicitStereoDREDDecodeMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 48000, libopusDREDPacketConfig{
		FrameSize:     960,
		ForceMode:     ModeCELT,
		Bandwidth:     BandwidthFullband,
		Channels:      2,
		ForceChannels: 2,
	})
	if dec.Channels() != 2 {
		t.Fatalf("stereo explicit DRED parity got decoder channels=%d, want 2", dec.Channels())
	}

	want, err := probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, -1, n, n)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED decode", err)
	}
	requireLibopusDREDDecodeParsed(t, want, "libopus decoder stereo DRED")
	if want.ret != n {
		t.Fatalf("libopus decoder stereo DRED decode ret=%d want %d", want.ret, n)
	}
	if want.channels != 2 {
		t.Fatalf("libopus decoder stereo DRED decode channels=%d want 2", want.channels)
	}

	pcm := make([]float32, dec.maxPacketSamples*int(dec.Channels()))
	got, err := dec.decodeExplicitDREDFloat(dred, n, pcm, n)
	if err != nil {
		t.Fatalf("decodeExplicitDREDFloat error: %v", err)
	}
	if got != n {
		t.Fatalf("decodeExplicitDREDFloat=%d want %d", got, n)
	}

	// This forced-stereo carrier yields the libopus mono neural duplicate
	// shape, so both sides should stay bit-exact L=R for this fixture.
	for i := 0; i < n; i++ {
		if d := math.Abs(float64(pcm[2*i] - pcm[2*i+1])); d != 0 {
			t.Fatalf("stereo DRED PCM not L=R duplicated at sample %d: |L-R|=%g", i, d)
		}
		if d := math.Abs(float64(want.pcm[2*i] - want.pcm[2*i+1])); d != 0 {
			t.Fatalf("libopus stereo DRED PCM not L=R duplicated at sample %d: |L-R|=%g", i, d)
		}
	}

	const stereoDREDStateTol = 1e-4
	assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "explicit stereo libopus plc", stereoDREDStateTol)
	assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "explicit stereo libopus fargan", stereoDREDStateTol)
	assertDecodedPCMQuality(t, pcm[:n*dec.Channels()], want.pcm[:n*dec.Channels()], dec.SampleRate(), dec.Channels(), "explicit stereo libopus pcm")
}

// TestDecoderExplicitStereoDRED16kDecodeMatchesLibopus covers the same CELT
// stereo DRED path through the 16 kHz decoder API.
func TestDecoderExplicitStereoDRED16kDecodeMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	// Force stereo at the libopus encoder control layer so this exercises a
	// real 16 kHz stereo carrier instead of the encoder's auto mono choice.
	probeInfo, probeErr := emitLibopusDREDPacketWithConfig(libopusDREDPacketConfig{
		FrameSize:     480,
		ForceMode:     ModeCELT,
		Bandwidth:     BandwidthFullband,
		Channels:      2,
		ForceChannels: 2,
	})
	if probeErr != nil {
		libopustest.HelperUnavailable(t, "dred packet", probeErr)
	}
	if !ParseTOC(probeInfo.packet[0]).Stereo {
		t.Fatalf("libopus dred emit helper produced mono TOC at 480-sample CELT FB despite forced channels (toc=0x%02x)", probeInfo.packet[0])
	}

	dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 16000, libopusDREDPacketConfig{
		FrameSize:     480,
		ForceMode:     ModeCELT,
		Bandwidth:     BandwidthFullband,
		Channels:      2,
		ForceChannels: 2,
	})
	if dec.Channels() != 2 {
		t.Fatalf("stereo explicit DRED 16k parity got decoder channels=%d, want 2", dec.Channels())
	}

	want, err := probeLibopusDecoderDREDDecodeFloatForDecoder(seedPacket, packetInfo, 16000, -1, n, n)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED decode", err)
	}
	requireLibopusDREDDecodeParsed(t, want, "libopus decoder stereo 16k DRED")
	if want.ret != n {
		t.Fatalf("libopus decoder stereo 16k DRED decode ret=%d want %d", want.ret, n)
	}
	if want.channels != 2 {
		t.Fatalf("libopus decoder stereo 16k DRED decode channels=%d want 2", want.channels)
	}

	pcm := make([]float32, dec.maxPacketSamples*int(dec.Channels()))
	got, err := dec.decodeExplicitDREDFloat(dred, n, pcm, n)
	if err != nil {
		t.Fatalf("decodeExplicitDREDFloat error: %v", err)
	}
	if got != n {
		t.Fatalf("decodeExplicitDREDFloat=%d want %d", got, n)
	}

	// L=R interleaved invariant: both gopus and libopus must produce
	// L=R-duplicated stereo PCM for DRED concealment at 16 kHz, same as the
	// 48 kHz stereo case.
	for i := 0; i < n; i++ {
		if d := math.Abs(float64(pcm[2*i] - pcm[2*i+1])); d != 0 {
			t.Fatalf("gopus stereo 16k DRED PCM not L=R duplicated at sample %d: |L-R|=%g", i, d)
		}
		if d := math.Abs(float64(want.pcm[2*i] - want.pcm[2*i+1])); d != 0 {
			t.Fatalf("libopus stereo 16k DRED PCM not L=R duplicated at sample %d: |L-R|=%g", i, d)
		}
	}

	_, plcTol, farganTol, _ := decoderDREDLiveSequenceTolerances(480)
	assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "explicit stereo 16k libopus plc", plcTol)
	assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "explicit stereo 16k libopus fargan", farganTol)
	assertDecodedPCMQuality(t, pcm[:n*dec.Channels()], want.pcm[:n*dec.Channels()], dec.SampleRate(), dec.Channels(), "explicit stereo 16k libopus pcm")
}

func TestDecoderExplicitDREDDecode16kMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityState16k(t)

	want, err := probeLibopusDecoderDREDDecodeFloatForDecoder(seedPacket, packetInfo, 16000, -1, n, n)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED decode", err)
	}
	requireLibopusDREDDecodeParsed(t, want, "libopus decoder DRED")
	if want.ret != n {
		t.Fatalf("libopus 16k decoder DRED decode ret=%d want %d", want.ret, n)
	}
	if want.channels != 1 {
		t.Fatalf("libopus 16k decoder DRED decode channels=%d want 1", want.channels)
	}

	pcm := make([]float32, dec.maxPacketSamples)
	got, err := dec.decodeExplicitDREDFloat(dred, n, pcm, n)
	if err != nil {
		t.Fatalf("decodeExplicitDREDFloat error: %v", err)
	}
	if got != n {
		t.Fatalf("decodeExplicitDREDFloat=%d want %d", got, n)
	}

	_, plcTol, farganTol, celtTol := decoderDREDLiveSequenceTolerances(480)
	assertDecodedPCMQuality(t, pcm[:n], want.pcm[:n], dec.SampleRate(), dec.Channels(), "explicit 16k libopus pcm")
	assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "explicit 16k libopus plc", plcTol)
	assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "explicit 16k libopus fargan", farganTol)
	assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.celt48k, "explicit 16k libopus celt", celtTol)
}

func TestDecoderPublicDecodeDRED16kMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	tests := []struct {
		name      string
		cfg       libopusDREDPacketConfig
		pcmTol    float64
		plcTol    float64
		farganTol float64
		celtTol   float64
	}{
		{
			name: "celt_fb_mono",
			cfg: libopusDREDPacketConfig{
				FrameSize: 480,
				ForceMode: ModeCELT,
				Bandwidth: BandwidthFullband,
			},
		},
		{
			name: "celt_fb_stereo",
			cfg: libopusDREDPacketConfig{
				FrameSize:     480,
				ForceMode:     ModeCELT,
				Bandwidth:     BandwidthFullband,
				Channels:      2,
				ForceChannels: 2,
			},
		},
		{
			name: "hybrid_swb_mono",
			cfg: libopusDREDPacketConfig{
				FrameSize: 960,
				ForceMode: ModeHybrid,
				Bandwidth: BandwidthSuperwideband,
			},
			pcmTol:    1e-4,
			plcTol:    1e-4,
			farganTol: 1e-4,
			celtTol:   1e-4,
		},
	}
	for _, tc := range tests {
		tc := tc
		t.Run(tc.name, func(t *testing.T) {
			dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 16000, tc.cfg)
			want, err := probeLibopusDecoderDREDDecodeFloatForDecoder(seedPacket, packetInfo, 16000, -1, n, n)
			if err != nil {
				libopustest.HelperUnavailable(t, "decoder DRED decode", err)
			}
			requireLibopusDREDDecodeParsed(t, want, "libopus public decoder DRED")
			if want.ret != n {
				t.Fatalf("libopus public 16k decoder DRED decode ret=%d want %d", want.ret, n)
			}
			if want.channels != dec.Channels() {
				t.Fatalf("libopus public 16k decoder DRED channels=%d want %d", want.channels, dec.Channels())
			}

			pcm := make([]float32, dec.maxPacketSamples*int(dec.Channels()))
			got, err := dec.DecodeDRED(dred, n, pcm, n)
			if err != nil {
				t.Fatalf("DecodeDRED error: %v", err)
			}
			if got != n {
				t.Fatalf("DecodeDRED=%d want %d", got, n)
			}
			if gotDuration := dec.LastPacketDuration(); gotDuration != n {
				t.Fatalf("LastPacketDuration()=%d want API-rate frame %d", gotDuration, n)
			}

			plcTol, farganTol, celtTol := tc.plcTol, tc.farganTol, tc.celtTol
			if tc.pcmTol == 0 {
				_, plcTol, farganTol, celtTol = decoderDREDLiveSequenceTolerances(tc.cfg.FrameSize)
			}
			gotPCM := pcm[:got*dec.Channels()]
			wantPCM := want.pcm[:got*dec.Channels()]
			if dec.Channels() == 2 {
				assertInterleavedStereoApproxDuplicated(t, gotPCM, got, "public 16k DecodeDRED", 1e-2)
				assertInterleavedStereoApproxDuplicated(t, wantPCM, got, "libopus public 16k DecodeDRED", 1e-2)
			}
			assertDecodedPCMQuality(t, gotPCM, wantPCM, dec.SampleRate(), dec.Channels(), "public 16k DecodeDRED pcm")
			assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "public 16k DecodeDRED plc", plcTol)
			assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "public 16k DecodeDRED fargan", farganTol)
			assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.celt48k, "public 16k DecodeDRED celt", celtTol)
		})
	}
}

func TestDecoderPublicDecodeDREDOverlongRequestMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityStateForDecoderRateAndPacketConfig(t, 48000, libopusDREDPacketConfig{
		FrameSize: 960,
		ForceMode: ModeCELT,
		Bandwidth: BandwidthFullband,
	})
	requested := overlongAPIRateRequestedFrameSize(dec.SampleRate())
	if requested <= dec.maxPacketSamples {
		t.Fatalf("requested=%d not over max packet samples %d", requested, dec.maxPacketSamples)
	}

	want, err := probeLibopusDecoderDREDDecodeFloatForDecoder(seedPacket, packetInfo, dec.SampleRate(), -1, n, requested)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED overlong decode", err)
	}
	requireLibopusDREDDecodeParsed(t, want, "libopus public overlong DRED")
	if want.ret != requested {
		t.Fatalf("libopus public overlong DRED ret=%d want %d", want.ret, requested)
	}

	pcm := make([]float32, requested*dec.Channels())
	got, err := dec.DecodeDRED(dred, n, pcm, requested)
	if err != nil {
		t.Fatalf("DecodeDRED overlong error: %v", err)
	}
	if got != requested {
		t.Fatalf("DecodeDRED overlong=%d want %d", got, requested)
	}

	gotPCM := pcm[:got*dec.Channels()]
	wantPCM := want.pcm[:got*dec.Channels()]
	assertDecodedPCMQuality(t, gotPCM, wantPCM, dec.SampleRate(), dec.Channels(), "public overlong DecodeDRED pcm")
}

func TestDecoderExplicitDREDCELT48kBridgeMatchesLibopusFirstLoss(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityState(t)
	if packetInfo.sampleRate != 48000 || n <= 0 {
		t.Skipf("48 kHz explicit bridge parity requires 48 kHz packet, got sampleRate=%d frame=%d", packetInfo.sampleRate, n)
	}

	want, err := probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, -1, n, n)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED decode", err)
	}
	requireLibopusDREDDecodeParsed(t, want, "libopus decoder DRED")
	if want.ret != n {
		t.Fatalf("libopus decoder DRED decode ret=%d want %d", want.ret, n)
	}

	pcm := make([]float32, dec.maxPacketSamples)
	if _, err := dec.decodeExplicitDREDFloat(dred, n, pcm, n); err != nil {
		t.Fatalf("decodeExplicitDREDFloat error: %v", err)
	}
	assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.celt48k, "explicit first libopus celt")
}

func TestDecoderExplicitDREDDecodeSecondLossMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityState(t)

	pcm0 := make([]float32, dec.maxPacketSamples)
	if _, err := dec.decodeExplicitDREDFloat(dred, n, pcm0, n); err != nil {
		t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
	}

	want, err := probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 2*n, n)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED decode", err)
	}
	requireLibopusDREDDecodeParsed(t, want, "libopus decoder DRED")
	if want.warmupRet != n {
		t.Fatalf("libopus decoder DRED warmup ret=%d want %d", want.warmupRet, n)
	}
	if want.ret != n {
		t.Fatalf("libopus decoder DRED second ret=%d want %d", want.ret, n)
	}
	if want.channels != 1 {
		t.Fatalf("libopus decoder DRED second channels=%d want 1", want.channels)
	}

	pcm := make([]float32, dec.maxPacketSamples)
	got, err := dec.decodeExplicitDREDFloat(dred, 2*n, pcm, n)
	if err != nil {
		t.Fatalf("decodeExplicitDREDFloat(second) error: %v", err)
	}
	if got != n {
		t.Fatalf("decodeExplicitDREDFloat(second)=%d want %d", got, n)
	}

	assertDecodedPCMQuality(t, pcm[:n], want.pcm[:n], dec.SampleRate(), dec.Channels(), "explicit second libopus pcm")
	assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "explicit second libopus plc")
	assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "explicit second libopus fargan")
}

func TestDecoderExplicitDREDDecodeSecondLossGainTransitionMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityState(t)
	const gainA = 256
	const gainB = -512

	if err := dec.SetGain(gainA); err != nil {
		t.Fatalf("SetGain(%d) error: %v", gainA, err)
	}
	wantFirst, err := probeLibopusDecoderDREDDecodeFloatWithGain(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, -1, n, n, gainA)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED decode", err)
	}
	requireLibopusDREDDecodeParsed(t, wantFirst, "libopus decoder DRED")
	if wantFirst.ret != n {
		t.Fatalf("libopus decoder DRED first ret=%d want %d", wantFirst.ret, n)
	}

	pcm0 := make([]float32, dec.maxPacketSamples)
	got, err := dec.decodeExplicitDREDFloat(dred, n, pcm0, n)
	if err != nil {
		t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
	}
	if got != n {
		t.Fatalf("decodeExplicitDREDFloat(first)=%d want %d", got, n)
	}

	assertDecodedPCMQuality(t, pcm0[:n], wantFirst.pcm[:n], dec.SampleRate(), dec.Channels(), "explicit second-loss gain transition first libopus pcm")
	assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), wantFirst.state, "explicit second-loss gain transition first libopus plc")
	assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), wantFirst.fargan, "explicit second-loss gain transition first libopus fargan")

	if err := dec.SetGain(gainB); err != nil {
		t.Fatalf("SetGain(%d) error: %v", gainB, err)
	}
	wantSecond, err := probeLibopusDecoderDREDDecodeFloatWithGain(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 2*n, n, gainB)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED decode", err)
	}
	requireLibopusDREDDecodeParsed(t, wantSecond, "libopus decoder DRED")
	if wantSecond.warmupRet != n {
		t.Fatalf("libopus decoder DRED warmup ret=%d want %d", wantSecond.warmupRet, n)
	}
	if wantSecond.ret != n {
		t.Fatalf("libopus decoder DRED second ret=%d want %d", wantSecond.ret, n)
	}

	pcm1 := make([]float32, dec.maxPacketSamples)
	got, err = dec.decodeExplicitDREDFloat(dred, 2*n, pcm1, n)
	if err != nil {
		t.Fatalf("decodeExplicitDREDFloat(second) error: %v", err)
	}
	if got != n {
		t.Fatalf("decodeExplicitDREDFloat(second)=%d want %d", got, n)
	}

	assertDecodedPCMQuality(t, pcm1[:n], wantSecond.pcm[:n], dec.SampleRate(), dec.Channels(), "explicit second-loss gain transition second libopus pcm")
	assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), wantSecond.state, "explicit second-loss gain transition second libopus plc")
	assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), wantSecond.fargan, "explicit second-loss gain transition second libopus fargan")
	assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, wantSecond.celt48k, "explicit second-loss gain transition second libopus celt")
}

func TestDecoderExplicitDREDDecodeSecondLoss16kMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityState16k(t)

	pcm0 := make([]float32, dec.maxPacketSamples)
	if _, err := dec.decodeExplicitDREDFloat(dred, n, pcm0, n); err != nil {
		t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
	}

	want, err := probeLibopusDecoderDREDDecodeFloatForDecoder(seedPacket, packetInfo, 16000, n, 2*n, n)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED decode", err)
	}
	requireLibopusDREDDecodeParsed(t, want, "libopus decoder DRED")
	if want.warmupRet != n {
		t.Fatalf("libopus 16k decoder DRED warmup ret=%d want %d", want.warmupRet, n)
	}
	if want.ret != n {
		t.Fatalf("libopus 16k decoder DRED second ret=%d want %d", want.ret, n)
	}
	if want.channels != 1 {
		t.Fatalf("libopus 16k decoder DRED second channels=%d want 1", want.channels)
	}

	pcm := make([]float32, dec.maxPacketSamples)
	got, err := dec.decodeExplicitDREDFloat(dred, 2*n, pcm, n)
	if err != nil {
		t.Fatalf("decodeExplicitDREDFloat(second) error: %v", err)
	}
	if got != n {
		t.Fatalf("decodeExplicitDREDFloat(second)=%d want %d", got, n)
	}

	_, plcTol, farganTol, celtTol := decoderDREDLiveSequenceTolerances(480)
	assertDecodedPCMQuality(t, pcm[:n], want.pcm[:n], dec.SampleRate(), dec.Channels(), "explicit 16k second libopus pcm")
	assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "explicit 16k second libopus plc", plcTol)
	assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "explicit 16k second libopus fargan", farganTol)
	assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.celt48k, "explicit 16k second libopus celt", celtTol)
}

func TestDecoderExplicitDREDCELT48kBridgeMatchesLibopusSecondLoss(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityState(t)
	if packetInfo.sampleRate != 48000 || n <= 0 {
		t.Skipf("48 kHz explicit bridge parity requires 48 kHz packet, got sampleRate=%d frame=%d", packetInfo.sampleRate, n)
	}
	pcm0 := make([]float32, dec.maxPacketSamples)
	if _, err := dec.decodeExplicitDREDFloat(dred, n, pcm0, n); err != nil {
		t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
	}

	want, err := probeLibopusDecoderDREDDecodeFloat(seedPacket, packetInfo.packet, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 2*n, n)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED decode", err)
	}
	requireLibopusDREDDecodeParsed(t, want, "libopus decoder DRED second")
	if want.ret != n {
		t.Fatalf("libopus decoder DRED second decode ret=%d want %d", want.ret, n)
	}

	pcm1 := make([]float32, dec.maxPacketSamples)
	if _, err := dec.decodeExplicitDREDFloat(dred, 2*n, pcm1, n); err != nil {
		t.Fatalf("decodeExplicitDREDFloat(second) error: %v", err)
	}
	assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.celt48k, "explicit second libopus celt")
}

func TestDecoderExplicitDREDDecodeThenNextPacketMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityState(t)
	if packetInfo.sampleRate != 48000 || n <= 0 {
		t.Skipf("48 kHz explicit follow-up parity requires 48 kHz packet, got sampleRate=%d frame=%d", packetInfo.sampleRate, n)
	}
	nextPacket := makeValidMonoCELTPacketForDREDTest(t)

	lossPCM := make([]float32, dec.maxPacketSamples)
	if _, err := dec.decodeExplicitDREDFloat(dred, n, lossPCM, n); err != nil {
		t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
	}

	want, err := probeLibopusDecoderDREDDecodeAndNextFloat(seedPacket, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, -1, n, n)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED decode", err)
	}
	requireLibopusDREDDecodeParsed(t, want, "libopus decoder DRED")
	if want.ret != n {
		t.Fatalf("libopus decoder DRED decode ret=%d want %d", want.ret, n)
	}
	if want.nextRet <= 0 {
		t.Fatalf("libopus decoder follow-up ret=%d want >0", want.nextRet)
	}

	nextPCM := make([]float32, dec.maxPacketSamples)
	gotNext, err := dec.Decode(nextPacket, nextPCM)
	if err != nil {
		t.Fatalf("Decode(next packet) error: %v", err)
	}
	if gotNext != want.nextRet {
		t.Fatalf("Decode(next packet)=%d want %d", gotNext, want.nextRet)
	}

	assertDecodedPCMQuality(t, nextPCM[:gotNext], want.nextPCM[:gotNext], dec.SampleRate(), dec.Channels(), "explicit next packet pcm")
	assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "explicit next packet plc")
	assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "explicit next packet fargan")
	assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.celt48k, "explicit next packet celt")
}

func TestDecoderExplicitDREDDecodeThenNextPacket16kMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityState16k(t)
	nextPacket := makeValidMonoCELTPacketForFrameSizeForDREDTest(t, 480)

	lossPCM := make([]float32, dec.maxPacketSamples)
	if _, err := dec.decodeExplicitDREDFloat(dred, n, lossPCM, n); err != nil {
		t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
	}

	want, err := probeLibopusDecoderDREDDecodeAndNextFloatForDecoder(seedPacket, packetInfo, nextPacket, 16000, -1, n, n)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED decode", err)
	}
	requireLibopusDREDDecodeParsed(t, want, "libopus decoder DRED")
	if want.ret != n {
		t.Fatalf("libopus 16k decoder DRED decode ret=%d want %d", want.ret, n)
	}
	if want.nextRet <= 0 {
		t.Fatalf("libopus 16k decoder follow-up ret=%d want >0", want.nextRet)
	}

	nextPCM := make([]float32, dec.maxPacketSamples)
	gotNext, err := dec.Decode(nextPacket, nextPCM)
	if err != nil {
		t.Fatalf("Decode(next packet) error: %v", err)
	}
	if gotNext != want.nextRet {
		t.Fatalf("Decode(next packet)=%d want %d", gotNext, want.nextRet)
	}

	_, plcTol, farganTol, celtTol := decoderDREDLiveSequenceTolerances(480)
	assertDecodedPCMQuality(t, nextPCM[:gotNext], want.nextPCM[:gotNext], dec.SampleRate(), dec.Channels(), "explicit 16k next packet pcm")
	assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "explicit 16k next packet plc", plcTol)
	assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "explicit 16k next packet fargan", farganTol)
	assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.celt48k, "explicit 16k next packet celt", celtTol)
}

func TestDecoderExplicitSecondLossThenNextPacket16kMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityState16k(t)
	nextPacket := makeValidMonoCELTPacketForFrameSizeForDREDTest(t, 480)

	pcm0 := make([]float32, dec.maxPacketSamples)
	if _, err := dec.decodeExplicitDREDFloat(dred, n, pcm0, n); err != nil {
		t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
	}
	pcm1 := make([]float32, dec.maxPacketSamples)
	if _, err := dec.decodeExplicitDREDFloat(dred, 2*n, pcm1, n); err != nil {
		t.Fatalf("decodeExplicitDREDFloat(second) error: %v", err)
	}

	want, err := probeLibopusDecoderDREDDecodeAndNextFloatForDecoder(seedPacket, packetInfo, nextPacket, 16000, n, 2*n, n)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED decode", err)
	}
	requireLibopusDREDDecodeParsed(t, want, "libopus decoder DRED")
	if want.warmupRet != n {
		t.Fatalf("libopus 16k decoder warmup ret=%d want %d", want.warmupRet, n)
	}
	if want.ret != n {
		t.Fatalf("libopus 16k decoder second ret=%d want %d", want.ret, n)
	}
	if want.nextRet <= 0 {
		t.Fatalf("libopus 16k decoder follow-up ret=%d want >0", want.nextRet)
	}

	nextPCM := make([]float32, dec.maxPacketSamples)
	gotNext, err := dec.Decode(nextPacket, nextPCM)
	if err != nil {
		t.Fatalf("Decode(next packet) error: %v", err)
	}
	if gotNext != want.nextRet {
		t.Fatalf("Decode(next packet)=%d want %d", gotNext, want.nextRet)
	}

	_, plcTol, farganTol, celtTol := decoderDREDLiveSequenceTolerances(480)
	assertDecodedPCMQuality(t, nextPCM[:gotNext], want.nextPCM[:gotNext], dec.SampleRate(), dec.Channels(), "explicit 16k second-loss next packet pcm")
	assertDecoderDREDPLCStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "explicit 16k second-loss next packet plc", plcTol)
	assertDecoderDREDFARGANStateApproxEqualWithin(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "explicit 16k second-loss next packet fargan", farganTol)
	assertDecoderDREDCELT48kBridgeApproxEqualWithin(t, dec, want.celt48k, "explicit 16k second-loss next packet celt", celtTol)
}

func TestDecoderExplicitSecondLossThenNextPacketMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	dec, dred, packetInfo, seedPacket, n := prepareExplicitDREDDecodeParityState(t)
	if packetInfo.sampleRate != 48000 || n <= 0 {
		t.Skipf("48 kHz explicit second-loss follow-up parity requires 48 kHz packet, got sampleRate=%d frame=%d", packetInfo.sampleRate, n)
	}
	nextPacket := makeValidMonoCELTPacketForDREDTest(t)

	pcm0 := make([]float32, dec.maxPacketSamples)
	if _, err := dec.decodeExplicitDREDFloat(dred, n, pcm0, n); err != nil {
		t.Fatalf("decodeExplicitDREDFloat(first) error: %v", err)
	}
	pcm1 := make([]float32, dec.maxPacketSamples)
	if _, err := dec.decodeExplicitDREDFloat(dred, 2*n, pcm1, n); err != nil {
		t.Fatalf("decodeExplicitDREDFloat(second) error: %v", err)
	}

	want, err := probeLibopusDecoderDREDDecodeAndNextFloat(seedPacket, packetInfo.packet, nextPacket, packetInfo.maxDREDSamples, packetInfo.sampleRate, n, 2*n, n)
	if err != nil {
		libopustest.HelperUnavailable(t, "decoder DRED decode", err)
	}
	requireLibopusDREDDecodeParsed(t, want, "libopus decoder DRED")
	if want.warmupRet != n {
		t.Fatalf("libopus decoder DRED warmup ret=%d want %d", want.warmupRet, n)
	}
	if want.ret != n {
		t.Fatalf("libopus decoder DRED second ret=%d want %d", want.ret, n)
	}
	if want.nextRet <= 0 {
		t.Fatalf("libopus decoder second-loss follow-up ret=%d want >0", want.nextRet)
	}

	nextPCM := make([]float32, dec.maxPacketSamples)
	gotNext, err := dec.Decode(nextPacket, nextPCM)
	if err != nil {
		t.Fatalf("Decode(next packet) after second loss error: %v", err)
	}
	if gotNext != want.nextRet {
		t.Fatalf("Decode(next packet) after second loss=%d want %d", gotNext, want.nextRet)
	}

	assertDecodedPCMQuality(t, nextPCM[:gotNext], want.nextPCM[:gotNext], dec.SampleRate(), dec.Channels(), "explicit second-loss next packet pcm")
	assertDecoderDREDPLCStateApproxEqual(t, requireDecoderDREDState(t, dec).dredPLC.Snapshot(), want.state, "explicit second-loss next packet plc")
	assertDecoderDREDFARGANStateApproxEqual(t, requireDecoderDREDState(t, dec).dredFARGAN.Snapshot(), want.fargan, "explicit second-loss next packet fargan")
	assertDecoderDREDCELT48kBridgeApproxEqual(t, dec, want.celt48k, "explicit second-loss next packet celt")
}
