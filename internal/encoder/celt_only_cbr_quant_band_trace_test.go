//go:build linux && amd64.v3 && gopus_celt_trace && !gopus_fixed_point && !gopus_qext

package encoder

import (
	"encoding/binary"
	"fmt"
	"strings"
	"testing"

	"github.com/thesyncim/gopus/internal/celt"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/libopustooling"
	"github.com/thesyncim/gopus/internal/testsignal"
)

var celtOnlyCBRQuantTraceCache libopustest.HelperCache

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

	tracePath := buildCELTQuantTraceOracleAtFrameBand(t, celtOnlyCBRQuantTraceFrame,
		celtOnlyCBRQuantTraceBand, true, &celtOnlyCBRQuantTraceCache)
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
	gqtrData := rangeData[rangeBytes:]
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
			quantOverflow = celt.WithCELTQuantBandTraceHookForTesting(celtOnlyCBRQuantTraceBand,
				func(event *celt.CELTQuantBandTraceSnapshot) { goQuant = append(goQuant, *event) }, encodeTraced)
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
