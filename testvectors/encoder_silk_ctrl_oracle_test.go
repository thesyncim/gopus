//go:build gopus_silk_trace

// Frame-level SILK encoder-control oracle comparison.
//
// Builds tools/csrc/libopus_silk_ctrl_info.c (which overrides
// silk_encode_frame_FLP via tools/csrc/silk_encode_frame_FLP_dump.c) and dumps,
// for every internal SILK frame, the silk_encoder_control_FLP state that drives
// NSQ + rate control. It then replays the same PCM through gopus' SILK FLP path
// with silk.WithSILKCtrlSnapshotHook and reports the FIRST diverging shaping/NSQ
// quantity, so a size delta in the unconstrained-VBR iter-0 break can be traced
// to its source.
//
//	Run: GOWORK=off GOPUS_TEST_TIER=parity GOPUS_STRICT_LIBOPUS_REF=1 \
//	       go test -tags gopus_silk_trace ./testvectors/ -run TestSILKCtrlOracle -v
package testvectors

import (
	"encoding/binary"
	"fmt"
	"math"
	"path/filepath"
	"testing"

	gopus "github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/silk"
	"github.com/thesyncim/gopus/internal/testsignal"
	"github.com/thesyncim/gopus/types"
)

const (
	silkCtrlMaxNbSubfr  = 4
	silkCtrlMaxShapeLPC = 24
	silkCtrlMaxLPC      = 16
	silkCtrlLTPOrder    = 5
	silkCtrlInputMagic  = "GSCI"
	silkCtrlOutputMagic = "GSCO"
)

var silkCtrlHelper libopustest.HelperCache

// silkEncodeFrameDumpSource returns the absolute path to the instrumented
// silk_encode_frame_FLP override translation unit under tools/csrc.
func silkEncodeFrameDumpSource() string {
	// RefPath() == <repoRoot>/tmp_check/opus-<ver>; two parents up is repoRoot.
	repoRoot := filepath.Dir(filepath.Dir(libopustest.RefPath()))
	return filepath.Join(repoRoot, "tools", "csrc", "silk_encode_frame_FLP_dump.c")
}

func getSILKCtrlHelperPath(t testing.TB) (string, bool) {
	t.Helper()
	path, err := silkCtrlHelper.Path(func() (string, error) {
		return libopustest.BuildPublicAPIHelper(libopustest.CHelperConfig{
			Label:      "silk ctrl",
			OutputBase: "gopus_libopus_silk_ctrl",
			SourceFile: "libopus_silk_ctrl_info.c",
			CFlags:     []string{"-DHAVE_CONFIG_H", "-O2", "-DNDEBUG"},
			RefIncludes: []string{
				"silk", "silk/float", "celt",
			},
			Sources: []string{silkEncodeFrameDumpSource()},
		})
	})
	if err != nil {
		if libopustest.StrictRefRequired() {
			t.Fatalf("build silk ctrl helper: %v", err)
		}
		t.Skipf("silk ctrl helper unavailable: %v", err)
		return "", false
	}
	return path, true
}

// silkCtrlRecord mirrors one ctrl_record emitted by the C oracle.
type silkCtrlRecord struct {
	opusFrame     int32
	channel       int32
	nbSubfr       int32
	signalType    int32
	quantOffset   int32
	maxBits       int32
	useCBR        int32
	nBytes        int32
	predGain      float32
	ltpredCodGain float32
	lambda        float32
	inputQuality  float32
	codingQuality float32
	snrDBQ7       int32
	inQBandsQ15   [4]int32
	speechActQ8   int32
	gainsUnqQ16   [silkCtrlMaxNbSubfr]int32
	gains         [silkCtrlMaxNbSubfr]float32
	ar            [silkCtrlMaxNbSubfr * silkCtrlMaxShapeLPC]float32
	lfMA          [silkCtrlMaxNbSubfr]float32
	lfAR          [silkCtrlMaxNbSubfr]float32
	tilt          [silkCtrlMaxNbSubfr]float32
	harmShapeGain [silkCtrlMaxNbSubfr]float32
	ltpCoef       [silkCtrlLTPOrder * silkCtrlMaxNbSubfr]float32
	ltpScale      float32
	pitchL        [silkCtrlMaxNbSubfr]int32
}

type silkCtrlOracleOut struct {
	packets [][]byte
	ranges  []uint32
	ctrl    []silkCtrlRecord
	stages  []silkEncodeStageRecord
}

type silkEncodeStageRecord struct {
	frame, channel, iter, stage, tell int32
	rangeValue                        uint32
	signalType, quantOffset, seed     int32
	lagIndex, contour, nlsfInterp     int32
	perIndex, ltpScale                int32
	gains                             [silkCtrlMaxNbSubfr]int32
	ltp                               [silkCtrlMaxNbSubfr]int32
	nlsf                              [silkCtrlMaxLPC + 1]int32
	pulses                            []int8
}

func runSILKCtrlOracle(helperPath string, req []byte, nFrames int) (*silkCtrlOracleOut, error) {
	raw, err := libopustest.RunHelper(helperPath, req)
	if err != nil {
		return nil, fmt.Errorf("run silk ctrl oracle: %w", err)
	}
	if len(raw) < 12 || string(raw[0:4]) != silkCtrlOutputMagic {
		return nil, fmt.Errorf("bad oracle response magic")
	}
	version := binary.LittleEndian.Uint32(raw[4:8])
	if version != 2 {
		return nil, fmt.Errorf("bad oracle version")
	}
	gotN := int(binary.LittleEndian.Uint32(raw[8:12]))
	if gotN != nFrames {
		return nil, fmt.Errorf("oracle frame count mismatch: got %d want %d", gotN, nFrames)
	}
	out := &silkCtrlOracleOut{}
	off := 12
	for i := 0; i < nFrames; i++ {
		pktLen := int(binary.LittleEndian.Uint32(raw[off:]))
		fr := binary.LittleEndian.Uint32(raw[off+4:])
		off += 8
		out.packets = append(out.packets, append([]byte(nil), raw[off:off+pktLen]...))
		out.ranges = append(out.ranges, fr)
		off += pktLen
	}
	nCtrl := int(binary.LittleEndian.Uint32(raw[off:]))
	off += 4
	rd := func() uint32 { v := binary.LittleEndian.Uint32(raw[off:]); off += 4; return v }
	ri := func() int32 { return int32(rd()) }
	rf := func() float32 { return math.Float32frombits(rd()) }
	for c := 0; c < nCtrl; c++ {
		var r silkCtrlRecord
		r.opusFrame = ri()
		r.channel = ri()
		r.nbSubfr = ri()
		r.signalType = ri()
		r.quantOffset = ri()
		r.maxBits = ri()
		r.useCBR = ri()
		r.nBytes = ri()
		r.predGain = rf()
		r.ltpredCodGain = rf()
		r.lambda = rf()
		r.inputQuality = rf()
		r.codingQuality = rf()
		r.snrDBQ7 = ri()
		for k := range r.inQBandsQ15 {
			r.inQBandsQ15[k] = ri()
		}
		r.speechActQ8 = ri()
		for k := range r.gainsUnqQ16 {
			r.gainsUnqQ16[k] = ri()
		}
		for k := range r.gains {
			r.gains[k] = rf()
		}
		for k := range r.ar {
			r.ar[k] = rf()
		}
		for k := range r.lfMA {
			r.lfMA[k] = rf()
		}
		for k := range r.lfAR {
			r.lfAR[k] = rf()
		}
		for k := range r.tilt {
			r.tilt[k] = rf()
		}
		for k := range r.harmShapeGain {
			r.harmShapeGain[k] = rf()
		}
		for k := range r.ltpCoef {
			r.ltpCoef[k] = rf()
		}
		r.ltpScale = rf()
		for k := range r.pitchL {
			r.pitchL[k] = ri()
		}
		out.ctrl = append(out.ctrl, r)
	}
	if version >= 2 {
		nStages := int(rd())
		for i := 0; i < nStages; i++ {
			var r silkEncodeStageRecord
			r.frame, r.channel, r.iter, r.stage, r.tell = ri(), ri(), ri(), ri(), ri()
			r.rangeValue = rd()
			r.signalType, r.quantOffset, r.seed = ri(), ri(), ri()
			r.lagIndex, r.contour, r.nlsfInterp = ri(), ri(), ri()
			r.perIndex, r.ltpScale = ri(), ri()
			for k := range r.gains {
				r.gains[k] = ri()
			}
			for k := range r.ltp {
				r.ltp[k] = ri()
			}
			for k := range r.nlsf {
				r.nlsf[k] = ri()
			}
			nPulses := int(ri())
			if nPulses < 0 || nPulses > len(raw)-off {
				return nil, fmt.Errorf("invalid stage pulse length %d (record %d/%d offset=%d remain=%d)", nPulses, i, nStages, off, len(raw)-off)
			}
			r.pulses = make([]int8, nPulses)
			for k := range r.pulses {
				r.pulses[k] = int8(raw[off])
				off++
			}
			out.stages = append(out.stages, r)
		}
		if overflow := ri(); overflow != 0 {
			return nil, fmt.Errorf("C stage trace overflowed its bounded record buffer")
		}
	}
	if off != len(raw) {
		return nil, fmt.Errorf("trailing oracle bytes: consumed %d of %d", off, len(raw))
	}
	return out, nil
}

// TestSILKCtrlOracle bisects the first diverging SILK control quantity vs
// libopus for the pure-SILK unconstrained-VBR mono case.
func TestSILKCtrlOracle(t *testing.T) {
	t.Parallel()
	requireTestTier(t, testTierParity)
	libopustest.RequireOracle(t)

	helperPath, ok := getSILKCtrlHelperPath(t)
	if !ok {
		return
	}

	type silkCtrlKase struct {
		name      string
		channels  int
		frameSize int
		bitrate   int
		bandwidth types.Bandwidth
		opusBW    uint32
		nFrames   int
		cvbr      bool
	}
	kases := []silkCtrlKase{
		{"nb-mono-10ms-12k-vbr", 1, 480, 12000, types.BandwidthNarrowband, opusBandwidthNB, 8, false},
		// The failing build-config-matrix case: pure-SILK WB mono CVBR over a long
		// stream. Divergence first appears around frame 38, so run the full 100.
		{"wb-mono-20ms-24k-cvbr", 1, 960, 24000, types.BandwidthWideband, opusBandwidthWB, 100, true},
		{"wb-mono-20ms-24k-vbr", 1, 960, 24000, types.BandwidthWideband, opusBandwidthWB, 100, false},
	}
	for _, k := range kases {
		k := k
		t.Run(k.name, func(t *testing.T) {
			runSILKCtrlBisect(t, helperPath, k.channels, k.frameSize, k.bitrate, k.bandwidth, k.opusBW, k.nFrames, k.cvbr)
		})
	}
}

// TestSILKCBRControlOracle traces CBR matrix inputs with packet or range
// differences under the native v3 build. The C helper uses the selected scalar
// or SIMD libopus build and captures each channel's SILK control state before
// the rate-control loop.
func TestSILKCBRControlOracle(t *testing.T) {
	requireTestTier(t, testTierParity)
	libopustest.RequireOracle(t)
	helperPath, ok := getSILKCtrlHelperPath(t)
	if !ok {
		return
	}

	selectedCases := 0
	for _, tc := range cbrTestMatrix() {
		if tc.name != "SILK-MB-20ms-mono-24k" && tc.name != "SILK-WB-20ms-stereo-48k" {
			continue
		}
		selectedCases++
		t.Run(tc.name, func(t *testing.T) {
			frameCount := 48000 / tc.frameSize
			pcm, err := testsignal.GenerateEncoderSignalVariant(
				testsignal.EncoderVariantAMMultisineV1,
				48000,
				frameCount*tc.frameSize*tc.channels,
				tc.channels,
			)
			if err != nil {
				t.Fatalf("generate CBR matrix signal: %v", err)
			}
			inputID := cbrPCMIdentity(pcm)
			oraclePCM := quantizeCBRPCM(pcm)
			req := buildVBRCVBRRequest(
				2, 2052, // OPUS_APPLICATION_RESTRICTED_SILK
				48000, tc.channels, tc.frameSize, tc.bitrate,
				tc.oracleBW, opusSignalAuto, oraclePCM, frameCount,
			)
			copy(req[:4], []byte(silkCtrlInputMagic))
			oracle, err := runSILKCtrlOracle(helperPath, req, frameCount)
			if err != nil {
				t.Fatalf("CBR control oracle: %v", err)
			}
			normalHelperPath, err := cbrEncoderOraclePath()
			if err != nil {
				t.Fatalf("build standard CBR packet oracle: %v", err)
			}
			normalC, err := runCBROracleEncode(normalHelperPath, tc, pcm)
			if err != nil {
				t.Fatalf("run standard CBR packet oracle: %v", err)
			}
			if len(oracle.packets) != frameCount || len(oracle.ranges) != frameCount ||
				len(normalC.Packets) != frameCount || len(normalC.FinalRanges) != frameCount {
				t.Fatalf("C packet counts differ from input frames: trace=(%d packets,%d ranges), standard=(%d packets,%d ranges), want %d",
					len(oracle.packets), len(oracle.ranges), len(normalC.Packets), len(normalC.FinalRanges), frameCount)
			}
			firstTracePacketFrame, firstTracePacketByte, firstTraceRangeFrame := -1, -1, -1
			for frame := 0; frame < frameCount; frame++ {
				if firstTracePacketFrame < 0 {
					if at := firstCBRByteDifference(oracle.packets[frame], normalC.Packets[frame]); at >= 0 {
						firstTracePacketFrame, firstTracePacketByte = frame, at
					}
				}
				if firstTraceRangeFrame < 0 && oracle.ranges[frame] != normalC.FinalRanges[frame] {
					firstTraceRangeFrame = frame
				}
			}
			if firstTracePacketFrame >= 0 || firstTraceRangeFrame >= 0 {
				t.Fatalf("instrumented C helper changes standard CBR output: first packet difference frame=%d byte=%d; first final-range difference frame=%d",
					firstTracePacketFrame, firstTracePacketByte, firstTraceRangeFrame)
			}
			if len(oracle.ctrl) != frameCount*tc.channels {
				t.Fatalf("C control records=%d, want %d for %d frames × %d channels", len(oracle.ctrl), frameCount*tc.channels, frameCount, tc.channels)
			}

			enc := encoder.NewEncoder(48000, tc.channels)
			enc.SetMode(tc.gopusMode)
			enc.SetRestrictedSilkApplication(true)
			enc.SetLowDelay(false)
			enc.SetBandwidth(tc.bandwidth)
			enc.SetBitrate(tc.bitrate)
			enc.SetBitrateMode(encoder.ModeCBR)
			enc.SetComplexity(10)

			goOutput := cbrEncodedOutput{
				Packets:     make([][]byte, 0, frameCount),
				FinalRanges: make([]uint32, 0, frameCount),
			}
			var snapshots []silk.SILKCtrlSnapshot
			var goStages []silkEncodeStageRecord
			channelByEncoder := make(map[*silk.Encoder]int, tc.channels)
			nextChannel := 0
			currentFrame := -1
			var encodeErr error
			silk.WithSILKEncodeTraceSnapshotHooks(func(s silk.SILKCtrlSnapshot) {
				snapshots = append(snapshots, s)
			}, func(e *silk.Encoder, s silk.SILKEncodeStageSnapshot) {
				channel, ok := channelByEncoder[e]
				if !ok {
					channel = nextChannel
					channelByEncoder[e] = channel
					nextChannel++
				}
				if currentFrame != 6 && currentFrame != 13 {
					return
				}
				r := silkEncodeStageRecord{
					frame: int32(currentFrame), channel: int32(channel), iter: int32(s.Iteration),
					stage: int32(s.Stage - 1), tell: int32(s.Tell), rangeValue: s.Range,
					signalType: int32(s.SignalType), quantOffset: int32(s.QuantOffsetType), seed: int32(s.Seed),
					lagIndex: int32(s.LagIndex), contour: int32(s.ContourIndex),
					nlsfInterp: int32(s.NLSFInterpCoefQ2), perIndex: int32(s.PERIndex),
					ltpScale: int32(s.LTPScaleIndex),
				}
				for i := range r.gains {
					r.gains[i] = int32(s.GainIndices[i])
					r.ltp[i] = int32(s.LTPIndices[i])
				}
				for i := range r.nlsf {
					r.nlsf[i] = int32(s.NLSFIndices[i])
				}
				if s.Stage == silk.SILKEncodeAfterNSQ {
					r.pulses = append([]int8(nil), s.Pulses...)
				}
				goStages = append(goStages, r)
			}, func() {
				for frame := 0; frame < frameCount; frame++ {
					currentFrame = frame
					start := frame * tc.frameSize * tc.channels
					end := start + tc.frameSize*tc.channels
					packet, err := enc.Encode(oraclePCM[start:end], tc.frameSize)
					if err != nil {
						encodeErr = fmt.Errorf("frame %d: %w", frame, err)
						return
					}
					goOutput.Packets = append(goOutput.Packets, append([]byte(nil), packet...))
					goOutput.FinalRanges = append(goOutput.FinalRanges, enc.FinalRange())
				}
			})
			if encodeErr != nil {
				t.Fatal(encodeErr)
			}
			if len(snapshots) != len(oracle.ctrl) {
				t.Fatalf("Go control snapshots=%d, C records=%d (input=%s)", len(snapshots), len(oracle.ctrl), inputID)
			}

			t.Logf("CBR trace input=AMMultisineV1/%s frames=%d channels=%d controls=%d", inputID, frameCount, tc.channels, len(oracle.ctrl))
			controlDivergence := false
			for i, r := range oracle.ctrl {
				wantFrame := int32(i / tc.channels)
				wantChannel := int32(i % tc.channels)
				if r.opusFrame != wantFrame || r.channel != wantChannel {
					t.Fatalf("C trace order record%d=(frame%d/channel%d), want frame%d/channel%d", i, r.opusFrame, r.channel, wantFrame, wantChannel)
				}
				wantUseCBR := int32(1)
				if tc.channels == 2 && r.channel == 0 {
					wantUseCBR = 0
				}
				if r.useCBR != wantUseCBR {
					t.Fatalf("C frame%d/channel%d useCBR=%d, want %d for this SILK stream", r.opusFrame, r.channel, r.useCBR, wantUseCBR)
				}
				if diff := silkCBRControlDifference(r, snapshots[i]); diff != "" {
					t.Errorf("first CBR control divergence frame%d/channel%d input=%s: %s", r.opusFrame, r.channel, inputID, diff)
					controlDivergence = true
					break
				}
			}
			if !controlDivergence {
				t.Logf("SILK control snapshots match exactly across %d channel-frames", len(snapshots))
			}
			if len(oracle.stages) == 0 {
				t.Errorf("C SILK stage trace is empty for frames 6 and 13")
			} else if diff := firstSILKEncodeStageDifference(oracle.stages, goStages); diff != "" {
				t.Errorf("first SILK NSQ/index/pulse stage divergence input=%s: %s", inputID, diff)
			} else {
				t.Logf("SILK NSQ/index/pulse stages match across %d witness events", len(goStages))
			}

			firstPacketFrame, firstPacketByte, firstRangeFrame := -1, -1, -1
			for frame := 0; frame < frameCount; frame++ {
				if firstPacketFrame < 0 {
					if at := firstCBRByteDifference(goOutput.Packets[frame], oracle.packets[frame]); at >= 0 {
						firstPacketFrame, firstPacketByte = frame, at
					}
				}
				if firstRangeFrame < 0 && goOutput.FinalRanges[frame] != oracle.ranges[frame] {
					firstRangeFrame = frame
				}
			}
			if firstPacketFrame >= 0 || firstRangeFrame >= 0 {
				t.Errorf("CBR output first packet difference frame=%d byte=%d; first final-range difference frame=%d", firstPacketFrame, firstPacketByte, firstRangeFrame)
			} else {
				t.Logf("CBR packet/range output matches across %d frames", frameCount)
			}
		})
	}
	if selectedCases != 2 {
		t.Fatalf("selected %d CBR trace witnesses, want exactly 2", selectedCases)
	}
}

func firstSILKEncodeStageDifference(cRecords, goRecords []silkEncodeStageRecord) string {
	limit := min(len(cRecords), len(goRecords))
	for i := 0; i < limit; i++ {
		c, g := cRecords[i], goRecords[i]
		where := fmt.Sprintf("record=%d frame=%d channel=%d iter=%d stage=%d", i, c.frame, c.channel, c.iter, c.stage)
		if c.frame != g.frame || c.channel != g.channel || c.iter != g.iter || c.stage != g.stage {
			return fmt.Sprintf("%s ordering C=(frame%d/channel%d/iter%d/stage%d) Go=(frame%d/channel%d/iter%d/stage%d)",
				where, c.frame, c.channel, c.iter, c.stage, g.frame, g.channel, g.iter, g.stage)
		}
		if c.tell != g.tell || c.rangeValue != g.rangeValue {
			return fmt.Sprintf("%s tell/range C=%d/%08x Go=%d/%08x", where, c.tell, c.rangeValue, g.tell, g.rangeValue)
		}
		if c.signalType != g.signalType || c.quantOffset != g.quantOffset || c.seed != g.seed ||
			c.lagIndex != g.lagIndex || c.contour != g.contour || c.nlsfInterp != g.nlsfInterp ||
			c.perIndex != g.perIndex || c.ltpScale != g.ltpScale {
			return fmt.Sprintf("%s core indices C=(sig%d qoff%d seed%d lag%d contour%d interp%d per%d scale%d) Go=(sig%d qoff%d seed%d lag%d contour%d interp%d per%d scale%d)",
				where,
				c.signalType, c.quantOffset, c.seed, c.lagIndex, c.contour, c.nlsfInterp, c.perIndex, c.ltpScale,
				g.signalType, g.quantOffset, g.seed, g.lagIndex, g.contour, g.nlsfInterp, g.perIndex, g.ltpScale)
		}
		for j := range c.gains {
			if c.gains[j] != g.gains[j] {
				return fmt.Sprintf("%s GainsIndices[%d] C=%d Go=%d", where, j, c.gains[j], g.gains[j])
			}
			if c.ltp[j] != g.ltp[j] {
				return fmt.Sprintf("%s LTPIndex[%d] C=%d Go=%d", where, j, c.ltp[j], g.ltp[j])
			}
		}
		for j := range c.nlsf {
			if c.nlsf[j] != g.nlsf[j] {
				return fmt.Sprintf("%s NLSFIndices[%d] C=%d Go=%d", where, j, c.nlsf[j], g.nlsf[j])
			}
		}
		for j := 0; j < min(len(c.pulses), len(g.pulses)); j++ {
			if c.pulses[j] != g.pulses[j] {
				return fmt.Sprintf("%s pulses[%d] C=%d Go=%d (lengths %d/%d)", where, j, c.pulses[j], g.pulses[j], len(c.pulses), len(g.pulses))
			}
		}
		if len(c.pulses) != len(g.pulses) {
			return fmt.Sprintf("%s pulse lengths C=%d Go=%d", where, len(c.pulses), len(g.pulses))
		}
	}
	if len(cRecords) != len(goRecords) {
		return fmt.Sprintf("stage record counts C=%d Go=%d", len(cRecords), len(goRecords))
	}
	return ""
}

func silkCBRControlDifference(r silkCtrlRecord, s silk.SILKCtrlSnapshot) string {
	if int(r.signalType) != s.SignalType {
		return fmt.Sprintf("signalType C=%d Go=%d", r.signalType, s.SignalType)
	}
	if int(r.quantOffset) != s.QuantOffset {
		return fmt.Sprintf("quantOffset C=%d Go=%d", r.quantOffset, s.QuantOffset)
	}
	if int(r.nbSubfr) != s.NbSubfr {
		return fmt.Sprintf("nbSubfr C=%d Go=%d", r.nbSubfr, s.NbSubfr)
	}
	if r.snrDBQ7 != s.SNRdBQ7 {
		return fmt.Sprintf("SNR_dB_Q7 C=%d Go=%d", r.snrDBQ7, s.SNRdBQ7)
	}
	if r.speechActQ8 != s.SpeechActivQ8 {
		return fmt.Sprintf("speech_activity_Q8 C=%d Go=%d", r.speechActQ8, s.SpeechActivQ8)
	}
	if math.Float32bits(r.codingQuality) != math.Float32bits(s.CodingQuality) {
		return fmt.Sprintf("coding_quality C=%08x Go=%08x", math.Float32bits(r.codingQuality), math.Float32bits(s.CodingQuality))
	}
	if math.Float32bits(r.inputQuality) != math.Float32bits(s.InputQuality) {
		return fmt.Sprintf("input_quality C=%08x Go=%08x", math.Float32bits(r.inputQuality), math.Float32bits(s.InputQuality))
	}
	if silkFloat2intRound(r.lambda*1024.0) != s.LambdaQ10 {
		return fmt.Sprintf("Lambda_Q10 C=%d Go=%d (C float=%08x)", silkFloat2intRound(r.lambda*1024.0), s.LambdaQ10, math.Float32bits(r.lambda))
	}
	for i := range r.inQBandsQ15 {
		if r.inQBandsQ15[i] != s.InQBandsQ15[i] {
			return fmt.Sprintf("input_quality_bands_Q15[%d] C=%d Go=%d", i, r.inQBandsQ15[i], s.InQBandsQ15[i])
		}
	}
	for i := 0; i < int(r.nbSubfr); i++ {
		// wrappers_FLP.c:silk_NSQ_wrapper_FLP multiplies in silk_float (float32)
		// and calls silk_float2int; float_cast.h rounds that float to nearest-even
		// on the supported SSE and AArch64 paths. Keep the Go conversion in range
		// because the C wrapper does not clamp Gains_Q16 before conversion.
		scaledGainQ16 := r.gains[i] * 65536.0
		int32Limit := float32(1 << 31)
		if scaledGainQ16 >= int32Limit || scaledGainQ16 < -int32Limit {
			return fmt.Sprintf("Gains_Q16[%d] source value is outside int32 conversion range: float=%08x", i, math.Float32bits(r.gains[i]))
		}
		refGainQ16 := silkFloat2intRound(scaledGainQ16)
		if refGainQ16 != s.GainsQ16[i] {
			return fmt.Sprintf("Gains_Q16[%d] C=%d Go=%d (C float=%08x)", i, refGainQ16, s.GainsQ16[i], math.Float32bits(r.gains[i]))
		}
		if got := silkFloat2intRound(r.tilt[i] * 16384.0); got != s.TiltQ14[i] {
			return fmt.Sprintf("Tilt_Q14[%d] C=%d Go=%d", i, got, s.TiltQ14[i])
		}
		if got := silkFloat2intRound(r.harmShapeGain[i] * 16384.0); got != s.HarmShapeQ14[i] {
			return fmt.Sprintf("HarmShapeGain_Q14[%d] C=%d Go=%d", i, got, s.HarmShapeQ14[i])
		}
		refLF := (silkFloat2intRound(r.lfAR[i]*16384.0) << 16) | int32(uint16(silkFloat2intRound(r.lfMA[i]*16384.0)))
		if refLF != s.LFShpQ14[i] {
			return fmt.Sprintf("LF_shp_Q14[%d] C=%08x Go=%08x", i, refLF, s.LFShpQ14[i])
		}
		if r.pitchL[i] != s.PitchL[i] {
			return fmt.Sprintf("pitchL[%d] C=%d Go=%d", i, r.pitchL[i], s.PitchL[i])
		}
		for j := 0; j < silkCtrlMaxShapeLPC; j++ {
			got := int16(silkFloat2intRound(r.ar[i*silkCtrlMaxShapeLPC+j] * 8192.0))
			if got != s.ARShpQ13[i*silkCtrlMaxShapeLPC+j] {
				return fmt.Sprintf("AR_Q13[%d][%d] C=%d Go=%d", i, j, got, s.ARShpQ13[i*silkCtrlMaxShapeLPC+j])
			}
		}
	}
	return ""
}

func runSILKCtrlBisect(t *testing.T, helperPath string, channels, frameSize, bitrate int, bandwidth types.Bandwidth, opusBW uint32, nFrames int, cvbr bool) {
	pcm := makeVBRCVBRTestPCM(nFrames, frameSize, channels)

	oracleMode := oracleModeVBR
	if cvbr {
		oracleMode = oracleModeCVBR
	}
	req := buildVBRCVBRRequest(
		oracleMode, opusApplicationVoIP,
		48000, channels, frameSize, bitrate,
		opusBW, opusSignalVoice,
		pcm, nFrames,
	)
	// Reuse the GVCI builder but rewrite the magic to GSCI.
	copy(req[0:4], []byte(silkCtrlInputMagic))

	oracle, err := runSILKCtrlOracle(helperPath, req, nFrames)
	if err != nil {
		t.Fatalf("oracle: %v", err)
	}

	// Drive gopus with the snapshot hook.
	var snaps []silk.SILKCtrlSnapshot
	enc, err := gopus.NewEncoder(gopus.EncoderConfig{
		SampleRate:  48000,
		Channels:    channels,
		Application: gopus.ApplicationVoIP,
	})
	if err != nil {
		t.Fatalf("new encoder: %v", err)
	}
	mustNoErr(t, enc.SetFrameSize(frameSize))
	mustNoErr(t, enc.SetBitrate(bitrate))
	mustNoErr(t, enc.SetBandwidth(bandwidth))
	mustNoErr(t, enc.SetSignal(types.SignalVoice))
	mustNoErr(t, enc.SetComplexity(10))
	enc.SetVBR(true)
	enc.SetVBRConstraint(cvbr)

	buf := make([]byte, 4000)
	goLens := make([]int, nFrames)
	silk.WithSILKCtrlSnapshotHook(func(s silk.SILKCtrlSnapshot) {
		snaps = append(snaps, s)
	}, func() {
		for i := 0; i < nFrames; i++ {
			frame := pcm[i*frameSize*channels : (i+1)*frameSize*channels]
			n, encErr := enc.Encode(frame, buf)
			if encErr != nil {
				t.Fatalf("encode frame %d: %v", i, encErr)
			}
			goLens[i] = n
		}
	})

	// Pair the ctrl records (mono => one per opus frame, channel 0) with snaps.
	var refCtrl []silkCtrlRecord
	for _, r := range oracle.ctrl {
		if r.channel == 0 {
			refCtrl = append(refCtrl, r)
		}
	}

	t.Logf("oracle packets: %v", pktLens(oracle.packets))
	t.Logf("gopus  packets: %v", goLens)
	t.Logf("ctrl records: ref=%d gopus_snaps=%d", len(refCtrl), len(snaps))

	n := len(refCtrl)
	if len(snaps) < n {
		n = len(snaps)
	}
	for i := 0; i < n; i++ {
		r := refCtrl[i]
		s := snaps[i]
		nbs := int(r.nbSubfr)
		var diffs []string
		if r.snrDBQ7 != s.SNRdBQ7 {
			diffs = append(diffs, fmt.Sprintf("SNR_dB_Q7 ref=%d go=%d", r.snrDBQ7, s.SNRdBQ7))
		}
		if r.speechActQ8 != s.SpeechActivQ8 {
			diffs = append(diffs, fmt.Sprintf("speechAct_Q8 ref=%d go=%d", r.speechActQ8, s.SpeechActivQ8))
		}
		for k := 0; k < 4; k++ {
			if r.inQBandsQ15[k] != s.InQBandsQ15[k] {
				diffs = append(diffs, fmt.Sprintf("inQBand_Q15[%d] ref=%d go=%d", k, r.inQBandsQ15[k], s.InQBandsQ15[k]))
			}
		}
		if r.codingQuality != s.CodingQuality {
			diffs = append(diffs, fmt.Sprintf("codingQuality ref=%.9g go=%.9g", r.codingQuality, s.CodingQuality))
		}
		if r.inputQuality != s.InputQuality {
			diffs = append(diffs, fmt.Sprintf("inputQuality ref=%.9g go=%.9g", r.inputQuality, s.InputQuality))
		}
		if int(r.signalType) != s.SignalType {
			diffs = append(diffs, fmt.Sprintf("signalType ref=%d go=%d", r.signalType, s.SignalType))
		}
		if int(r.quantOffset) != s.QuantOffset {
			diffs = append(diffs, fmt.Sprintf("quantOffset ref=%d go=%d", r.quantOffset, s.QuantOffset))
		}
		// Lambda_Q10: oracle stores float Lambda; convert with truncation toward
		// nearest (libopus silk_float2int = round). gopus stores LambdaQ10 directly.
		refLambdaQ10 := int32(math.RoundToEven(float64(r.lambda) * 1024.0))
		if refLambdaQ10 != s.LambdaQ10 {
			diffs = append(diffs, fmt.Sprintf("LambdaQ10 ref~=%d(%.6f) go=%d", refLambdaQ10, r.lambda, s.LambdaQ10))
		}
		for k := 0; k < nbs; k++ {
			refG := int32(float64(r.gains[k]) * 65536.0) // gopus uses truncation for Gains_Q16
			if absI32(refG-s.GainsQ16[k]) > 1 {
				diffs = append(diffs, fmt.Sprintf("Gains_Q16[%d] ref=%d(go=%d)", k, refG, s.GainsQ16[k]))
			}
			refTilt := silkFloat2intRound(r.tilt[k] * 16384.0)
			if absI32(refTilt-s.TiltQ14[k]) > 0 {
				diffs = append(diffs, fmt.Sprintf("Tilt_Q14[%d] ref=%d go=%d", k, refTilt, s.TiltQ14[k]))
			}
			refHSG := silkFloat2intRound(r.harmShapeGain[k] * 16384.0)
			if absI32(refHSG-s.HarmShapeQ14[k]) > 0 {
				diffs = append(diffs, fmt.Sprintf("HarmShapeGain_Q14[%d] ref=%d go=%d", k, refHSG, s.HarmShapeQ14[k]))
			}
			refLF := (silkFloat2intRound(r.lfAR[k]*16384.0) << 16) | int32(uint16(silkFloat2intRound(r.lfMA[k]*16384.0)))
			if refLF != s.LFShpQ14[k] {
				diffs = append(diffs, fmt.Sprintf("LF_shp_Q14[%d] ref=%d go=%d", k, refLF, s.LFShpQ14[k]))
			}
			if r.pitchL[k] != s.PitchL[k] {
				diffs = append(diffs, fmt.Sprintf("pitchL[%d] ref=%d go=%d", k, r.pitchL[k], s.PitchL[k]))
			}
			for j := 0; j < silkCtrlMaxShapeLPC; j++ {
				refAR := silkFloat2intRound(r.ar[k*silkCtrlMaxShapeLPC+j] * 8192.0)
				goAR := int32(s.ARShpQ13[k*silkCtrlMaxShapeLPC+j])
				if absI32(refAR-goAR) > 0 {
					diffs = append(diffs, fmt.Sprintf("AR_Q13[%d][%d] ref=%d go=%d", k, j, refAR, goAR))
					break
				}
			}
		}
		if len(diffs) > 0 {
			t.Logf("FRAME %d FIRST DIVERGENCE (ref nBytes=%d): %v", i, r.nBytes, diffs)
			t.Logf("  ref: sig=%d qoff=%d lambda=%.6f gainsUnq=%v predGain=%.4f ltpCG=%.4f inQ=%.6f cdQ=%.6f",
				r.signalType, r.quantOffset, r.lambda, r.gainsUnqQ16[:nbs], r.predGain, r.ltpredCodGain, r.inputQuality, r.codingQuality)
			t.Logf("  go rate-control: SNR_dB_Q7=%d targetRateBps=%d", s.SNRdBQ7, s.TargetRateBps)
			return
		}
	}
	t.Logf("NO control-state divergence across %d frames; size delta (if any) is in NSQ/encode bit usage", n)
}

func pktLens(pkts [][]byte) []int {
	out := make([]int, len(pkts))
	for i, p := range pkts {
		out[i] = len(p)
	}
	return out
}

func absI32(v int32) int32 {
	if v < 0 {
		return -v
	}
	return v
}

func silkFloat2intRound(f float32) int32 {
	return int32(math.RoundToEven(float64(f)))
}

func mustNoErr(t *testing.T, err error) {
	t.Helper()
	if err != nil {
		t.Fatalf("setup: %v", err)
	}
}
