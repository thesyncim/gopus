//go:build linux && amd64.v3 && gopus_celt_trace && !gopus_fixed_point && !gopus_qext

package encoder

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"strings"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/libopustooling"
	"github.com/thesyncim/gopus/internal/testsignal"
	"github.com/thesyncim/gopus/types"
)

const (
	celtOnlyCBRFrameSize   = 960
	celtOnlyCBRChannels    = 2
	celtOnlyCBRBitrate     = 96000
	celtOnlyCBRFrames      = 50
	celtOnlyCBRCapacity    = 4000
	celtOnlyCBRPacketBytes = 240
	celtOnlyCBRAudioApp    = 0
	celtOnlyCBRFullband    = 1105
)

var celtOnlyCBRStageOracleCache libopustest.HelperCache

// TestCELTOnlyCBRSelectedFrameStageDiagnostic traces the actual mode selected
// for one frame in the 50-frame Auto CBR stream. The selected frame is CELT-only
// on both paths, so the trace compares its CELT producer stages directly.
func TestCELTOnlyCBRSelectedFrameStageDiagnostic(t *testing.T) {
	libopustest.RequireOracle(t)
	requireCELTTraceV3(t, "CELT-only CBR selected-frame stage")

	pcm, err := testsignal.GenerateEncoderSignalVariant(
		testsignal.EncoderVariantAMMultisineV1,
		48000,
		celtOnlyCBRFrameSize*celtOnlyCBRChannels*celtOnlyCBRFrames,
		celtOnlyCBRChannels,
	)
	if err != nil {
		t.Fatalf("generate Auto CBR input: %v", err)
	}
	pcm = quantizeCELTTracePCM(pcm)
	input := celtTraceCBRInputFor(pcm, celtOnlyCBRAudioApp, celtOnlyCBRFullband,
		celtOnlyCBRChannels, celtOnlyCBRBitrate, celtOnlyCBRFrameSize,
		celtOnlyCBRFrames, 10)
	inputHash := sha256.Sum256(input)
	t.Logf("fixture=AMMultisineV1 input=Auto-FB-20ms-stereo-96k frames=%d frame_size=%d channels=%d bitrate=%d complexity=10 max_data_bytes=%d GCBR_sha256=%s target_frame=%d",
		celtOnlyCBRFrames, celtOnlyCBRFrameSize, celtOnlyCBRChannels, celtOnlyCBRBitrate,
		celtOnlyCBRCapacity, hex.EncodeToString(inputHash[:]), celtOnlyCBRSelectedFrame)

	ordinaryPath := buildCELTTraceOracle(t, false)
	ordinaryBytes, err := libopustest.RunHelper(ordinaryPath, input)
	if err != nil {
		t.Fatalf("run ordinary Auto CBR oracle: %v", err)
	}
	ordinary, err := parseCELTTraceCBRPrefix(ordinaryBytes)
	if err != nil {
		t.Fatalf("parse ordinary Auto CBR output: %v", err)
	}
	if len(ordinary.Packets) != celtOnlyCBRFrames || len(ordinary.FinalRanges) != celtOnlyCBRFrames {
		t.Fatalf("ordinary C returned packets=%d ranges=%d, want %d", len(ordinary.Packets), len(ordinary.FinalRanges), celtOnlyCBRFrames)
	}
	for frame, packet := range ordinary.Packets {
		if len(packet) != celtOnlyCBRPacketBytes {
			t.Fatalf("ordinary C CBR frame %d has %d bytes, want %d", frame, len(packet), celtOnlyCBRPacketBytes)
		}
	}
	targetCTOC := ordinary.Packets[celtOnlyCBRSelectedFrame][0]
	targetCConfig := targetCTOC >> 3
	targetCMode := opusTOCMode(targetCConfig)
	t.Logf("selected ordinary C frame %d TOC=0x%02x config=%d mode=%s",
		celtOnlyCBRSelectedFrame, targetCTOC, targetCConfig, targetCMode)
	if targetCConfig != 31 || targetCMode != "CELT" {
		t.Fatalf("selected ordinary C frame %d is config=%d mode=%s, want actual CELT-only config 31",
			celtOnlyCBRSelectedFrame, targetCConfig, targetCMode)
	}

	tracePath := buildCELTTraceOracleAtFrameWithCache(t, true, celtOnlyCBRSelectedFrame, &celtOnlyCBRStageOracleCache)
	traceBytes, err := libopustest.RunHelper(tracePath, input)
	if err != nil {
		t.Fatalf("run traced Auto CBR stage oracle: %v", err)
	}
	tracePrefix, tracePayload, err := splitCELTTraceOutput(traceBytes)
	if err != nil {
		t.Fatalf("split traced Auto CBR output: %v", err)
	}
	traced, err := parseCELTTraceCBRPrefix(tracePrefix)
	if err != nil {
		t.Fatalf("parse traced Auto CBR output: %v", err)
	}
	if !sameCELTTraceCBROutput(ordinary, traced) {
		t.Fatalf("C stage tracing changed packets or final ranges for the %d-frame stream", celtOnlyCBRFrames)
	}
	cTrace, err := parseCELTEncodeTrace(tracePayload)
	if err != nil {
		t.Fatalf("parse C CELT stage trace: %v", err)
	}
	if !strings.Contains(ordinary.LibopusVersion, libopustooling.DefaultVersion) {
		t.Fatalf("C oracle version=%q does not identify pinned libopus %q", ordinary.LibopusVersion, libopustooling.DefaultVersion)
	}
	if cTrace.TraceFrame != celtOnlyCBRSelectedFrame || cTrace.Overflow != 0 {
		t.Fatalf("C stage trace selected frame=%d overflow=%d, want frame %d and no overflow",
			cTrace.TraceFrame, cTrace.Overflow, celtOnlyCBRSelectedFrame)
	}
	if cTrace.BandCalls == 0 || cTrace.LogCalls == 0 || cTrace.NormalizationCalls == 0 ||
		cTrace.CoarseCalls == 0 || cTrace.QuantCalls == 0 || cTrace.PreemphasisCalls != celtOnlyCBRChannels || cTrace.MDCTCalls == 0 {
		t.Fatalf("C CELT-only stage capture lacks actual selected-frame calls: %s", cTrace.counts())
	}
	t.Logf("C oracle=%q archmask=0x%x features=0x%x selected_arch=%d target frame=%d TOC config=%d mode=%s CELT stage calls: %s",
		ordinary.LibopusVersion, ordinary.ArchMask, ordinary.BuildFeatures, ordinary.SelectedArch,
		celtOnlyCBRSelectedFrame, targetCConfig, targetCMode, cTrace.counts())

	tracedGo := newCELTOnlyCBREncoder()
	plainGo := newCELTOnlyCBREncoder()
	tracedPackets, tracedRanges := make([][]byte, 0, celtOnlyCBRFrames), make([]uint32, 0, celtOnlyCBRFrames)
	plainPackets, plainRanges := make([][]byte, 0, celtOnlyCBRFrames), make([]uint32, 0, celtOnlyCBRFrames)
	var goTrace celt.EncodeStageTrace
	for frame := range celtOnlyCBRFrames {
		framePCM := pcm[frame*celtOnlyCBRFrameSize*celtOnlyCBRChannels : (frame+1)*celtOnlyCBRFrameSize*celtOnlyCBRChannels]
		if frame == celtOnlyCBRSelectedFrame {
			tracedGo.ensureCELTEncoder()
			tracedGo.celtEncoder.EnableEncodeStageTraceForTesting()
		}
		packet, encodeErr := tracedGo.EncodeFloat32WithAnalysisMaxBytes(framePCM, celtOnlyCBRFrameSize, framePCM, celtOnlyCBRCapacity)
		if encodeErr != nil {
			t.Fatalf("encode traced Go Auto frame %d: %v", frame, encodeErr)
		}
		tracedPackets = append(tracedPackets, append([]byte(nil), packet...))
		tracedRanges = append(tracedRanges, tracedGo.FinalRange())
		plainPacket, plainErr := encodeCBRStreamFrame(plainGo, pcm, frame)
		if plainErr != nil {
			t.Fatalf("encode plain Go Auto frame %d: %v", frame, plainErr)
		}
		plainPackets = append(plainPackets, plainPacket)
		plainRanges = append(plainRanges, plainGo.FinalRange())
		if frame == celtOnlyCBRSelectedFrame {
			goTrace = tracedGo.celtEncoder.EncodeStageTraceForTesting()
		}
	}
	if !sameCBRStream(tracedPackets, tracedRanges, plainPackets, plainRanges) {
		t.Fatal("Go CELT stage trace changed one or more packets or final ranges in the 50-frame stream")
	}
	if len(tracedPackets[celtOnlyCBRSelectedFrame]) == 0 {
		t.Fatalf("traced Go frame %d has no packet", celtOnlyCBRSelectedFrame)
	}
	targetGoTOC := tracedPackets[celtOnlyCBRSelectedFrame][0]
	targetGoConfig := targetGoTOC >> 3
	targetGoMode := opusTOCMode(targetGoConfig)
	t.Logf("selected traced Go frame %d TOC=0x%02x config=%d mode=%s stage-overflow=%t",
		celtOnlyCBRSelectedFrame, targetGoTOC, targetGoConfig, targetGoMode, goTrace.StageOverflow)
	if targetGoConfig != 31 || targetGoMode != "CELT" {
		t.Fatalf("selected Go frame %d is config=%d mode=%s, want actual CELT-only config 31",
			celtOnlyCBRSelectedFrame, targetGoConfig, targetGoMode)
	}
	if goTrace.StageOverflow {
		t.Fatal("Go selected-frame CELT stage trace overflowed its bounded capture")
	}
	if len(goTrace.BandStages) == 0 || len(goTrace.Normalizations) == 0 || len(goTrace.CoarseEnergy) == 0 ||
		len(goTrace.BandQuantize) == 0 || len(goTrace.MDCTCalls) == 0 || len(goTrace.Preemphasis) != celtOnlyCBRChannels {
		t.Fatalf("Go CELT-only stage capture lacks actual selected-frame calls: bands=%d normalize=%d coarse=%d quant=%d mdct=%d preemphasis=%d",
			len(goTrace.BandStages), len(goTrace.Normalizations), len(goTrace.CoarseEnergy), len(goTrace.BandQuantize),
			len(goTrace.MDCTCalls), len(goTrace.Preemphasis))
	}
	if err := validateCELTTraceShapes(goTrace, cTrace); err != nil {
		t.Fatalf("invalid selected-frame Go/C CELT trace shapes: %v", err)
	}
	if err := validateCELTTraceExpectedDimensions(goTrace, cTrace, celtOnlyCBRFrameSize, celtTraceBandCount,
		celtOnlyCBRChannels, 800, 3); err != nil {
		t.Fatalf("unexpected selected-frame Go/C CELT dimensions: %v", err)
	}
	if err := validateCELTOnlyCBRMDCTGeometry(goTrace, cTrace, celtOnlyCBRFrameSize,
		celtOnlyCBRChannels, 120, 3); err != nil {
		t.Fatalf("unexpected selected-frame Go/C MDCT geometry: %v", err)
	}
	t.Logf("trace transparency: all %d C and Go packets and final ranges match their untraced controls", celtOnlyCBRFrames)
	logCELTTraceDifferences(t, goTrace, cTrace)
	for frame, packet := range tracedPackets {
		if len(packet) != celtOnlyCBRPacketBytes {
			t.Fatalf("traced Go CBR frame %d has %d bytes, want %d", frame, len(packet), celtOnlyCBRPacketBytes)
		}
	}
	if len(tracedPackets[celtOnlyCBRSelectedFrame]) != len(ordinary.Packets[celtOnlyCBRSelectedFrame]) {
		t.Fatalf("selected packet size differs: Go=%d C=%d",
			len(tracedPackets[celtOnlyCBRSelectedFrame]), len(ordinary.Packets[celtOnlyCBRSelectedFrame]))
	}
	packetDiff := firstCELTTraceByteDifference(tracedPackets[celtOnlyCBRSelectedFrame], ordinary.Packets[celtOnlyCBRSelectedFrame])
	t.Logf("selected frame %d packet byte diff=%d Go range=0x%08x C range=0x%08x",
		celtOnlyCBRSelectedFrame, packetDiff,
		tracedRanges[celtOnlyCBRSelectedFrame], ordinary.FinalRanges[celtOnlyCBRSelectedFrame])
}

func opusTOCMode(config uint8) string {
	switch {
	case config <= 11:
		return "SILK"
	case config <= 15:
		return "Hybrid"
	default:
		return "CELT"
	}
}

// validateCELTOnlyCBRMDCTGeometry checks the actual long/short transform
// sequence selected by celt_encoder.c. At complexity >= 8, a transient frame
// runs one long transform per channel for secondMdct before its eight short
// transforms per channel.
func validateCELTOnlyCBRMDCTGeometry(goTrace celt.EncodeStageTrace, cTrace celtCBRStageTrace,
	frameSize, channels, overlap, maxShift int) error {
	if err := validateCELTMDCTTraceShapes(goTrace, cTrace); err != nil {
		return err
	}
	callCount := len(goTrace.MDCTCalls)
	if channels <= 0 || callCount%channels != 0 {
		return fmt.Errorf("MDCT call count %d is not divisible by channel count %d", len(goTrace.MDCTCalls), channels)
	}
	blocksPerChannel := callCount / channels
	mixedLongAndShort := blocksPerChannel == 9
	if blocksPerChannel != 1 && blocksPerChannel != 8 && !mixedLongAndShort {
		return fmt.Errorf("captured %d MDCT blocks per channel; want source long count 1, short count 8, or secondMdct long-plus-short count 9", blocksPerChannel)
	}
	for i, got := range goTrace.MDCTCalls {
		wantC := cTrace.MDCT[i]
		callIndex := i
		callsPerChannel, transformN, shift := 1, 2*frameSize, 0
		if blocksPerChannel == 8 || (mixedLongAndShort && i >= channels) {
			callsPerChannel, transformN, shift = 8, frameSize/4, maxShift
			if mixedLongAndShort {
				// compute_mdcts(mode, 0, ...) emits one long call per channel
				// before compute_mdcts(mode, M, ...) emits short-block calls.
				callIndex -= channels
			}
		}
		channel, block := callIndex/callsPerChannel, callIndex%callsPerChannel
		lookupN := transformN << shift
		if got.Channel != channel || got.Block != block || got.TransformN != transformN || got.MaxShift != maxShift ||
			got.Shift != shift || got.Stride != callsPerChannel || got.Overlap != overlap ||
			got.FFTSize != transformN/4 || got.LookupN != lookupN {
			return fmt.Errorf("MDCT call %d Go=(channel=%d block=%d lookup=%d maxshift=%d n=%d shift=%d stride=%d overlap=%d fft=%d) C=(lookup=%d maxshift=%d n=%d shift=%d stride=%d overlap=%d fft=%d), want branch blocks=%d transform=%d shift=%d",
				i, got.Channel, got.Block, got.LookupN, got.MaxShift, got.TransformN, got.Shift, got.Stride, got.Overlap, got.FFTSize,
				wantC.LookupN, wantC.MaxShift, wantC.TransformN, wantC.Shift, wantC.Stride, wantC.Overlap, wantC.FFTSize,
				callsPerChannel, transformN, shift)
		}
	}
	return nil
}

func TestCELTOnlyCBRMDCTGeometrySequences(t *testing.T) {
	for _, tc := range []struct {
		name        string
		long, short bool
	}{
		{name: "long", long: true},
		{name: "short", short: true},
		{name: "second_mdct_then_short", long: true, short: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			goTrace, cTrace := syntheticCELTOnlyCBRMDCTTrace(tc.long, tc.short, celtOnlyCBRChannels, celtOnlyCBRFrameSize, 120, 3)
			if err := validateCELTOnlyCBRMDCTGeometry(goTrace, cTrace, celtOnlyCBRFrameSize,
				celtOnlyCBRChannels, 120, 3); err != nil {
				t.Fatalf("valid source-selected MDCT sequence rejected: %v", err)
			}
		})
	}
}

func syntheticCELTOnlyCBRMDCTTrace(long, short bool, channels, frameSize, overlap, maxShift int) (celt.EncodeStageTrace, celtCBRStageTrace) {
	var goTrace celt.EncodeStageTrace
	var cTrace celtCBRStageTrace
	appendCalls := func(transformN, shift, stride int) {
		fftSize := transformN / 4
		input := make([]float32, transformN/2+overlap)
		window := make([]float32, overlap)
		trig := make([]float32, transformN/2)
		for channel := range channels {
			for block := range stride {
				goTrace.MDCTCalls = append(goTrace.MDCTCalls, celt.EncodeMDCTCallTrace{
					Channel: channel, Block: block, LookupN: transformN << shift,
					MaxShift: maxShift, TransformN: transformN, Shift: shift,
					Stride: stride, Overlap: overlap, FFTSize: fftSize,
					Input: input, Window: window, Trig: trig,
				})
				cTrace.MDCT = append(cTrace.MDCT, celtCBRStageMDCT{
					LookupN: transformN << shift, MaxShift: maxShift,
					TransformN: transformN, Shift: shift, Stride: stride,
					Overlap: overlap, FFTSize: fftSize,
					Input: input, Window: window, Trig: trig,
				})
			}
		}
	}
	if long {
		appendCalls(2*frameSize, 0, 1)
	}
	if short {
		appendCalls(frameSize/4, maxShift, 8)
	}
	cTrace.MDCTCalls = len(cTrace.MDCT)
	return goTrace, cTrace
}

func newCELTOnlyCBREncoder() *Encoder {
	e := NewEncoder(48000, celtOnlyCBRChannels)
	e.SetMode(ModeAuto)
	e.SetRestrictedSilkApplication(false)
	e.SetLowDelay(false)
	e.SetBandwidth(types.BandwidthFullband)
	e.SetBitrate(celtOnlyCBRBitrate)
	e.SetBitrateMode(ModeCBR)
	e.SetComplexity(10)
	return e
}

func encodeCBRStreamFrame(e *Encoder, pcm []float32, frame int) ([]byte, error) {
	samples := celtOnlyCBRFrameSize * celtOnlyCBRChannels
	start := frame * samples
	end := start + samples
	packet, err := e.EncodeFloat32WithAnalysisMaxBytes(pcm[start:end], celtOnlyCBRFrameSize,
		pcm[start:end], celtOnlyCBRCapacity)
	if err != nil {
		return nil, err
	}
	return append([]byte(nil), packet...), nil
}

func sameCBRStream(aPackets [][]byte, aRanges []uint32, bPackets [][]byte, bRanges []uint32) bool {
	if len(aPackets) != celtOnlyCBRFrames || len(bPackets) != celtOnlyCBRFrames ||
		len(aRanges) != celtOnlyCBRFrames || len(bRanges) != celtOnlyCBRFrames {
		return false
	}
	for i := range aPackets {
		if !bytes.Equal(aPackets[i], bPackets[i]) || aRanges[i] != bRanges[i] {
			return false
		}
	}
	return true
}
