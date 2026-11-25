//go:build linux && amd64.v3 && gopus_celt_trace && !gopus_fixed_point && !gopus_qext

package encoder

import (
	"bytes"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/libopustooling"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/testsignal"
	"github.com/thesyncim/gopus/types"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

var celtOnlyCBRQuantTraceCache libopustest.HelperCache
var celtOnlyCBRQuantTraceTFCache libopustest.HelperCache

// TestCELTOnlyCBRQuantBandTraceDiagnostic compares the real selected-band
// quantization events in the 50-frame Auto CBR stream. The C trace helper uses
// GEDI controls equivalent to the ordinary GCBR oracle; every packet and final
// range must match before the selected-frame trace is examined.
func TestCELTOnlyCBRQuantBandTraceDiagnostic(t *testing.T) {
	libopustest.RequireOracle(t)
	requireCELTTraceV3(t, "CELT-only CBR selected-band quantization")

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
	cbInput := celtTraceCBRInputFor(pcm, celtOnlyCBRAudioApp, celtOnlyCBRFullband,
		celtOnlyCBRChannels, celtOnlyCBRBitrate, celtOnlyCBRFrameSize,
		celtOnlyCBRFrames, 10)
	ordinaryPath := buildCELTTraceOracle(t, false)
	ordinaryBytes, err := libopustest.RunHelper(ordinaryPath, cbInput)
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
	if ordinary.Packets[celtOnlyCBRQuantTraceFrame][0]>>3 != 31 {
		t.Fatalf("ordinary C selected frame %d has TOC config %d, want CELT-only config 31", celtOnlyCBRQuantTraceFrame,
			ordinary.Packets[celtOnlyCBRQuantTraceFrame][0]>>3)
	}
	if !containsPinnedLibopus(ordinary.LibopusVersion) {
		t.Fatalf("C oracle version=%q does not identify pinned libopus %q", ordinary.LibopusVersion, libopustooling.DefaultVersion)
	}

	tracePath := buildCELTQuantTraceOracleAtFrameBandWithTFTrace(t, celtOnlyCBRQuantTraceFrame,
		celtOnlyCBRQuantTraceBand, &celtOnlyCBRQuantTraceTFCache)
	tracedBytes, err := libopustest.RunHelper(tracePath, celtOnlyCBRGEDIInput(pcm))
	if err != nil {
		t.Fatalf("run selected-frame GEDI/GQTR oracle: %v", err)
	}
	cRecords, prefixBytes, err := parseCELTQuantTraceGEDOPrefix(tracedBytes, celtOnlyCBRFrames)
	if err != nil {
		t.Fatalf("parse traced GEDI packet prefix: %v", err)
	}
	if !sameEncodeDiffRecords(cRecords, celtOnlyCBRRecords(ordinary)) {
		t.Fatalf("GQTR GEDI trace changed CBR packets or final ranges across %d frames", celtOnlyCBRFrames)
	}
	traceBody := tracedBytes[prefixBytes:]
	stageBytes, err := scanCELTVBRStageTrace(traceBody)
	if err != nil {
		t.Fatalf("scan selected-frame C CELT stage trace: %v", err)
	}
	cStage, err := parseCELTEncodeTrace(traceBody[:stageBytes])
	if err != nil {
		t.Fatalf("parse selected-frame C CELT stage trace: %v", err)
	}
	if cStage.TraceFrame != celtOnlyCBRQuantTraceFrame || cStage.Overflow != 0 {
		t.Fatalf("C CELT stage trace frame=%d overflow=%d, want frame %d with no overflow",
			cStage.TraceFrame, cStage.Overflow, celtOnlyCBRQuantTraceFrame)
	}
	if cStage.BandCalls == 0 || cStage.LogCalls == 0 || cStage.NormalizationCalls == 0 ||
		cStage.CoarseCalls == 0 || cStage.QuantCalls == 0 || cStage.PreemphasisCalls != celtOnlyCBRChannels || cStage.MDCTCalls == 0 {
		t.Fatalf("C stage trace lacks selected-frame CELT calls: %s", cStage.counts())
	}
	if len(cStage.Bands) == 0 || len(cStage.Normalizations) == 0 || len(cStage.Quant) == 0 {
		t.Fatalf("C stage trace has no band/normalization/quant dimensions: bands=%d normalizations=%d quant=%d",
			len(cStage.Bands), len(cStage.Normalizations), len(cStage.Quant))
	}
	for index, band := range cStage.Bands {
		if band.FrameCoeffs != celtOnlyCBRFrameSize || band.Bands != celtTraceBandCount || band.Channels != celtOnlyCBRChannels || band.LM != 3 {
			t.Fatalf("C band stage %d geometry=%d/%d/%d LM%d, want %d/%d/%d LM3",
				index, band.FrameCoeffs, band.Bands, band.Channels, band.LM,
				celtOnlyCBRFrameSize, celtTraceBandCount, celtOnlyCBRChannels)
		}
	}
	for index, normalization := range cStage.Normalizations {
		if normalization.ActiveCoeffs != 800 || normalization.Bands != celtTraceBandCount || normalization.Channels != celtOnlyCBRChannels {
			t.Fatalf("C normalization stage %d geometry=%d/%d/%d, want 800/%d/%d",
				index, normalization.ActiveCoeffs, normalization.Bands, normalization.Channels,
				celtTraceBandCount, celtOnlyCBRChannels)
		}
	}
	for index, quant := range cStage.Quant {
		if quant.ActiveCoeffs != 800 || quant.Bands != celtTraceBandCount || quant.Channels != celtOnlyCBRChannels {
			t.Fatalf("C quant stage %d geometry=%d/%d/%d, want 800/%d/%d",
				index, quant.ActiveCoeffs, quant.Bands, quant.Channels,
				celtTraceBandCount, celtOnlyCBRChannels)
		}
	}
	t.Logf("fixture=AMMultisineV1 frames=%d frame_size=%d channels=%d bitrate=%d complexity=10 max_data_bytes=%d target_frame=%d target_band=%d C_version=%q archmask=0x%x features=0x%x selected_arch=%d C_stage_calls={%s}",
		celtOnlyCBRFrames, celtOnlyCBRFrameSize, celtOnlyCBRChannels, celtOnlyCBRBitrate,
		celtOnlyCBRCapacity, celtOnlyCBRQuantTraceFrame, celtOnlyCBRQuantTraceBand,
		ordinary.LibopusVersion, ordinary.ArchMask, ordinary.BuildFeatures, ordinary.SelectedArch, cStage.counts())

	entropyData := traceBody[stageBytes:]
	entropyBytes, err := scanCELTVBREntropyTrace(entropyData)
	if err != nil {
		t.Fatalf("scan selected-frame C entropy trace: %v", err)
	}
	entropyTrace, err := parseCELTVBREntropyTrace(entropyData[:entropyBytes])
	if err != nil {
		t.Fatalf("parse selected-frame C entropy trace: %v", err)
	}
	if entropyTrace.Frame != celtOnlyCBRQuantTraceFrame || entropyTrace.Overflow != 0 ||
		entropyTrace.RawCalls == 0 || entropyTrace.DoneCalls == 0 {
		t.Fatalf("C entropy trace incomplete: %s", entropyTrace.summary())
	}
	rangeData := entropyData[entropyBytes:]
	coderRanges, rangeBytes, err := parseCELTCoderRangeTrace(rangeData, uint32(celtOnlyCBRQuantTraceFrame))
	if err != nil {
		t.Fatalf("parse selected-frame C coder-range trace: %v", err)
	}
	if coderRanges.Overflow != 0 || coderRanges.CoarseCalls != 1 || coderRanges.QuantCalls != 1 ||
		coderRanges.SameCoder != 1 || coderRanges.Count != 3 {
		t.Fatalf("C coder-range trace incomplete: frame=%d overflow=%d calls=%d/%d same_coder=%d points=%d",
			coderRanges.Frame, coderRanges.Overflow, coderRanges.CoarseCalls, coderRanges.QuantCalls,
			coderRanges.SameCoder, coderRanges.Count)
	}
	tfData := rangeData[rangeBytes:]
	cTF, tfBytes, err := parseCELTQuantityTFTrace(tfData, uint32(celtOnlyCBRQuantTraceFrame))
	if err != nil {
		t.Fatalf("parse selected-frame C TF encoder trace: %v", err)
	}
	if tfBytes > len(tfData) {
		t.Fatalf("GCTF consumed %d bytes from a %d-byte trailer", tfBytes, len(tfData))
	}
	gqtrData := tfData[tfBytes:]
	cQuant, gqtrBytes, err := parseCELTQuantBandTraceForTarget(gqtrData,
		uint32(celtOnlyCBRQuantTraceFrame), uint32(celtOnlyCBRQuantTraceBand))
	if err != nil {
		for _, event := range cQuant.Events {
			t.Logf("C GQTR event stage=%d ordinal=%d theta=%d band=%d n=%d B=%d B0=%d LM=%d stereo=%d round=%d",
				event.Stage, event.Ordinal, event.ThetaOrdinal, event.Band, event.N, event.B,
				event.B0, event.LM, event.Stereo, event.ThetaRound)
		}
		t.Fatalf("parse selected-frame/band C GQTR: %v", err)
	}
	if gqtrBytes != len(gqtrData) {
		t.Fatalf("GQTR has %d trailing bytes", len(gqtrData)-gqtrBytes)
	}
	if err := validateCELTOnlyCBRQuantBranchGeometry(cQuant.Events, celtOnlyCBRQuantTraceBand,
		celtOnlyCBRQuantTraceN, celtOnlyCBRQuantTraceLM, celtOnlyCBRQuantTraceDualStereoShape); err != nil {
		t.Fatalf("C selected-band quantization branch geometry: %v", err)
	}

	tracedGo := newCELTOnlyCBREncoder()
	plainGo := newCELTOnlyCBREncoder()
	tracedPackets := make([][]byte, 0, celtOnlyCBRFrames)
	tracedRanges := make([]uint32, 0, celtOnlyCBRFrames)
	plainPackets := make([][]byte, 0, celtOnlyCBRFrames)
	plainRanges := make([]uint32, 0, celtOnlyCBRFrames)
	goQuant := make([]celt.CELTQuantBandTraceSnapshot, 0, celtQuantTraceWireMaxEvents)
	var goCoderRanges []celt.EncodeCoderRangeTrace
	var goTF celt.TFEncodeTraceSnapshot
	var quantOverflow bool
	for frame := range celtOnlyCBRFrames {
		framePCM := pcm[frame*celtOnlyCBRFrameSize*celtOnlyCBRChannels : (frame+1)*celtOnlyCBRFrameSize*celtOnlyCBRChannels]
		var packet []byte
		var encodeErr error
		encodeTraced := func() {
			packet, encodeErr = tracedGo.EncodeFloat32WithAnalysisMaxBytes(framePCM,
				celtOnlyCBRFrameSize, framePCM, celtOnlyCBRCapacity)
		}
		if frame == celtOnlyCBRQuantTraceFrame {
			if tracedGo.celtEncoder == nil {
				// Frame 0 creates the CELT core inside its first public encode call.
				// Initialize that same core before the call so the test can arm its
				// trace without consuming or changing a frame.
				tracedGo.ensureCELTEncoder()
			}
			if tracedGo.celtEncoder == nil {
				t.Fatal("Go CELT encoder is not initialized at the selected frame")
			}
			tracedGo.celtEncoder.EnableEncodeStageTraceForTesting()
			celt.EnableTFEncodeTraceForTesting()
			quantOverflow = celt.WithCELTQuantBandTraceHookForTesting(celtOnlyCBRQuantTraceBand,
				func(event *celt.CELTQuantBandTraceSnapshot) { goQuant = append(goQuant, *event) }, encodeTraced)
			goTF = celt.TFEncodeTraceForTesting()
			celt.DisableTFEncodeTraceForTesting()
			goStageTrace := tracedGo.celtEncoder.EncodeStageTraceForTesting()
			if goStageTrace.StageOverflow {
				t.Fatal("Go coder-range trace exceeded its bounded frame capture")
			}
			goCoderRanges = append(goCoderRanges, goStageTrace.CoderRanges...)
			tracedGo.celtEncoder.DisableEncodeStageTraceForTesting()
		} else {
			encodeTraced()
		}
		if quantOverflow {
			t.Fatal("Go quant-band trace exceeded its bounded capture")
		}
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
	}
	if !sameCBRStream(tracedPackets, tracedRanges, plainPackets, plainRanges) {
		t.Fatal("Go quant-band tracing changed one or more packets or final ranges in the 50-frame stream")
	}
	if len(goCoderRanges) != 3 {
		t.Fatalf("Go coder-range trace has %d points, want 3", len(goCoderRanges))
	}
	if goCoderRanges[0].Coder == nil || goCoderRanges[0].Coder != goCoderRanges[1].Coder ||
		goCoderRanges[0].Coder != goCoderRanges[2].Coder {
		t.Fatal("Go coder-range points do not refer to one live range encoder")
	}
	for i, point := range goCoderRanges {
		if point.Stage != int32(i+1) {
			t.Fatalf("Go coder-range point %d stage=%d, want %d", i, point.Stage, i+1)
		}
		if point.Range == 0 || coderRanges.Points[i].Range == 0 {
			t.Fatalf("coder-range point %d has zero Go/C range: 0x%08x/0x%08x",
				i, point.Range, coderRanges.Points[i].Range)
		}
	}
	for i, got := range goCoderRanges {
		wantStage := int32(i + 1)
		want := coderRanges.Points[i]
		if got.Stage != wantStage || want.Stage != uint32(wantStage) {
			t.Fatalf("coder-range point %d stage Go=%d C=%d, want %d", i, got.Stage, want.Stage, wantStage)
		}
		if got.Range != want.Range || uint32(got.TellFrac) != want.TellFrac {
			t.Logf("first coder-range difference: stage=%d Go range/tell=0x%08x/%d C=0x%08x/%d",
				got.Stage, got.Range, got.TellFrac, want.Range, want.TellFrac)
			break
		}
		if i == len(goCoderRanges)-1 {
			t.Logf("coder range/tell matches at all %d source-bound boundaries", len(goCoderRanges))
		}
	}
	if len(tracedPackets[celtOnlyCBRQuantTraceFrame]) == 0 || tracedPackets[celtOnlyCBRQuantTraceFrame][0]>>3 != 31 {
		t.Fatalf("Go selected frame %d is not a CELT-only packet", celtOnlyCBRQuantTraceFrame)
	}
	if len(goQuant) == 0 {
		t.Fatalf("Go captured no quant events for frame %d band %d", celtOnlyCBRQuantTraceFrame, celtOnlyCBRQuantTraceBand)
	}
	if err := validateGoCELTQuantityTFTrace(goTF); err != nil {
		t.Fatalf("invalid Go selected-frame TF encoder trace: %v", err)
	}
	if len(goCoderRanges) != 3 || goTF.Coder != goCoderRanges[0].Coder ||
		goCoderRanges[0].Coder != goCoderRanges[2].Coder {
		t.Fatalf("Go TF trace coder identity differs from the coarse/quant range coder: TF=%p ranges=%v",
			goTF.Coder, goCoderRanges)
	}
	t.Logf("C spread ICDF symbol=%d logp=%d range/tell-frac=%08x/%d→%08x/%d",
		cTF.SpreadSymbol, cTF.SpreadLogP, cTF.SpreadRangeBefore, cTF.SpreadTellFracBefore,
		cTF.SpreadRangeAfter, cTF.SpreadTellFracAfter)
	if len(goTF.Bits) == 0 || len(cTF.Bits) == 0 {
		t.Fatal("selected-frame TF trace has no actual entropy calls")
	}
	goAfterCoarse := goCoderRanges[1]
	if first := goTF.Bits[0]; first.RangeBefore != goAfterCoarse.Range || first.TellFracBefore != goAfterCoarse.TellFrac {
		t.Fatalf("Go first TF call is not immediately after coarse energy: TF range/tell-frac=%08x/%d, GCRG after-coarse=%08x/%d",
			first.RangeBefore, first.TellFracBefore, goAfterCoarse.Range, goAfterCoarse.TellFrac)
	}
	cAfterCoarse := coderRanges.Points[1]
	if first := cTF.Bits[0]; first.RangeBefore != cAfterCoarse.Range || first.TellFracBefore != cAfterCoarse.TellFrac {
		t.Fatalf("C first TF call is not immediately after coarse energy: TF range/tell-frac=%08x/%d, GCRG after-coarse=%08x/%d",
			first.RangeBefore, first.TellFracBefore, cAfterCoarse.Range, cAfterCoarse.TellFrac)
	}
	if difference := compareCELTQuantityTFTrace(goTF, cTF); difference != "" {
		t.Logf("first TF encoder difference: %s", difference)
	} else {
		t.Logf("selected frame %d TF encode: all %d actual TF entropy calls and raw/effective flags match; coder state before spread ICDF matches bit-for-bit",
			celtOnlyCBRQuantTraceFrame, len(goTF.Bits))
	}
	if err := validateCELTQuantBandTraceEventsForBand(goQuant, uint32(celtOnlyCBRQuantTraceBand)); err != nil {
		t.Fatalf("invalid Go selected-band quant trace: %v", err)
	}
	if err := validateCELTOnlyCBRQuantBranchGeometry(goQuant, celtOnlyCBRQuantTraceBand,
		celtOnlyCBRQuantTraceN, celtOnlyCBRQuantTraceLM, celtOnlyCBRQuantTraceDualStereoShape); err != nil {
		t.Fatalf("Go selected-band quantization branch geometry: %v", err)
	}
	if difference := compareCELTQuantBandTraceForTarget(goQuant, cQuant,
		uint32(celtOnlyCBRQuantTraceFrame), uint32(celtOnlyCBRQuantTraceBand)); difference != "" {
		t.Logf("first selected-band quant-stage difference: %s", difference)
	} else {
		t.Logf("selected frame %d band %d quant trace: all %d events match bit-for-bit",
			celtOnlyCBRQuantTraceFrame, celtOnlyCBRQuantTraceBand, len(goQuant))
	}
	for _, difference := range compareCELTQuantBandTracePayloads(goQuant, cQuant) {
		t.Logf("selected-band quant payload: %s", difference)
	}
	packetDiff := firstCELTTraceByteDifference(tracedPackets[celtOnlyCBRQuantTraceFrame], ordinary.Packets[celtOnlyCBRQuantTraceFrame])
	t.Logf("selected frame %d packet byte diff=%d Go range=0x%08x C range=0x%08x; all %d traced GEDI packets/ranges match ordinary GCBR",
		celtOnlyCBRQuantTraceFrame, packetDiff, tracedRanges[celtOnlyCBRQuantTraceFrame],
		ordinary.FinalRanges[celtOnlyCBRQuantTraceFrame], celtOnlyCBRFrames)
}

func validateCELTOnlyCBRQuantBranchGeometry(events []celt.CELTQuantBandTraceSnapshot, band, n, lm int, dualStereo bool) error {
	if dualStereo {
		// GQTR records the quantization event graph, not quant_all_bands' dual_stereo input.
		// The source-observed separate-channel path has recursive mono theta/PVQ events only.
		thetaCount, pvqCount := 0, 0
		for _, event := range events {
			switch event.Stage {
			case uint32(celt.CELTQuantBandTraceTheta):
				thetaCount++
				if event.Band != uint32(band) || event.Stereo != 0 || event.N != 4 || event.LM != 2 || event.ThetaRound != 0 {
					return fmt.Errorf("dual-stereo recursive theta band=%d stereo=%d N=%d LM=%d round=%d, want band=%d stereo=0 N=4 LM=2 round=0",
						event.Band, event.Stereo, event.N, event.LM, event.ThetaRound, band)
				}
			case uint32(celt.CELTQuantBandTracePVQ):
				pvqCount++
				if event.Band != uint32(band) || event.Stereo != 0 || event.N != 4 || event.LM != 2 {
					return fmt.Errorf("dual-stereo recursive PVQ band=%d stereo=%d N=%d LM=%d, want band=%d stereo=0 N=4 LM=2",
						event.Band, event.Stereo, event.N, event.LM, band)
				}
			case uint32(celt.CELTQuantBandTraceStereoMerge), uint32(celt.CELTQuantBandTraceBandOutput), uint32(celt.CELTQuantBandTraceRDOSelect):
				return fmt.Errorf("dual-stereo band %d emitted unexpected stereo stage %d", band, event.Stage)
			default:
				return fmt.Errorf("dual-stereo band %d emitted unknown stage %d", band, event.Stage)
			}
		}
		if thetaCount != 2 || pvqCount != 4 || len(events) != thetaCount+pvqCount {
			return fmt.Errorf("dual-stereo band %d has %d recursive theta and %d PVQ events, want 2 and 4",
				band, thetaCount, pvqCount)
		}
		return nil
	}
	topLevel := 0
	for _, event := range events {
		if event.Stage != uint32(celt.CELTQuantBandTraceTheta) || event.Stereo != 1 {
			continue
		}
		topLevel++
		if event.Band != uint32(band) || event.N != uint32(n) || event.LM != int32(lm) {
			return fmt.Errorf("top-level theta band=%d N=%d LM=%d, want band=%d N=%d LM=%d",
				event.Band, event.N, event.LM, band, n, lm)
		}
	}
	if topLevel == 0 {
		return fmt.Errorf("no top-level stereo theta for band %d", band)
	}
	return nil
}

type celtCoderRangePoint struct {
	Stage    uint32
	Range    uint32
	TellFrac uint32
}

type celtCoderRangeTrace struct {
	Version     uint32
	Frame       uint32
	Overflow    uint32
	CoarseCalls uint32
	QuantCalls  uint32
	SameCoder   uint32
	Count       int
	Points      [3]celtCoderRangePoint
}

func parseCELTCoderRangeTrace(data []byte, expectedFrame uint32) (celtCoderRangeTrace, int, error) {
	var trace celtCoderRangeTrace
	const headerBytes = 32
	if len(data) < headerBytes || string(data[:4]) != "GCRG" {
		return trace, 0, fmt.Errorf("missing or truncated GCRG header")
	}
	words := [7]uint32{}
	for i := range words {
		words[i] = binary.LittleEndian.Uint32(data[4+i*4:])
	}
	trace.Version = words[0]
	trace.Frame = words[1]
	trace.Overflow = words[2]
	trace.CoarseCalls = words[3]
	trace.QuantCalls = words[4]
	trace.SameCoder = words[5]
	count := words[6]
	if trace.Version != 1 {
		return trace, 0, fmt.Errorf("GCRG version=%d, want 1", trace.Version)
	}
	if trace.Frame != expectedFrame {
		return trace, 0, fmt.Errorf("GCRG frame=%d, want %d", trace.Frame, expectedFrame)
	}
	if count > uint32(len(trace.Points)) {
		return trace, 0, fmt.Errorf("GCRG point count=%d exceeds bound %d", count, len(trace.Points))
	}
	need := headerBytes + int(count)*12
	if len(data) < need {
		return trace, 0, fmt.Errorf("truncated GCRG points: have %d bytes, need %d", len(data), need)
	}
	trace.Count = int(count)
	off := headerBytes
	for i := 0; i < trace.Count; i++ {
		trace.Points[i] = celtCoderRangePoint{
			Stage:    binary.LittleEndian.Uint32(data[off:]),
			Range:    binary.LittleEndian.Uint32(data[off+4:]),
			TellFrac: binary.LittleEndian.Uint32(data[off+8:]),
		}
		off += 12
	}
	if err := validateCELTCoderRangeTrace(trace); err != nil {
		return trace, 0, err
	}
	return trace, need, nil
}

func validateCELTCoderRangeTrace(trace celtCoderRangeTrace) error {
	if trace.Overflow != 0 || trace.CoarseCalls != 1 || trace.QuantCalls != 1 ||
		trace.SameCoder != 1 || trace.Count != len(trace.Points) {
		return fmt.Errorf("incomplete GCRG capture: overflow=%d coarse_calls=%d quant_calls=%d same_coder=%d points=%d",
			trace.Overflow, trace.CoarseCalls, trace.QuantCalls, trace.SameCoder, trace.Count)
	}
	for i, point := range trace.Points {
		if point.Stage != uint32(i+1) {
			return fmt.Errorf("GCRG point %d has stage=%d, want %d", i, point.Stage, i+1)
		}
		if point.Range == 0 {
			return fmt.Errorf("GCRG point %d has zero coder range", i)
		}
	}
	return nil
}

func TestCELTCoderRangeTraceRejectsMalformed(t *testing.T) {
	valid := make([]byte, 32+3*12)
	copy(valid[:4], "GCRG")
	for index, value := range []uint32{1, 25, 0, 1, 1, 1, 3} {
		binary.LittleEndian.PutUint32(valid[4+index*4:], value)
	}
	for i := range 3 {
		off := 32 + i*12
		binary.LittleEndian.PutUint32(valid[off:], uint32(i+1))
		binary.LittleEndian.PutUint32(valid[off+4:], uint32(0x1000+i))
		binary.LittleEndian.PutUint32(valid[off+8:], uint32(200+i))
	}
	if got, consumed, err := parseCELTCoderRangeTrace(valid, 25); err != nil || consumed != len(valid) ||
		got.Count != 3 || got.Points[2].Stage != 3 || got.Points[2].Range != 0x1002 || got.Points[2].TellFrac != 202 {
		t.Fatalf("valid GCRG rejected or decoded incorrectly: trace=%+v consumed=%d err=%v", got, consumed, err)
	}

	badMagic := append([]byte(nil), valid...)
	copy(badMagic[:4], "XCRG")
	badVersion := append([]byte(nil), valid...)
	binary.LittleEndian.PutUint32(badVersion[4:], 2)
	badFrame := append([]byte(nil), valid...)
	binary.LittleEndian.PutUint32(badFrame[8:], 24)
	tooManyPoints := append([]byte(nil), valid...)
	binary.LittleEndian.PutUint32(tooManyPoints[28:], 4)
	fewPoints := append([]byte(nil), valid...)
	binary.LittleEndian.PutUint32(fewPoints[28:], 2)
	withOverflow := append([]byte(nil), valid...)
	binary.LittleEndian.PutUint32(withOverflow[12:], 1)
	missingCoarseCall := append([]byte(nil), valid...)
	binary.LittleEndian.PutUint32(missingCoarseCall[16:], 0)
	duplicateQuantCall := append([]byte(nil), valid...)
	binary.LittleEndian.PutUint32(duplicateQuantCall[20:], 2)
	missingIdentity := append([]byte(nil), valid...)
	binary.LittleEndian.PutUint32(missingIdentity[24:], 0)
	zeroRange := append([]byte(nil), valid...)
	binary.LittleEndian.PutUint32(zeroRange[36:], 0)
	badStage := append([]byte(nil), valid...)
	binary.LittleEndian.PutUint32(badStage[32:], 4)
	outOfOrder := append([]byte(nil), valid...)
	binary.LittleEndian.PutUint32(outOfOrder[44:], 3)
	truncatedPoint := append([]byte(nil), valid[:len(valid)-1]...)
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{name: "truncated header", data: valid[:31]},
		{name: "bad magic", data: badMagic},
		{name: "unsupported version", data: badVersion},
		{name: "wrong selected frame", data: badFrame},
		{name: "count exceeds bound", data: tooManyPoints},
		{name: "wrong point count", data: fewPoints},
		{name: "reported overflow", data: withOverflow},
		{name: "missing coarse call", data: missingCoarseCall},
		{name: "duplicate quant call", data: duplicateQuantCall},
		{name: "missing coder identity", data: missingIdentity},
		{name: "zero coder range", data: zeroRange},
		{name: "invalid stage", data: badStage},
		{name: "out of order stages", data: outOfOrder},
		{name: "truncated final point", data: truncatedPoint},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := parseCELTCoderRangeTrace(tc.data, 25); err == nil {
				t.Fatal("malformed GCRG record was accepted")
			}
		})
	}
}

func celtOnlyCBRGEDIInput(pcm []float32) []byte {
	params := libopustest.EncodeDiffParams{
		SampleRate: 48000, Channels: celtOnlyCBRChannels,
		Application: libopustest.OpusApplicationAudio,
		ForceMode:   libopustest.EncodeDiffForceModeAuto,
		Bandwidth:   celtOnlyCBRFullband, Bitrate: celtOnlyCBRBitrate,
		Complexity: 10, Signal: libopustest.EncodeDiffSignalAuto,
		FrameSize: celtOnlyCBRFrameSize, FrameCount: celtOnlyCBRFrames, PCM: pcm,
	}
	payload := libopustest.NewOraclePayloadVersion("GEDI", 1)
	values := [...]uint32{
		uint32(params.SampleRate), uint32(params.Channels), uint32(params.Application),
		uint32(params.ForceMode), uint32(params.Bandwidth), uint32(params.MaxBandwidth),
		uint32(params.Bitrate), uint32(params.Complexity), params.Signal,
		0, 0, uint32(params.ForceChannels), uint32(params.InbandFEC), uint32(params.PacketLoss),
		0, uint32(params.LSBDepth), 0, 0,
		uint32(params.FrameSize), uint32(params.FrameCount), uint32(len(params.PCM)),
	}
	payload.U32s(values[:]...)
	payload.Float32s(params.PCM...)
	return payload.Bytes()
}

func parseCELTQuantTraceGEDOPrefix(data []byte, expectedFrames int) ([]libopustest.EncodeDiffRecord, int, error) {
	if len(data) < 12 || string(data[:4]) != "GEDO" || binary.LittleEndian.Uint32(data[4:8]) != 1 {
		return nil, 0, fmt.Errorf("invalid GEDO v1 prefix")
	}
	count := int(binary.LittleEndian.Uint32(data[8:12]))
	if expectedFrames <= 0 || count != expectedFrames || count > 4096 {
		return nil, 0, fmt.Errorf("GEDO record count=%d, want %d", count, expectedFrames)
	}
	off := 12
	records := make([]libopustest.EncodeDiffRecord, count)
	for frame := range count {
		if off+12 > len(data) {
			return nil, 0, fmt.Errorf("truncated GEDO record %d", frame)
		}
		ret := int(int32(binary.LittleEndian.Uint32(data[off:])))
		finalRange := binary.LittleEndian.Uint32(data[off+4:])
		packetLen := uint64(binary.LittleEndian.Uint32(data[off+8:]))
		off += 12
		if packetLen > uint64(len(data)-off) {
			return nil, 0, fmt.Errorf("truncated GEDO packet %d length %d", frame, packetLen)
		}
		packet := append([]byte(nil), data[off:off+int(packetLen)]...)
		off += int(packetLen)
		padding := (4 - len(packet)%4) % 4
		if off+padding > len(data) {
			return nil, 0, fmt.Errorf("truncated GEDO packet %d padding", frame)
		}
		for _, pad := range data[off : off+padding] {
			if pad != 0 {
				return nil, 0, fmt.Errorf("nonzero GEDO packet %d padding", frame)
			}
		}
		off += padding
		if (ret > 0 && ret != len(packet)) || (ret <= 0 && len(packet) != 0) {
			return nil, 0, fmt.Errorf("GEDO record %d ret=%d packet length=%d", frame, ret, packetLen)
		}
		records[frame] = libopustest.EncodeDiffRecord{Ret: ret, FinalRange: finalRange, Packet: packet}
	}
	if off+4 > len(data) || string(data[off:off+4]) != "GCET" {
		return nil, 0, fmt.Errorf("missing GCET section after GEDO at offset %d", off)
	}
	return records, off, nil
}

func celtOnlyCBRRecords(output celtTraceCBROutput) []libopustest.EncodeDiffRecord {
	records := make([]libopustest.EncodeDiffRecord, len(output.Packets))
	for frame, packet := range output.Packets {
		records[frame] = libopustest.EncodeDiffRecord{
			Ret: len(packet), FinalRange: output.FinalRanges[frame], Packet: append([]byte(nil), packet...),
		}
	}
	return records
}

func containsPinnedLibopus(version string) bool {
	return len(version) != 0 && strings.Contains(version, libopustooling.DefaultVersion)
}

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

func buildCELTQuantTraceOracleAtFrameBand(t *testing.T, frame, band int, coderRangeTrace bool, cache *libopustest.HelperCache) string {
	return buildCELTQuantTraceOracleAtFrameBandOptions(t, frame, band, coderRangeTrace, false, cache)
}

// buildCELTQuantTraceOracleAtFrameBandWithTFTrace adds the bounded selected
// frame TF bit trace to the ordinary GCRG/GQTR helper output.
func buildCELTQuantTraceOracleAtFrameBandWithTFTrace(t *testing.T, frame, band int, cache *libopustest.HelperCache) string {
	return buildCELTQuantTraceOracleAtFrameBandOptions(t, frame, band, true, true, cache)
}

func buildCELTQuantTraceOracleAtFrameBandOptions(t *testing.T, frame, band int, coderRangeTrace, tfTrace bool, cache *libopustest.HelperCache) string {
	t.Helper()
	libopustest.RequireOracle(t)
	if frame < 0 || frame >= celtOnlyCBRFrames {
		t.Fatalf("CELT quant trace frame %d outside selected CBR stream [0,%d)", frame, celtOnlyCBRFrames)
	}
	if band < 0 || band >= celtTraceBandCount {
		t.Fatalf("CELT quant trace band %d outside [0,%d)", band, celtTraceBandCount)
	}
	if cache == nil {
		t.Fatal("CELT quant trace helper requires an explicit selector-scoped cache")
	}
	if tfTrace && !coderRangeTrace {
		t.Fatal("CELT TF trace requires the same-coder range checkpoints")
	}

	path, err := cache.Path(func() (string, error) {
		root := celtQuantTraceRepoRoot(t)
		quantSource, entropyIncludeDir, entropySourceHash := writeCELTQuantTraceTargetSources(t, root, frame, band)
		patchedBands := writeCELTQuantTraceBandsSource(t, libopustest.RefPath("celt", "bands.c"))
		preemphasisSource := writeCELTPreemphasisTraceSource(t)

		bandsSourceHash, err := hashCELTTraceSource(patchedBands)
		if err != nil {
			return "", fmt.Errorf("hash generated CELT bands trace source: %w", err)
		}
		preemphasisSourceHash, err := hashCELTTraceSource(preemphasisSource)
		if err != nil {
			return "", fmt.Errorf("hash generated CELT preemphasis trace source: %w", err)
		}

		outputBase := fmt.Sprintf("gopus_libopus_celt_quant_trace_f%d_b%d", frame, band)
		if tfTrace {
			outputBase += "_tf"
		}
		linkMapPath := filepath.Join(t.TempDir(), outputBase+".map")
		config := libopustest.CHelperConfig{
			Label:      fmt.Sprintf("CELT quant trace frame %d band %d", frame, band),
			OutputBase: outputBase,
			SourceFile: quantSource,
			CFlags: []string{
				"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG",
				fmt.Sprintf("-DGOPUS_CELT_QUANT_TRACE_FRAME=%d", frame),
				fmt.Sprintf("-DGOPUS_CELT_QUANT_TRACE_BAND=%d", band),
				fmt.Sprintf("-DGOPUS_CELT_ENTROPY_SOURCE_SHA256=%q", entropySourceHash),
				fmt.Sprintf("-DGOPUS_CELT_BANDS_TRACE_SOURCE_SHA256=%q", bandsSourceHash),
				fmt.Sprintf("-DGOPUS_CELT_PREEMPHASIS_TRACE_SOURCE_SHA256=%q", preemphasisSourceHash),
			},
			RefIncludes: []string{"celt", "silk", "src"},
			IncludeDirs: []string{entropyIncludeDir, filepath.Join(root, "tools", "csrc")},
			Sources:     []string{preemphasisSource, patchedBands},
			LDFlags: []string{
				"-Wl,--wrap=opus_encode_float",
				"-Wl,--wrap=comb_filter",
				"-Wl,--wrap=compute_band_energies",
				"-Wl,--wrap=amp2Log2",
				"-Wl,--wrap=normalise_bands",
				"-Wl,--wrap=quant_coarse_energy",
				"-Wl,--wrap=quant_all_bands",
				"-Wl,--wrap=clt_mdct_forward_c",
				"-Wl,--wrap=ec_enc_bits",
				"-Wl,--wrap=ec_enc_done",
				"-Wl,--wrap=alg_quant",
				"-Wl,-Map," + linkMapPath,
			},
		}
		if coderRangeTrace {
			config.CFlags = append(config.CFlags, "-DGOPUS_CELT_CODER_RANGE_TRACE")
		}
		if tfTrace {
			config.CFlags = append(config.CFlags, "-DGOPUS_CELT_TF_TRACE")
			config.LDFlags = append(config.LDFlags,
				"-Wl,--wrap=ec_enc_bit_logp",
				"-Wl,--wrap=ec_enc_icdf",
			)
		}
		helperPath, err := libopustest.BuildPublicAPIHelper(config)
		if err != nil {
			return "", err
		}
		if err := validateCELTTraceLinkMap(linkMapPath); err != nil {
			return "", err
		}
		if err := validateCELTQuantTraceLinkMap(linkMapPath); err != nil {
			return "", err
		}
		return helperPath, nil
	})
	if err != nil {
		libopustest.HelperUnavailable(t, fmt.Sprintf("CELT quant trace frame %d band %d", frame, band), err)
	}
	return path
}

func writeCELTQuantTraceTargetSources(t *testing.T, root string, frame, band int) (quantSource, entropyIncludeDir, entropySourceHash string) {
	t.Helper()
	quantPath := filepath.Join(root, "tools", "csrc", "libopus_encode_diff_celt_quant_trace.c")
	quantBytes, err := os.ReadFile(quantPath)
	if err != nil {
		t.Fatalf("read CELT quant trace source %s: %v", quantPath, err)
	}
	quantText := string(quantBytes)
	for _, replacement := range []struct {
		from string
		to   string
	}{
		{"if (band != 17) return -1;", "if (band != GOPUS_CELT_QUANT_TRACE_BAND) return -1;"},
		{"gqtr.events[previous_theta].header.band != 17", "gqtr.events[previous_theta].header.band != GOPUS_CELT_QUANT_TRACE_BAND"},
		{"gqtr.events[gqtr.current_theta].header.band != 17", "gqtr.events[gqtr.current_theta].header.band != GOPUS_CELT_QUANT_TRACE_BAND"},
		{"gqtr.events[theta_ordinal].header.band == 17", "gqtr.events[theta_ordinal].header.band == GOPUS_CELT_QUANT_TRACE_BAND"},
		{"!trace_selected_frame() || band != 17 || selected_theta_ordinal < 0 ||", "!trace_selected_frame() || band != GOPUS_CELT_QUANT_TRACE_BAND || selected_theta_ordinal < 0 ||"},
	} {
		quantText = replaceCELTQuantTraceSource(t, quantText, replacement.from, replacement.to)
	}
	quantDir := t.TempDir()
	quantSource = filepath.Join(quantDir, "libopus_encode_diff_celt_quant_trace.c")
	if err := os.WriteFile(quantSource, []byte(quantText), 0o600); err != nil {
		t.Fatalf("write selected-frame CELT quant trace source: %v", err)
	}

	entropyPath := filepath.Join(root, "tools", "csrc", "libopus_encode_diff_celt_entropy_trace.c")
	entropyBytes, err := os.ReadFile(entropyPath)
	if err != nil {
		t.Fatalf("read CELT entropy trace source %s: %v", entropyPath, err)
	}
	const originalMain = "int main(void) {\n  int result = gopus_encode_diff_main();"
	const renamedMain = "int gopus_encode_diff_celt_entropy_trace_main(void) {\n  int result = gopus_encode_diff_main();"
	const originalFrame = "#define TRACE_FRAME 1u"
	const selectedFrame = "#define TRACE_FRAME GOPUS_CELT_QUANT_TRACE_FRAME"
	entropyText := replaceCELTQuantTraceSource(t, string(entropyBytes), originalMain, renamedMain)
	entropyText = replaceCELTQuantTraceSource(t, entropyText, originalFrame, selectedFrame)
	entropyIncludeDir = t.TempDir()
	entropySource := filepath.Join(entropyIncludeDir, "libopus_encode_diff_celt_entropy_trace_included.c")
	if err := os.WriteFile(entropySource, []byte(entropyText), 0o600); err != nil {
		t.Fatalf("write selected-frame CELT entropy trace source: %v", err)
	}
	entropyHash := sha256.Sum256([]byte(entropyText))
	entropySourceHash = fmt.Sprintf("%x", entropyHash[:])
	return quantSource, entropyIncludeDir, entropySourceHash
}

func hashCELTTraceSource(path string) (string, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return "", err
	}
	hash := sha256.Sum256(data)
	return fmt.Sprintf("%x", hash[:]), nil
}

const (
	celtTFTraceMaxBits  = 64
	celtTFTraceMaxBands = 21
	celtTFTraceHeader   = 112
)

type celtTFEncodeBit struct {
	Ordinal        uint32
	Symbol         int32
	LogP           uint32
	RangeBefore    uint32
	TellFracBefore uint32
	TellBefore     int32
	RangeAfter     uint32
	TellFracAfter  uint32
	TellAfter      int32
}

type celtTFEncodeTrace struct {
	Version              uint32
	Frame                uint32
	Overflow             uint32
	CoarseCalls          uint32
	BitCalls             uint32
	StoredBits           uint32
	ForeignCalls         uint32
	ICDFCalls            uint32
	SpreadCalls          uint32
	SpreadTableMatch     uint32
	SameCoder            uint32
	QuantCalls           uint32
	Start                int32
	End                  int32
	LM                   int32
	EntryTell            int32
	StorageBits          uint32
	Transient            uint32
	SelectEncoded        uint32
	SelectSymbol         int32
	PostTFRes            []int32
	SpreadSymbol         int32
	SpreadLogP           uint32
	SpreadRangeBefore    uint32
	SpreadTellFracBefore uint32
	SpreadRangeAfter     uint32
	SpreadTellFracAfter  uint32
	Bits                 []celtTFEncodeBit
}

// This copy of libopus celt.c tf_select_table keeps the trace oracle
// independent from the Go implementation table.
var celtTFTraceSelectTable = [4][8]int32{
	{0, -1, 0, -1, 0, -1, 0, -1},
	{0, -1, 0, -2, 1, 0, 1, -1},
	{0, -2, 0, -3, 2, 0, 1, -1},
	{0, -2, 0, -3, 3, 0, 1, -1},
}

func parseCELTQuantityTFTrace(data []byte, expectedFrame uint32) (celtTFEncodeTrace, int, error) {
	var trace celtTFEncodeTrace
	if len(data) < celtTFTraceHeader || string(data[:4]) != "GCTF" {
		return trace, 0, fmt.Errorf("missing or truncated GCTF header")
	}
	words := [27]uint32{}
	for i := range words {
		words[i] = binary.LittleEndian.Uint32(data[4+i*4:])
	}
	trace.Version = words[0]
	trace.Frame = words[1]
	trace.Overflow = words[2]
	trace.CoarseCalls = words[3]
	trace.BitCalls = words[4]
	trace.StoredBits = words[5]
	trace.ForeignCalls = words[6]
	trace.ICDFCalls = words[7]
	trace.SpreadCalls = words[8]
	trace.SpreadTableMatch = words[9]
	trace.SameCoder = words[10]
	trace.QuantCalls = words[11]
	trace.Start = int32(words[12])
	trace.End = int32(words[13])
	trace.LM = int32(words[14])
	trace.EntryTell = int32(words[15])
	trace.StorageBits = words[16]
	trace.Transient = words[17]
	trace.SelectEncoded = words[18]
	trace.SelectSymbol = int32(words[19])
	postCount := words[20]
	trace.SpreadSymbol = int32(words[21])
	trace.SpreadLogP = words[22]
	trace.SpreadRangeBefore = words[23]
	trace.SpreadTellFracBefore = words[24]
	trace.SpreadRangeAfter = words[25]
	trace.SpreadTellFracAfter = words[26]
	if trace.Version != 1 {
		return trace, 0, fmt.Errorf("unsupported GCTF version %d", trace.Version)
	}
	if trace.Frame != expectedFrame {
		return trace, 0, fmt.Errorf("GCTF frame=%d, want %d", trace.Frame, expectedFrame)
	}
	if trace.Overflow != 0 || trace.CoarseCalls != 1 || trace.ForeignCalls != 0 ||
		trace.ICDFCalls != 1 || trace.SpreadCalls != 1 || trace.SpreadTableMatch != 1 ||
		trace.SameCoder != 1 || trace.QuantCalls != 1 {
		return trace, 0, fmt.Errorf("incomplete GCTF capture: overflow=%d coarse=%d foreign=%d icdf=%d spread=%d table=%d same_coder=%d quant=%d",
			trace.Overflow, trace.CoarseCalls, trace.ForeignCalls, trace.ICDFCalls, trace.SpreadCalls,
			trace.SpreadTableMatch, trace.SameCoder, trace.QuantCalls)
	}
	if trace.Start < 0 || trace.End <= trace.Start || trace.End > celtTFTraceMaxBands ||
		trace.LM < 0 || trace.LM > 3 || trace.Transient > 1 || trace.SelectEncoded > 1 ||
		postCount != uint32(trace.End-trace.Start) || trace.SelectSymbol < 0 || trace.SelectSymbol > 1 ||
		trace.EntryTell < 0 || trace.StorageBits == 0 || uint32(trace.EntryTell) > trace.StorageBits {
		return trace, 0, fmt.Errorf("invalid GCTF TF geometry/state: start=%d end=%d LM=%d transient=%d select_encoded=%d select_symbol=%d post=%d entry_tell=%d storage_bits=%d",
			trace.Start, trace.End, trace.LM, trace.Transient, trace.SelectEncoded, trace.SelectSymbol, postCount, trace.EntryTell, trace.StorageBits)
	}
	if trace.SpreadSymbol < 0 || trace.SpreadSymbol > 3 || trace.SpreadLogP != 5 ||
		trace.SpreadRangeBefore == 0 || trace.SpreadRangeAfter == 0 ||
		uint64(trace.SpreadTellFracBefore) > uint64(trace.StorageBits)*8+8 ||
		uint64(trace.SpreadTellFracAfter) > uint64(trace.StorageBits)*8+8 ||
		trace.SpreadTellFracAfter < trace.SpreadTellFracBefore {
		return trace, 0, fmt.Errorf("invalid GCTF spread close: symbol=%d logp=%d range=%08x/%08x",
			trace.SpreadSymbol, trace.SpreadLogP, trace.SpreadRangeBefore, trace.SpreadRangeAfter)
	}
	if trace.BitCalls != trace.StoredBits || trace.StoredBits > celtTFTraceMaxBits {
		return trace, 0, fmt.Errorf("GCTF TF calls=%d stored=%d, bound=%d", trace.BitCalls, trace.StoredBits, celtTFTraceMaxBits)
	}
	postBytes := int(postCount) * 4
	bitBytes := int(trace.StoredBits) * 36
	need := celtTFTraceHeader + postBytes + bitBytes
	if len(data) < need {
		return trace, 0, fmt.Errorf("truncated GCTF records: have %d bytes, need %d", len(data), need)
	}
	trace.PostTFRes = make([]int32, int(postCount))
	off := celtTFTraceHeader
	for i := range trace.PostTFRes {
		trace.PostTFRes[i] = int32(binary.LittleEndian.Uint32(data[off:]))
		off += 4
	}
	trace.Bits = make([]celtTFEncodeBit, int(trace.StoredBits))
	for i := range trace.Bits {
		bit := &trace.Bits[i]
		bit.Ordinal = binary.LittleEndian.Uint32(data[off:])
		bit.Symbol = int32(binary.LittleEndian.Uint32(data[off+4:]))
		bit.LogP = binary.LittleEndian.Uint32(data[off+8:])
		bit.RangeBefore = binary.LittleEndian.Uint32(data[off+12:])
		bit.TellFracBefore = binary.LittleEndian.Uint32(data[off+16:])
		bit.TellBefore = int32(binary.LittleEndian.Uint32(data[off+20:]))
		bit.RangeAfter = binary.LittleEndian.Uint32(data[off+24:])
		bit.TellFracAfter = binary.LittleEndian.Uint32(data[off+28:])
		bit.TellAfter = int32(binary.LittleEndian.Uint32(data[off+32:]))
		off += 36
	}
	if err := validateCELTQuantityTFTrace(trace); err != nil {
		return trace, 0, err
	}
	return trace, need, nil
}

func validateCELTQuantityTFTrace(trace celtTFEncodeTrace) error {
	bandCalls := int(trace.End - trace.Start)
	firstLogP := uint32(4)
	nextLogP := uint32(5)
	if trace.Transient != 0 {
		firstLogP = 2
		nextLogP = 4
	}
	budget := int64(trace.StorageBits)
	tell := int64(trace.EntryTell)
	selectReserved := trace.LM > 0 && tell+int64(firstLogP)+1 <= budget
	if selectReserved {
		budget--
	}
	bitIndex := 0
	curr := int32(0)
	tfChanged := int32(0)
	budgetedFlags := make([]int32, bandCalls)
	for index := 0; index < bandCalls; index++ {
		logp := nextLogP
		if index == 0 {
			logp = firstLogP
		}
		if tell+int64(logp) <= budget {
			if bitIndex >= len(trace.Bits) {
				return fmt.Errorf("GCTF omitted budget-eligible TF band %d (tell=%d logp=%d budget=%d)", trace.Start+int32(index), tell, logp, budget)
			}
			bit := trace.Bits[bitIndex]
			if bit.Ordinal != uint32(bitIndex) || bit.Symbol < 0 || bit.Symbol > 1 || bit.LogP != logp ||
				bit.TellBefore != int32(tell) || bit.TellBefore < 0 || bit.TellAfter < bit.TellBefore ||
				uint64(bit.TellFracBefore) > uint64(trace.StorageBits)*8+8 ||
				uint64(bit.TellFracAfter) > uint64(trace.StorageBits)*8+8 ||
				bit.TellFracAfter < bit.TellFracBefore || bit.RangeBefore == 0 || bit.RangeAfter == 0 {
				return fmt.Errorf("invalid GCTF TF event %d: ordinal=%d symbol=%d logp=%d tell=%d→%d want logp=%d tell-before=%d range=%08x/%08x",
					bitIndex, bit.Ordinal, bit.Symbol, bit.LogP, bit.TellBefore, bit.TellAfter,
					logp, tell, bit.RangeBefore, bit.RangeAfter)
			}
			if bitIndex == 0 {
				if bit.TellBefore != trace.EntryTell {
					return fmt.Errorf("GCTF first TF event tell=%d, want entry tell=%d", bit.TellBefore, trace.EntryTell)
				}
			} else {
				previous := trace.Bits[bitIndex-1]
				if previous.RangeAfter != bit.RangeBefore || previous.TellFracAfter != bit.TellFracBefore ||
					previous.TellAfter != bit.TellBefore {
					return fmt.Errorf("GCTF event %d state does not continue event %d: range/tell-frac/tell=%08x/%d/%d→%08x/%d/%d",
						bitIndex, bitIndex-1, previous.RangeAfter, previous.TellFracAfter, previous.TellAfter,
						bit.RangeBefore, bit.TellFracBefore, bit.TellBefore)
				}
			}
			curr ^= bit.Symbol
			tfChanged |= curr
			budgetedFlags[index] = curr
			tell = int64(bit.TellAfter)
			bitIndex++
		} else {
			return fmt.Errorf("selected GCTF fixture skipped TF band %d at tell=%d logp=%d budget=%d; raw C flags are incomplete",
				trace.Start+int32(index), tell, logp, budget)
		}
	}
	if !selectReserved && trace.SelectEncoded != 0 {
		return fmt.Errorf("GCTF encoded tf_select without reservation at tell=%d LM=%d", trace.EntryTell, trace.LM)
	}
	tableBase := int(trace.Transient) * 4
	selectChangesTable := celtTFTraceSelectTable[trace.LM][tableBase+int(tfChanged)] !=
		celtTFTraceSelectTable[trace.LM][tableBase+2+int(tfChanged)]
	wantSelect := selectReserved && selectChangesTable
	if (trace.SelectEncoded != 0) != wantSelect {
		return fmt.Errorf("GCTF tf_select event=%d, want=%t from reservation=%t tf_changed=%d table row LM=%d transient=%d",
			trace.SelectEncoded, wantSelect, selectReserved, tfChanged, trace.LM, trace.Transient)
	}
	effectiveSelect := int32(0)
	if trace.SelectEncoded != 0 {
		if bitIndex >= len(trace.Bits) {
			return fmt.Errorf("GCTF omitted reserved tf_select event")
		}
		bit := trace.Bits[bitIndex]
		if bit.Ordinal != uint32(bitIndex) || bit.Symbol != trace.SelectSymbol || bit.Symbol < 0 || bit.Symbol > 1 ||
			bit.LogP != 1 || bit.TellBefore != int32(tell) || bit.TellBefore < 0 || bit.TellAfter < bit.TellBefore ||
			uint64(bit.TellFracBefore) > uint64(trace.StorageBits)*8+8 ||
			uint64(bit.TellFracAfter) > uint64(trace.StorageBits)*8+8 ||
			bit.TellFracAfter < bit.TellFracBefore || bit.RangeBefore == 0 || bit.RangeAfter == 0 {
			return fmt.Errorf("invalid GCTF tf_select event %d: symbol=%d logp=%d tell=%d→%d", bitIndex, bit.Symbol, bit.LogP, bit.TellBefore, bit.TellAfter)
		}
		if bitIndex == 0 {
			if bit.TellBefore != trace.EntryTell {
				return fmt.Errorf("GCTF first TF event tell=%d, want entry tell=%d", bit.TellBefore, trace.EntryTell)
			}
		} else {
			previous := trace.Bits[bitIndex-1]
			if previous.RangeAfter != bit.RangeBefore || previous.TellFracAfter != bit.TellFracBefore ||
				previous.TellAfter != bit.TellBefore {
				return fmt.Errorf("GCTF event %d state does not continue event %d", bitIndex, bitIndex-1)
			}
		}
		effectiveSelect = bit.Symbol
		bitIndex++
	}
	if bitIndex != len(trace.Bits) {
		return fmt.Errorf("GCTF has %d unexplained TF events after budget simulation", len(trace.Bits)-bitIndex)
	}
	lastBit := trace.Bits[len(trace.Bits)-1]
	if lastBit.RangeAfter != trace.SpreadRangeBefore || lastBit.TellFracAfter != trace.SpreadTellFracBefore {
		return fmt.Errorf("GCTF coder state before spread ICDF differs: last TF range/tell-frac=%08x/%d, spread-before=%08x/%d",
			lastBit.RangeAfter, lastBit.TellFracAfter, trace.SpreadRangeBefore, trace.SpreadTellFracBefore)
	}
	for i, flag := range budgetedFlags {
		index := tableBase + 2*int(effectiveSelect) + int(flag)
		want := celtTFTraceSelectTable[trace.LM][index]
		if trace.PostTFRes[i] != want {
			return fmt.Errorf("GCTF post-TF band %d=%d, want %d from tf_select_table row LM=%d transient=%d select=%d flag=%d",
				trace.Start+int32(i), trace.PostTFRes[i], want, trace.LM, trace.Transient, effectiveSelect, flag)
		}
	}
	return nil
}

func compareCELTQuantityTFTrace(goTrace celt.TFEncodeTraceSnapshot, cTrace celtTFEncodeTrace) string {
	if goTrace.Coder == nil || goTrace.CallCount != 1 || goTrace.ForeignCalls != 0 || goTrace.Overflow || !goTrace.Complete {
		return fmt.Sprintf("invalid Go TF trace: coder=%p calls=%d foreign=%d overflow=%t complete=%t",
			goTrace.Coder, goTrace.CallCount, goTrace.ForeignCalls, goTrace.Overflow, goTrace.Complete)
	}
	if int32(len(goTrace.TFResBefore)) != cTrace.End-cTrace.Start ||
		int32(len(goTrace.TFResBudgeted)) != cTrace.End-cTrace.Start ||
		int32(len(goTrace.TFResAfter)) != cTrace.End-cTrace.Start || len(goTrace.Bits) != len(cTrace.Bits) {
		return fmt.Sprintf("TF shape differs: Go start/end=%d/%d raw/budgeted/post/events=%d/%d/%d/%d C start/end=%d/%d raw/post/events=%d/%d/%d",
			goTrace.Start, goTrace.End, len(goTrace.TFResBefore), len(goTrace.TFResBudgeted), len(goTrace.TFResAfter), len(goTrace.Bits),
			cTrace.Start, cTrace.End, cTrace.End-cTrace.Start, len(cTrace.PostTFRes), len(cTrace.Bits))
	}
	if goTrace.Start != cTrace.Start || goTrace.End != cTrace.End || goTrace.LM != cTrace.LM || boolToUint32(goTrace.Transient) != cTrace.Transient {
		return fmt.Sprintf("TF call controls differ: Go start/end/LM/transient=%d/%d/%d/%t C=%d/%d/%d/%t",
			goTrace.Start, goTrace.End, goTrace.LM, goTrace.Transient,
			cTrace.Start, cTrace.End, cTrace.LM, cTrace.Transient != 0)
	}
	if goTrace.EntryTell != cTrace.EntryTell || goTrace.StorageBits != cTrace.StorageBits {
		return fmt.Sprintf("TF budget entry differs: Go tell/storage=%d/%d C=%d/%d",
			goTrace.EntryTell, goTrace.StorageBits, cTrace.EntryTell, cTrace.StorageBits)
	}
	firstLogP := int32(4)
	if goTrace.Transient {
		firstLogP = 2
	}
	wantSelectReserved := goTrace.LM > 0 && goTrace.EntryTell+firstLogP+1 <= int32(goTrace.StorageBits)
	if goTrace.TFSelectReserved != wantSelectReserved {
		return fmt.Sprintf("Go tf_select reservation=%t disagrees with budget tell=%d first_logp=%d storage=%d",
			goTrace.TFSelectReserved, goTrace.EntryTell, firstLogP, goTrace.StorageBits)
	}
	bandCalls := int(cTrace.End - cTrace.Start)
	var cRaw int32
	for i := 0; i < bandCalls; i++ {
		cRaw ^= cTrace.Bits[i].Symbol
		if goTrace.TFResBefore[i] != cRaw {
			return fmt.Sprintf("first raw tf_res difference at band %d: Go=%d C=%d reconstructed from actual XOR calls",
				cTrace.Start+int32(i), goTrace.TFResBefore[i], cRaw)
		}
		if goTrace.TFResBudgeted[i] != cRaw {
			return fmt.Sprintf("first budgeted tf_res difference at band %d: Go=%d C=%d",
				cTrace.Start+int32(i), goTrace.TFResBudgeted[i], cRaw)
		}
	}
	for i := range cTrace.PostTFRes {
		if goTrace.TFResAfter[i] != cTrace.PostTFRes[i] {
			return fmt.Sprintf("first effective tf_res difference at band %d: Go=%d C=%d",
				cTrace.Start+int32(i), goTrace.TFResAfter[i], cTrace.PostTFRes[i])
		}
	}
	if goTrace.TFSelectEncoded != (cTrace.SelectEncoded != 0) {
		return fmt.Sprintf("tf_select coding differs: Go encoded=%t C encoded=%d", goTrace.TFSelectEncoded, cTrace.SelectEncoded)
	}
	if cTrace.SelectEncoded != 0 && goTrace.TFSelectValue != cTrace.SelectSymbol {
		return fmt.Sprintf("tf_select symbol differs: Go=%d C=%d", goTrace.TFSelectValue, cTrace.SelectSymbol)
	}
	if goTrace.TFSelectEffective != cTrace.SelectSymbol && cTrace.SelectEncoded != 0 {
		return fmt.Sprintf("effective tf_select differs: Go=%d C encoded=%d", goTrace.TFSelectEffective, cTrace.SelectSymbol)
	}
	if cTrace.SelectEncoded == 0 && goTrace.TFSelectEffective != 0 {
		return fmt.Sprintf("Go effective tf_select=%d without an encoded select bit", goTrace.TFSelectEffective)
	}
	for i, goBit := range goTrace.Bits {
		cBit := cTrace.Bits[i]
		wantSelectBit := i >= bandCalls
		if int32(i) != goBit.Ordinal || cBit.Ordinal != uint32(i) || goBit.Symbol != cBit.Symbol ||
			goBit.LogP != cBit.LogP || goBit.RangeBefore != cBit.RangeBefore ||
			uint32(goBit.TellFracBefore) != cBit.TellFracBefore || goBit.TellBefore != cBit.TellBefore ||
			goBit.RangeAfter != cBit.RangeAfter || uint32(goBit.TellFracAfter) != cBit.TellFracAfter ||
			goBit.TellAfter != cBit.TellAfter || goBit.SelectBit != wantSelectBit {
			return fmt.Sprintf("first TF entropy-call difference at event %d: Go{symbol=%d logp=%d range/tell=%08x/%d/%d→%08x/%d/%d select=%t} C{symbol=%d logp=%d range/tell=%08x/%d/%d→%08x/%d/%d}",
				i, goBit.Symbol, goBit.LogP, goBit.RangeBefore, goBit.TellFracBefore, goBit.TellBefore, goBit.RangeAfter, goBit.TellFracAfter, goBit.TellAfter, goBit.SelectBit,
				cBit.Symbol, cBit.LogP, cBit.RangeBefore, cBit.TellFracBefore, cBit.TellBefore, cBit.RangeAfter, cBit.TellFracAfter, cBit.TellAfter)
		}
	}
	lastGoBit := goTrace.Bits[len(goTrace.Bits)-1]
	if lastGoBit.RangeAfter != cTrace.SpreadRangeBefore || uint32(lastGoBit.TellFracAfter) != cTrace.SpreadTellFracBefore {
		return fmt.Sprintf("coder state before spread ICDF differs: Go range/tell-frac=%08x/%d C spread-before=%08x/%d",
			lastGoBit.RangeAfter, lastGoBit.TellFracAfter, cTrace.SpreadRangeBefore, cTrace.SpreadTellFracBefore)
	}
	if goTrace.TFSelectEffective != 0 && cTrace.SelectEncoded == 0 {
		return fmt.Sprintf("TF data matches but C select input is unobserved; Go effective select=%d", goTrace.TFSelectEffective)
	}
	return ""
}

func boolToUint32(value bool) uint32 {
	if value {
		return 1
	}
	return 0
}

func validCELTQuantityTFTraceWireForTesting() []byte {
	data := make([]byte, celtTFTraceHeader+2*4+2*36)
	copy(data[:4], "GCTF")
	words := [...]uint32{
		1, 25, 0, 1, 2, 2, 0, 1, 1, 1, 1, 1,
		3, 5, 0, 300, 512, 0, 0, 0, 2, 3, 5, 0x0400, 2472, 0x0300, 2512,
	}
	for i, word := range words {
		binary.LittleEndian.PutUint32(data[4+i*4:], word)
	}
	off := celtTFTraceHeader
	for _, value := range []int32{0, -1} {
		binary.LittleEndian.PutUint32(data[off:], uint32(value))
		off += 4
	}
	for i, bit := range [][8]uint32{
		{0, 4, 0x1000, 2400, 300, 0x0800, 2432, 304},
		{1, 5, 0x0800, 2432, 304, 0x0400, 2472, 309},
	} {
		values := [...]uint32{uint32(i), bit[0], bit[1], bit[2], bit[3], bit[4], bit[5], bit[6], bit[7]}
		for _, value := range values {
			binary.LittleEndian.PutUint32(data[off:], value)
			off += 4
		}
	}
	return data
}

func TestCELTQuantityTFTraceRejectsMalformed(t *testing.T) {
	valid := validCELTQuantityTFTraceWireForTesting()
	trace, consumed, err := parseCELTQuantityTFTrace(valid, 25)
	if err != nil || consumed != len(valid) || len(trace.Bits) != 2 || trace.PostTFRes[1] != -1 {
		t.Fatalf("valid GCTF rejected or decoded incorrectly: trace=%+v consumed=%d/%d err=%v", trace, consumed, len(valid), err)
	}
	mutate := func(word int, value uint32) []byte {
		data := append([]byte(nil), valid...)
		binary.LittleEndian.PutUint32(data[4+word*4:], value)
		return data
	}
	badLogP := append([]byte(nil), valid...)
	binary.LittleEndian.PutUint32(badLogP[celtTFTraceHeader+8:], 2)
	zeroRange := append([]byte(nil), valid...)
	binary.LittleEndian.PutUint32(zeroRange[celtTFTraceHeader+8+12:], 0)
	badSpreadRange := mutate(23, 0)
	badSpreadTellUnit := mutate(24, 309)
	badTellFracChain := append([]byte(nil), valid...)
	binary.LittleEndian.PutUint32(badTellFracChain[celtTFTraceHeader+8+36+16:], 2433)
	badTellChain := append([]byte(nil), valid...)
	binary.LittleEndian.PutUint32(badTellChain[celtTFTraceHeader+8+36+20:], 303)
	truncated := append([]byte(nil), valid[:len(valid)-1]...)
	for _, tc := range []struct {
		name string
		data []byte
	}{
		{"truncated header", valid[:celtTFTraceHeader-1]},
		{"bad magic", append([]byte("XCTF"), valid[4:]...)},
		{"wrong version", mutate(0, 2)},
		{"wrong selected frame", mutate(1, 24)},
		{"overflow", mutate(2, 1)},
		{"missing coarse identity", mutate(10, 0)},
		{"missing spread match", mutate(9, 0)},
		{"wrong count", mutate(4, 1)},
		{"post count mismatch", mutate(20, 1)},
		{"invalid geometry", mutate(13, 2)},
		{"invalid transient", mutate(17, 2)},
		{"invalid select bit", mutate(18, 2)},
		{"zero spread range", badSpreadRange},
		{"spread fractional tell uses integer tell units", badSpreadTellUnit},
		{"zero event range", zeroRange},
		{"discontinuous fractional tell", badTellFracChain},
		{"discontinuous integer tell", badTellChain},
		{"wrong probability", badLogP},
		{"truncated events", truncated},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if _, _, err := parseCELTQuantityTFTrace(tc.data, 25); err == nil {
				t.Fatal("malformed GCTF payload was accepted")
			}
		})
	}
	if _, consumed, err := parseCELTQuantityTFTrace(append(valid, []byte("GQTR")...), 25); err != nil || consumed != len(valid) {
		t.Fatalf("GCTF parser did not leave the following GQTR trailer intact: consumed=%d err=%v", consumed, err)
	}
}

func TestCELTQuantityTFTraceComparisonReportsFirstActualDifference(t *testing.T) {
	cTrace, _, err := parseCELTQuantityTFTrace(validCELTQuantityTFTraceWireForTesting(), 25)
	if err != nil {
		t.Fatal(err)
	}
	goTrace := celt.TFEncodeTraceSnapshot{
		Start: 3, End: 5, LM: 0, EntryTell: 300, StorageBits: 512, Transient: false, TFSelectInput: 0,
		TFSelectReserved: false,
		TFResBefore:      []int32{0, 1}, TFResBudgeted: []int32{0, 1}, TFResAfter: []int32{0, -1},
		TFSelectEffective: 0, CallCount: 1, Complete: true,
		Bits: []celt.TFEncodeBitTrace{
			{Ordinal: 0, Symbol: 0, LogP: 4, RangeBefore: 0x1000, TellFracBefore: 2400, TellBefore: 300, RangeAfter: 0x0800, TellFracAfter: 2432, TellAfter: 304},
			{Ordinal: 1, Symbol: 1, LogP: 5, RangeBefore: 0x0800, TellFracBefore: 2432, TellBefore: 304, RangeAfter: 0x0400, TellFracAfter: 2472, TellAfter: 309},
		},
	}
	if difference := compareCELTQuantityTFTrace(goTrace, cTrace); difference == "" {
		t.Fatal("comparison accepted a missing actual coder identity")
	}
	goTrace.Coder = new(rangecoding.Encoder)
	if err := validateGoCELTQuantityTFTrace(goTrace); err != nil {
		t.Fatalf("valid Go TF trace rejected: %v", err)
	}
	badBudget := goTrace
	badBudget.TFResBudgeted = append([]int32(nil), goTrace.TFResBudgeted...)
	badBudget.TFResBudgeted[1] = 0
	if err := validateGoCELTQuantityTFTrace(badBudget); err == nil {
		t.Fatal("Go TF validator accepted an incorrect budgeted flag")
	}
	badTell := goTrace
	badTell.Bits = append([]celt.TFEncodeBitTrace(nil), goTrace.Bits...)
	badTell.Bits[1].TellBefore++
	if err := validateGoCELTQuantityTFTrace(badTell); err == nil {
		t.Fatal("Go TF validator accepted a discontinuous coder tell")
	}
	if difference := compareCELTQuantityTFTrace(goTrace, cTrace); difference != "" {
		t.Fatalf("matching actual TF call trace rejected: %s", difference)
	}
	goTrace.Bits[1].TellFracAfter++
	if difference := compareCELTQuantityTFTrace(goTrace, cTrace); !strings.Contains(difference, "event 1") {
		t.Fatalf("comparison did not report the first changed actual TF event: %q", difference)
	}
}

func validateGoCELTQuantityTFTrace(trace celt.TFEncodeTraceSnapshot) error {
	if trace.Coder == nil || trace.CallCount != 1 || trace.ForeignCalls != 0 || trace.Overflow || !trace.Complete {
		return fmt.Errorf("Go trace coder=%p calls=%d foreign=%d overflow=%t complete=%t",
			trace.Coder, trace.CallCount, trace.ForeignCalls, trace.Overflow, trace.Complete)
	}
	if trace.Start < 0 || trace.End <= trace.Start || trace.End > celtTFTraceMaxBands ||
		trace.LM < 0 || trace.LM > 3 || len(trace.TFResBefore) != int(trace.End-trace.Start) ||
		len(trace.TFResBudgeted) != len(trace.TFResBefore) || len(trace.TFResAfter) != len(trace.TFResBefore) ||
		trace.EntryTell < 0 || trace.StorageBits == 0 || uint64(trace.EntryTell) > uint64(trace.StorageBits) ||
		trace.TFSelectInput < 0 || trace.TFSelectInput > 1 || trace.TFSelectEffective < 0 || trace.TFSelectEffective > 1 {
		return fmt.Errorf("invalid Go TF geometry start=%d end=%d LM=%d raw/budgeted/post=%d/%d/%d entry_tell=%d storage_bits=%d select-input/effective=%d/%d",
			trace.Start, trace.End, trace.LM, len(trace.TFResBefore), len(trace.TFResBudgeted), len(trace.TFResAfter),
			trace.EntryTell, trace.StorageBits, trace.TFSelectInput, trace.TFSelectEffective)
	}
	maxTellFrac := int64(trace.StorageBits)*8 + 8
	if len(trace.Bits) > celtTFTraceMaxBits {
		return fmt.Errorf("Go TF trace has %d entropy calls, bound=%d", len(trace.Bits), celtTFTraceMaxBits)
	}

	firstLogP, nextLogP := int64(4), int64(5)
	if trace.Transient {
		firstLogP, nextLogP = 2, 4
	}
	budget := int64(trace.StorageBits)
	tell := int64(trace.EntryTell)
	selectReserved := trace.LM > 0 && tell+firstLogP+1 <= budget
	if selectReserved {
		budget--
	}
	if trace.TFSelectReserved != selectReserved {
		return fmt.Errorf("Go tf_select reservation=%t, want %t from tell=%d first_logp=%d storage=%d",
			trace.TFSelectReserved, selectReserved, trace.EntryTell, firstLogP, trace.StorageBits)
	}

	bitIndex := 0
	curr, tfChanged := int32(0), int32(0)
	budgeted := make([]int32, len(trace.TFResBefore))
	for band, raw := range trace.TFResBefore {
		if raw < 0 || raw > 1 {
			return fmt.Errorf("Go raw tf_res band %d=%d, want 0 or 1", trace.Start+int32(band), raw)
		}
		logp := nextLogP
		if band == 0 {
			logp = firstLogP
		}
		if tell+logp > budget {
			return fmt.Errorf("selected Go TF fixture skips band %d at tell=%d logp=%d budget=%d",
				trace.Start+int32(band), tell, logp, budget)
		}
		if bitIndex >= len(trace.Bits) {
			return fmt.Errorf("Go trace omits budget-eligible TF band %d", trace.Start+int32(band))
		}
		bit := trace.Bits[bitIndex]
		wantSymbol := raw ^ curr
		if bit.Ordinal != int32(bitIndex) || bit.SelectBit || bit.Symbol != wantSymbol || bit.LogP != uint32(logp) ||
			bit.TellBefore != int32(tell) || bit.TellBefore < 0 || bit.TellAfter < bit.TellBefore ||
			int64(bit.TellFracBefore) < 0 || int64(bit.TellFracBefore) > maxTellFrac ||
			int64(bit.TellFracAfter) > maxTellFrac ||
			bit.TellFracAfter < bit.TellFracBefore ||
			bit.RangeBefore == 0 || bit.RangeAfter == 0 {
			return fmt.Errorf("invalid Go TF band event %d: symbol=%d want=%d logp=%d want=%d tell=%d→%d want-before=%d range=%08x/%08x tell-frac=%d→%d",
				bitIndex, bit.Symbol, wantSymbol, bit.LogP, logp, bit.TellBefore, bit.TellAfter, tell,
				bit.RangeBefore, bit.RangeAfter, bit.TellFracBefore, bit.TellFracAfter)
		}
		if bitIndex == 0 {
			if bit.TellBefore != trace.EntryTell {
				return fmt.Errorf("Go first TF event tell=%d, want entry tell=%d", bit.TellBefore, trace.EntryTell)
			}
		} else {
			previous := trace.Bits[bitIndex-1]
			if previous.RangeAfter != bit.RangeBefore || previous.TellFracAfter != bit.TellFracBefore ||
				previous.TellAfter != bit.TellBefore {
				return fmt.Errorf("Go TF event %d state does not continue event %d", bitIndex, bitIndex-1)
			}
		}
		curr = raw
		tfChanged |= curr
		budgeted[band] = curr
		if trace.TFResBudgeted[band] != curr {
			return fmt.Errorf("Go budgeted tf_res band %d=%d, want %d from actual XOR calls",
				trace.Start+int32(band), trace.TFResBudgeted[band], curr)
		}
		tell = int64(bit.TellAfter)
		bitIndex++
	}

	tableBase := 4 * boolToUint32(trace.Transient)
	selectTableChanges := celtTFTraceSelectTable[trace.LM][int(tableBase)+int(tfChanged)] !=
		celtTFTraceSelectTable[trace.LM][int(tableBase)+2+int(tfChanged)]
	wantSelect := selectReserved && selectTableChanges
	if trace.TFSelectEncoded != wantSelect {
		return fmt.Errorf("Go tf_select encoded=%t, want %t from reservation=%t tf_changed=%d LM=%d transient=%t",
			trace.TFSelectEncoded, wantSelect, selectReserved, tfChanged, trace.LM, trace.Transient)
	}
	effectiveSelect := int32(0)
	if trace.TFSelectEncoded {
		if bitIndex >= len(trace.Bits) {
			return fmt.Errorf("Go trace omits reserved tf_select event")
		}
		bit := trace.Bits[bitIndex]
		if bit.Ordinal != int32(bitIndex) || !bit.SelectBit || bit.Symbol != trace.TFSelectInput ||
			bit.LogP != 1 || bit.TellBefore != int32(tell) || bit.TellAfter < bit.TellBefore ||
			int64(bit.TellFracBefore) < 0 || int64(bit.TellFracBefore) > maxTellFrac ||
			int64(bit.TellFracAfter) > maxTellFrac ||
			bit.TellFracAfter < bit.TellFracBefore || bit.RangeBefore == 0 || bit.RangeAfter == 0 {
			return fmt.Errorf("invalid Go tf_select event %d: symbol=%d input=%d logp=%d tell=%d→%d",
				bitIndex, bit.Symbol, trace.TFSelectInput, bit.LogP, bit.TellBefore, bit.TellAfter)
		}
		if bitIndex == 0 {
			if bit.TellBefore != trace.EntryTell {
				return fmt.Errorf("Go first TF event tell=%d, want entry tell=%d", bit.TellBefore, trace.EntryTell)
			}
		} else {
			previous := trace.Bits[bitIndex-1]
			if previous.RangeAfter != bit.RangeBefore || previous.TellFracAfter != bit.TellFracBefore ||
				previous.TellAfter != bit.TellBefore {
				return fmt.Errorf("Go tf_select event state does not continue event %d", bitIndex-1)
			}
		}
		effectiveSelect = bit.Symbol
		if trace.TFSelectValue != bit.Symbol {
			return fmt.Errorf("Go tf_select value=%d differs from actual event symbol=%d", trace.TFSelectValue, bit.Symbol)
		}
		bitIndex++
	} else if trace.TFSelectEffective != 0 {
		return fmt.Errorf("Go effective tf_select=%d without an encoded select bit", trace.TFSelectEffective)
	}
	if bitIndex != len(trace.Bits) {
		return fmt.Errorf("Go TF trace has %d unexplained entropy events", len(trace.Bits)-bitIndex)
	}
	if trace.TFSelectEffective != effectiveSelect {
		return fmt.Errorf("Go effective tf_select=%d, want %d", trace.TFSelectEffective, effectiveSelect)
	}
	for band, flag := range budgeted {
		index := int(tableBase) + 2*int(effectiveSelect) + int(flag)
		want := celtTFTraceSelectTable[trace.LM][index]
		if trace.TFResAfter[band] != want {
			return fmt.Errorf("Go post-TF band %d=%d, want %d from tf_select_table flag=%d select=%d",
				trace.Start+int32(band), trace.TFResAfter[band], want, flag, effectiveSelect)
		}
	}
	return nil
}
