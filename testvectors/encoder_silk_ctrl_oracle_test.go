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
	"runtime"
	"testing"

	gopus "github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/opusmath"
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
		cFlags := []string{"-DHAVE_CONFIG_H", "-O2", "-DNDEBUG"}
		if runtime.GOOS == "linux" {
			cFlags = append(cFlags,
				"-Wl,--wrap=silk_find_LPC_FLP",
				"-Wl,--wrap=silk_LTP_analysis_filter_FLP",
			)
		}
		return libopustest.BuildPublicAPIHelper(libopustest.CHelperConfig{
			Label:      "silk ctrl",
			OutputBase: "gopus_libopus_silk_ctrl",
			SourceFile: "libopus_silk_ctrl_info.c",
			CFlags:     cFlags,
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
	packets  [][]byte
	ranges   []uint32
	ctrl     []silkCtrlRecord
	stages   []silkEncodeStageRecord
	lpcCalls []silkFindLPCRecord
	ltpCalls []silkLTPCallRecord
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

type silkFindLPCRecord struct {
	frame, channel, framesEncoded   int32
	apiFsHz, fsKHz                  int32
	frameLength, subfrLength        int32
	nbSubfr, order                  int32
	useInterp, firstFrameAfterReset int32
	arch                            int32
	minInvGain                      float32
	prevNLSF                        [silkCtrlMaxLPC]int32
	input                           []float32
	selectedInterp                  int32
	nlsf                            [silkCtrlMaxLPC]int32
}

type silkLTPCallRecord struct {
	frame, channel, signalType, filterCalled int32
	subfrLength, nbSubfr, preLength          int32
	outputCount                              int
	gains                                    [silkCtrlMaxNbSubfr]float32
	subframes                                []silkLTPSubframeRecord
}

type silkLTPSubframeRecord struct {
	pitchL  int32
	invGain float32
	taps    [silkCtrlLTPOrder]float32
	samples []silkLTPOutputSampleRecord
}

type silkLTPOutputSampleRecord struct {
	x      float32
	lags   [silkCtrlLTPOrder]float32
	result float32
}

type goSILKFindLPCRecord struct {
	frame, channel int32
	snapshot       silk.SILKNLSFInterpolationSnapshot
}

type goSILKLTPCallRecord struct {
	frame, channel int32
	snapshot       silk.SILKLTPAnalysisTraceSnapshot
}

func TestParseSILKCtrlOracleOutputGSCO5LTPGains(t *testing.T) {
	makeOutput := func(gainCount int) []byte {
		var raw []byte
		putU32 := func(value uint32) {
			var b [4]byte
			binary.LittleEndian.PutUint32(b[:], value)
			raw = append(raw, b[:]...)
		}
		putI32 := func(value int32) { putU32(uint32(value)) }
		putF32 := func(value float32) { putU32(math.Float32bits(value)) }
		raw = append(raw, silkCtrlOutputMagic...)
		putU32(5) // GSCO v5
		putU32(1) // one packet
		putU32(0) // empty packet
		putU32(0) // final range
		putU32(0) // control records
		putU32(0) // stage records
		putI32(0) // stage overflow
		putU32(0) // FindLPC records
		putI32(0) // FindLPC overflow
		putU32(1) // one LTP context
		putI32(6) // frame
		putI32(0) // channel
		putI32(2) // voiced
		putI32(1) // filter called
		putI32(1) // subframe length
		putI32(4) // four subframes
		putI32(1) // pre length
		putU32(8) // 4 * (subframe + pre)
		for i := 0; i < gainCount; i++ {
			putF32(float32(i+1) * 0.25)
		}
		if gainCount != 4 {
			return raw
		}
		for k := 0; k < 4; k++ {
			putI32(20 + int32(k)) // pitch lag
			putF32(0.5)           // inverse gain
			for j := 0; j < silkCtrlLTPOrder; j++ {
				putF32(float32(j) / 16)
			}
		}
		for i := 0; i < 8; i++ {
			putF32(float32(i) / 8) // x
			for j := 0; j < silkCtrlLTPOrder; j++ {
				putF32(float32(j) / 32) // lag
			}
			putF32(float32(i) / 16) // result
		}
		putI32(0) // LTP overflow
		return raw
	}

	parsed, err := parseSILKCtrlOracleOutput(makeOutput(4), 1)
	if err != nil {
		t.Fatalf("parse valid GSCO v5 four-subframe context: %v", err)
	}
	if len(parsed.ltpCalls) != 1 || len(parsed.ltpCalls[0].subframes) != 4 {
		t.Fatalf("parsed LTP context/subframes=%d/%d, want 1/4", len(parsed.ltpCalls), len(parsed.ltpCalls[0].subframes))
	}
	for i, want := range [4]float32{0.25, 0.5, 0.75, 1.0} {
		if got := parsed.ltpCalls[0].gains[i]; math.Float32bits(got) != math.Float32bits(want) {
			t.Fatalf("raw gain[%d]=%08x, want %08x", i, math.Float32bits(got), math.Float32bits(want))
		}
	}
	if _, err := parseSILKCtrlOracleOutput(makeOutput(2), 1); err == nil {
		t.Fatal("truncated GSCO v5 gain header was accepted")
	}
}

func runSILKCtrlOracle(helperPath string, req []byte, nFrames int) (*silkCtrlOracleOut, error) {
	raw, err := libopustest.RunHelper(helperPath, req)
	if err != nil {
		return nil, fmt.Errorf("run silk ctrl oracle: %w", err)
	}
	return parseSILKCtrlOracleOutput(raw, nFrames)
}

func parseSILKCtrlOracleOutput(raw []byte, nFrames int) (*silkCtrlOracleOut, error) {
	if len(raw) < 12 || string(raw[0:4]) != silkCtrlOutputMagic {
		return nil, fmt.Errorf("bad oracle response magic")
	}
	version := binary.LittleEndian.Uint32(raw[4:8])
	if version != 5 {
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
	nLPC := int(rd())
	if nLPC < 0 || nLPC > 8 {
		return nil, fmt.Errorf("invalid C FindLPC trace count %d", nLPC)
	}
	for i := 0; i < nLPC; i++ {
		var r silkFindLPCRecord
		r.frame, r.channel, r.framesEncoded = ri(), ri(), ri()
		r.apiFsHz, r.fsKHz = ri(), ri()
		r.frameLength, r.subfrLength = ri(), ri()
		r.nbSubfr, r.order = ri(), ri()
		r.useInterp, r.firstFrameAfterReset = ri(), ri()
		r.arch = ri()
		r.minInvGain = rf()
		if r.order <= 0 || r.order > silkCtrlMaxLPC || r.nbSubfr <= 0 || r.nbSubfr > silkCtrlMaxNbSubfr ||
			r.subfrLength <= 0 || r.fsKHz <= 0 {
			return nil, fmt.Errorf("invalid C FindLPC dimensions fs=%d frame=%d subframe=%d subframes=%d order=%d",
				r.fsKHz, r.frameLength, r.subfrLength, r.nbSubfr, r.order)
		}
		for j := range r.prevNLSF {
			r.prevNLSF[j] = ri()
		}
		inputCount := int(rd())
		if inputCount < 0 || inputCount > 1024 || inputCount > (len(raw)-off)/4 {
			return nil, fmt.Errorf("invalid C FindLPC input count %d", inputCount)
		}
		wantInputCount := int(r.nbSubfr) * (int(r.subfrLength) + int(r.order))
		if inputCount != wantInputCount {
			return nil, fmt.Errorf("C FindLPC input count=%d, want %d from frame dimensions", inputCount, wantInputCount)
		}
		r.input = make([]float32, inputCount)
		for j := range r.input {
			r.input[j] = rf()
		}
		r.selectedInterp = ri()
		for j := range r.nlsf {
			r.nlsf[j] = ri()
		}
		out.lpcCalls = append(out.lpcCalls, r)
	}
	if overflow := ri(); overflow != 0 {
		return nil, fmt.Errorf("C FindLPC trace overflowed its bounded record buffer")
	}
	nLTP := int(rd())
	if nLTP < 0 || nLTP > 8 {
		return nil, fmt.Errorf("invalid C LTP trace count %d", nLTP)
	}
	for i := 0; i < nLTP; i++ {
		var r silkLTPCallRecord
		r.frame, r.channel, r.signalType, r.filterCalled = ri(), ri(), ri(), ri()
		r.subfrLength, r.nbSubfr, r.preLength = ri(), ri(), ri()
		r.outputCount = int(rd())
		if r.frame < 0 || (r.frame != 6 && r.frame != 13) || r.channel < 0 || r.channel > 1 ||
			r.signalType < 0 || r.signalType > 2 || (r.filterCalled != 0 && r.filterCalled != 1) ||
			r.subfrLength <= 0 || r.nbSubfr <= 0 || r.nbSubfr > silkCtrlMaxNbSubfr ||
			r.preLength <= 0 || r.preLength > silkCtrlMaxLPC {
			return nil, fmt.Errorf("invalid C LTP trace dimensions frame=%d channel=%d type=%d called=%d subframe=%d subframes=%d pre=%d",
				r.frame, r.channel, r.signalType, r.filterCalled, r.subfrLength, r.nbSubfr, r.preLength)
		}
		wantOutputCount := int(r.nbSubfr) * (int(r.subfrLength) + int(r.preLength))
		gainBytes := int(r.nbSubfr) * 4
		if len(raw)-off < gainBytes {
			return nil, fmt.Errorf("truncated C LTP gains for frame=%d/channel=%d", r.frame, r.channel)
		}
		for k := 0; k < int(r.nbSubfr); k++ {
			r.gains[k] = rf()
		}
		if r.filterCalled == 0 {
			if r.outputCount != 0 {
				return nil, fmt.Errorf("unvoiced C LTP context frame=%d/channel=%d has output count %d", r.frame, r.channel, r.outputCount)
			}
		} else {
			perSubframeHeaderBytes := int(r.nbSubfr) * 7 * 4
			remaining := len(raw) - off
			if r.outputCount != wantOutputCount || r.outputCount > 1024 || remaining < perSubframeHeaderBytes ||
				r.outputCount > (remaining-perSubframeHeaderBytes)/28 {
				return nil, fmt.Errorf("invalid C LTP output count %d, want %d (remaining=%d)", r.outputCount, wantOutputCount, remaining)
			}
		}
		if r.filterCalled == 1 {
			r.subframes = make([]silkLTPSubframeRecord, r.nbSubfr)
			for k := range r.subframes {
				sf := &r.subframes[k]
				sf.pitchL = ri()
				sf.invGain = rf()
				for j := range sf.taps {
					sf.taps[j] = rf()
				}
				sf.samples = make([]silkLTPOutputSampleRecord, int(r.subfrLength)+int(r.preLength))
			}
			for k := range r.subframes {
				for j := range r.subframes[k].samples {
					sample := &r.subframes[k].samples[j]
					sample.x = rf()
					for tap := range sample.lags {
						sample.lags[tap] = rf()
					}
					sample.result = rf()
				}
			}
		}
		out.ltpCalls = append(out.ltpCalls, r)
	}
	if overflow := ri(); overflow != 0 {
		return nil, fmt.Errorf("C LTP trace overflowed its bounded record buffer")
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
			var goLPCCalls []goSILKFindLPCRecord
			var goLTPCalls []goSILKLTPCallRecord
			channelByEncoder := make(map[*silk.Encoder]int, tc.channels)
			nextChannel := 0
			currentFrame := -1
			var encodeErr error
			channelForEncoder := func(e *silk.Encoder) int {
				channel, ok := channelByEncoder[e]
				if !ok {
					channel = nextChannel
					channelByEncoder[e] = channel
					nextChannel++
				}
				return channel
			}
			silk.WithSILKLTPAnalysisTraceHook(func(e *silk.Encoder, s silk.SILKLTPAnalysisTraceSnapshot) {
				channel := channelForEncoder(e)
				if (currentFrame != 6 && currentFrame != 13) || channel != 0 {
					return
				}
				s.PitchBuffer = append([]float32(nil), s.PitchBuffer...)
				s.Residual = append([]float32(nil), s.Residual...)
				goLTPCalls = append(goLTPCalls, goSILKLTPCallRecord{
					frame: int32(currentFrame), channel: int32(channel), snapshot: s,
				})
			}, func() {
				silk.WithSILKNLSFInterpolationTraceHook(func(e *silk.Encoder, s silk.SILKNLSFInterpolationSnapshot) {
					channel := channelForEncoder(e)
					if (currentFrame != 6 && currentFrame != 13) || channel != 0 {
						return
					}
					s.Input = append([]float32(nil), s.Input...)
					goLPCCalls = append(goLPCCalls, goSILKFindLPCRecord{
						frame: int32(currentFrame), channel: int32(channel), snapshot: s,
					})
				}, func() {
					silk.WithSILKEncodeTraceSnapshotHooks(func(s silk.SILKCtrlSnapshot) {
						snapshots = append(snapshots, s)
					}, func(e *silk.Encoder, s silk.SILKEncodeStageSnapshot) {
						channel := channelForEncoder(e)
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
				})
			})
			if encodeErr != nil {
				t.Fatal(encodeErr)
			}
			plainEnc := encoder.NewEncoder(48000, tc.channels)
			plainEnc.SetMode(tc.gopusMode)
			plainEnc.SetRestrictedSilkApplication(true)
			plainEnc.SetLowDelay(false)
			plainEnc.SetBandwidth(tc.bandwidth)
			plainEnc.SetBitrate(tc.bitrate)
			plainEnc.SetBitrateMode(encoder.ModeCBR)
			plainEnc.SetComplexity(10)
			plainOutput := cbrEncodedOutput{
				Packets:     make([][]byte, 0, frameCount),
				FinalRanges: make([]uint32, 0, frameCount),
			}
			for frame := 0; frame < frameCount; frame++ {
				start := frame * tc.frameSize * tc.channels
				end := start + tc.frameSize*tc.channels
				packet, err := plainEnc.Encode(oraclePCM[start:end], tc.frameSize)
				if err != nil {
					t.Fatalf("untraced Go frame %d: %v", frame, err)
				}
				plainOutput.Packets = append(plainOutput.Packets, append([]byte(nil), packet...))
				plainOutput.FinalRanges = append(plainOutput.FinalRanges, plainEnc.FinalRange())
			}
			firstHookPacketFrame, firstHookPacketByte, firstHookRangeFrame := -1, -1, -1
			for frame := 0; frame < frameCount; frame++ {
				if firstHookPacketFrame < 0 {
					if at := firstCBRByteDifference(goOutput.Packets[frame], plainOutput.Packets[frame]); at >= 0 {
						firstHookPacketFrame, firstHookPacketByte = frame, at
					}
				}
				if firstHookRangeFrame < 0 && goOutput.FinalRanges[frame] != plainOutput.FinalRanges[frame] {
					firstHookRangeFrame = frame
				}
			}
			if firstHookPacketFrame >= 0 || firstHookRangeFrame >= 0 {
				t.Fatalf("Go trace hook changes output: first packet difference frame=%d byte=%d; first final-range difference frame=%d",
					firstHookPacketFrame, firstHookPacketByte, firstHookRangeFrame)
			}
			t.Logf("traced/untraced Go packet and range output matches across %d frames", frameCount)
			if len(snapshots) != len(oracle.ctrl) {
				t.Fatalf("Go control snapshots=%d, C records=%d (input=%s)", len(snapshots), len(oracle.ctrl), inputID)
			}
			if runtime.GOOS == "linux" {
				wantCalls := 2 * tc.channels
				if len(oracle.lpcCalls) != wantCalls {
					t.Fatalf("actual C FindLPC snapshots=%d, want %d for frames 6/13 across %d channels",
						len(oracle.lpcCalls), wantCalls, tc.channels)
				}
				if len(goLPCCalls) != 2 {
					t.Fatalf("Go FindLPC snapshots=%d, want 2 for frames 6/13 channel 0", len(goLPCCalls))
				}
				for _, g := range goLPCCalls {
					var c *silkFindLPCRecord
					for i := range oracle.lpcCalls {
						candidate := &oracle.lpcCalls[i]
						if candidate.frame == g.frame && candidate.channel == g.channel {
							c = candidate
							break
						}
					}
					if c == nil {
						t.Fatalf("missing actual C FindLPC snapshot for frame%d/channel%d", g.frame, g.channel)
					}
					if diff := firstSILKFindLPCActualDifference(*c, g); diff != "" {
						t.Logf("first actual C/Go FindLPC difference input=%s frame%d/channel%d: %s",
							inputID, g.frame, g.channel, diff)
					} else {
						t.Logf("actual C/Go FindLPC inputs and state match at frame%d/channel%d", g.frame, g.channel)
					}
				}
				if len(oracle.ltpCalls) != wantCalls {
					t.Fatalf("actual C LTP snapshots=%d, want %d for frames 6/13 across %d channels",
						len(oracle.ltpCalls), wantCalls, tc.channels)
				}
				if len(goLTPCalls) != 2 {
					t.Fatalf("Go LTP snapshots=%d, want 2 for frames 6/13 channel 0", len(goLTPCalls))
				}
				for _, g := range goLTPCalls {
					var c *silkLTPCallRecord
					for i := range oracle.ltpCalls {
						candidate := &oracle.ltpCalls[i]
						if candidate.frame == g.frame && candidate.channel == g.channel {
							c = candidate
							break
						}
					}
					if c == nil {
						t.Fatalf("missing actual C LTP snapshot for frame%d/channel%d", g.frame, g.channel)
					}
					inputDiff, resultDiff, goModelDiff, fmaModelDiff := compareSILKLTPActual(*c, g)
					if inputDiff != "" {
						t.Logf("actual C/Go LTP operand difference input=%s frame%d/channel%d: %s",
							inputID, g.frame, g.channel, inputDiff)
					} else {
						t.Logf("actual C/Go LTP operands match at frame%d/channel%d", g.frame, g.channel)
					}
					if resultDiff != "" {
						t.Logf("actual C/Go LTP output difference input=%s frame%d/channel%d: %s",
							inputID, g.frame, g.channel, resultDiff)
					}
					if goModelDiff != "" {
						t.Logf("Go source-order LTP model difference frame%d/channel%d: %s", g.frame, g.channel, goModelDiff)
					}
					if fmaModelDiff != "" {
						t.Logf("ordered float32 FMA LTP model difference frame%d/channel%d: %s", g.frame, g.channel, fmaModelDiff)
					}
				}
			} else {
				t.Log("actual C FindLPC/LTP call snapshots are available on Linux builds with linker wrapping")
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

func firstSILKFindLPCActualDifference(c silkFindLPCRecord, g goSILKFindLPCRecord) string {
	s := g.snapshot
	where := fmt.Sprintf("C frame%d/channel%d nFramesEncoded=%d Go packetFrame=%d", c.frame, c.channel, c.framesEncoded, s.FrameInPacket)
	if c.frame != g.frame || c.channel != g.channel {
		return fmt.Sprintf("%s ordering Go frame%d/channel%d", where, g.frame, g.channel)
	}
	if c.framesEncoded != s.FrameInPacket {
		return fmt.Sprintf("nFramesEncoded C=%d Go=%d", c.framesEncoded, s.FrameInPacket)
	}
	if c.apiFsHz != 48000 {
		return fmt.Sprintf("API sample rate C=%d Go test input=48000", c.apiFsHz)
	}
	goSubfrSamples := int32(s.SubframeLen - s.Order)
	if c.order != int32(s.Order) || c.nbSubfr != int32(s.NumSubframes) ||
		c.subfrLength != goSubfrSamples || c.frameLength != goSubfrSamples*int32(s.NumSubframes) {
		return fmt.Sprintf("dimensions C=(fs=%dkHz frame=%d subframe=%d subframes=%d order=%d) Go=(fs=%dkHz frame=%d subframe=%d subframes=%d order=%d)",
			c.fsKHz, c.frameLength, c.subfrLength, c.nbSubfr, c.order,
			goSubfrSamples/5, goSubfrSamples*int32(s.NumSubframes), goSubfrSamples, s.NumSubframes, s.Order)
	}
	if c.fsKHz != goSubfrSamples/5 {
		return fmt.Sprintf("internal sample rate C=%dkHz Go=%dkHz from %d samples per 5ms subframe", c.fsKHz, goSubfrSamples/5, goSubfrSamples)
	}
	if c.useInterp != 1 {
		return fmt.Sprintf("useInterpolatedNLSFs C=%d Go interpolation callback active", c.useInterp)
	}
	if c.firstFrameAfterReset != 0 {
		return fmt.Sprintf("first_frame_after_reset C=%d Go interpolation callback active", c.firstFrameAfterReset)
	}
	if math.Float32bits(c.minInvGain) != math.Float32bits(s.MinInvGain) {
		return fmt.Sprintf("minInvGain C=%08x Go=%08x", math.Float32bits(c.minInvGain), math.Float32bits(s.MinInvGain))
	}
	for i := 0; i < s.Order; i++ {
		if c.prevNLSF[i] != int32(s.PrevNLSFQ15[i]) {
			return fmt.Sprintf("prev_NLSFq_Q15[%d] C=%d Go=%d", i, c.prevNLSF[i], s.PrevNLSFQ15[i])
		}
	}
	if len(c.input) != len(s.Input) {
		return fmt.Sprintf("LPC input lengths C=%d Go=%d", len(c.input), len(s.Input))
	}
	for i := range c.input {
		cBits, gBits := math.Float32bits(c.input[i]), math.Float32bits(s.Input[i])
		if cBits != gBits {
			return fmt.Sprintf("LPC input[%d] C=%08x Go=%08x", i, cBits, gBits)
		}
	}
	if c.selectedInterp != s.SelectedIndex {
		return fmt.Sprintf("selected interpolation index C=%d Go=%d with matching input/state", c.selectedInterp, s.SelectedIndex)
	}
	if c.selectedInterp < 4 {
		for i := 0; i < s.Order; i++ {
			if c.nlsf[i] != int32(s.LastNLSFQ15[i]) {
				return fmt.Sprintf("output NLSF_Q15[%d] C=%d Go last-half=%d", i, c.nlsf[i], s.LastNLSFQ15[i])
			}
		}
	}
	return ""
}

func compareSILKLTPActual(c silkLTPCallRecord, g goSILKLTPCallRecord) (inputDiff, resultDiff, goModelDiff, fmaModelDiff string) {
	s := g.snapshot
	where := fmt.Sprintf("C frame%d/channel%d Go packetFrame=%d", c.frame, c.channel, s.FrameInPacket)
	if c.frame != g.frame || c.channel != g.channel {
		return fmt.Sprintf("%s ordering Go frame%d/channel%d", where, g.frame, g.channel), "", "", ""
	}
	if c.signalType != 2 || c.filterCalled != 1 || s.SignalType != 2 {
		return fmt.Sprintf("expected voiced LTP filter call: C signal/called=%d/%d Go signal=%d", c.signalType, c.filterCalled, s.SignalType), "", "", ""
	}
	if c.subfrLength != s.SubframeSamples || c.nbSubfr != s.NumSubframes || c.preLength != s.Order {
		return fmt.Sprintf("geometry C=(subframe=%d subframes=%d pre=%d) Go=(subframe=%d subframes=%d order=%d)",
			c.subfrLength, c.nbSubfr, c.preLength, s.SubframeSamples, s.NumSubframes, s.Order), "", "", ""
	}
	wantCount := int(s.NumSubframes) * (int(s.SubframeSamples) + int(s.Order))
	if c.outputCount != wantCount || len(s.Residual) != wantCount || len(c.subframes) != int(s.NumSubframes) {
		return fmt.Sprintf("output geometry C count/subframes=%d/%d Go residual/subframes=%d/%d want count=%d",
			c.outputCount, len(c.subframes), len(s.Residual), s.NumSubframes, wantCount), "", "", ""
	}
	for k := 0; k < int(s.NumSubframes); k++ {
		csf := c.subframes[k]
		if math.Float32bits(c.gains[k]) != math.Float32bits(s.Gains[k]) {
			return fmt.Sprintf("subframe%d raw gain C=%08x Go=%08x", k,
				math.Float32bits(c.gains[k]), math.Float32bits(s.Gains[k])), "", "", ""
		}
		cInvGain := float32(1.0) / c.gains[k]
		goInvGain := float32(1.0) / s.Gains[k]
		if math.Float32bits(csf.invGain) != math.Float32bits(cInvGain) ||
			math.Float32bits(s.InvGains[k]) != math.Float32bits(goInvGain) {
			return fmt.Sprintf("subframe%d reciprocal from matching raw gain C=%08x Go=%08x; stored invGain C=%08x Go=%08x",
				k, math.Float32bits(cInvGain), math.Float32bits(goInvGain),
				math.Float32bits(csf.invGain), math.Float32bits(s.InvGains[k])), "", "", ""
		}
		if csf.pitchL != s.PitchLags[k] {
			return fmt.Sprintf("subframe%d pitchL C=%d Go=%d", k, csf.pitchL, s.PitchLags[k]), "", "", ""
		}
		if math.Float32bits(csf.invGain) != math.Float32bits(s.InvGains[k]) {
			return fmt.Sprintf("subframe%d invGain C=%08x Go=%08x", k, math.Float32bits(csf.invGain), math.Float32bits(s.InvGains[k])), "", "", ""
		}
		for j := 0; j < silkCtrlLTPOrder; j++ {
			if math.Float32bits(csf.taps[j]) != math.Float32bits(s.Taps[k][j]) {
				return fmt.Sprintf("subframe%d B[%d] C=%08x Go=%08x", k, j,
					math.Float32bits(csf.taps[j]), math.Float32bits(s.Taps[k][j])), "", "", ""
			}
		}
	}
	if len(s.PitchBuffer) == 0 || s.Scale == 0 {
		return "Go pitch buffer or scale is empty", "", "", ""
	}
	allInputsMatch := true
	allResultsMatch := true
	for k := 0; k < int(s.NumSubframes); k++ {
		csf := c.subframes[k]
		segmentSamples := int(s.SubframeSamples + s.Order)
		for i := 0; i < segmentSamples; i++ {
			sampleIndex := k*segmentSamples + i
			xIndex := s.FrameStart - int(s.Order) + k*int(s.SubframeSamples) + i
			if xIndex < 0 || xIndex >= len(s.PitchBuffer) {
				return fmt.Sprintf("subframe%d sample%d Go x index %d outside pitch buffer length %d", k, i, xIndex, len(s.PitchBuffer)), "", "", ""
			}
			goX := s.PitchBuffer[xIndex] * s.Scale
			if math.Float32bits(csf.samples[i].x) != math.Float32bits(goX) {
				if inputDiff == "" {
					inputDiff = fmt.Sprintf("subframe%d sample%d x C=%08x Go=%08x", k, i, math.Float32bits(csf.samples[i].x), math.Float32bits(goX))
				}
				allInputsMatch = false
			}
			for j := 0; j < silkCtrlLTPOrder; j++ {
				lagIndex := xIndex - int(s.PitchLags[k]) + silkCtrlLTPOrder/2 - j
				if lagIndex < 0 || lagIndex >= len(s.PitchBuffer) {
					return fmt.Sprintf("subframe%d sample%d lag%d index %d outside pitch buffer length %d", k, i, j, lagIndex, len(s.PitchBuffer)), "", "", ""
				}
				goLag := s.PitchBuffer[lagIndex] * s.Scale
				if math.Float32bits(csf.samples[i].lags[j]) != math.Float32bits(goLag) {
					if inputDiff == "" {
						inputDiff = fmt.Sprintf("subframe%d sample%d lag%d C=%08x Go=%08x", k, i, j,
							math.Float32bits(csf.samples[i].lags[j]), math.Float32bits(goLag))
					}
					allInputsMatch = false
				}
			}
			goResult := s.Residual[sampleIndex]
			if math.Float32bits(csf.samples[i].result) != math.Float32bits(goResult) {
				if resultDiff == "" {
					resultDiff = fmt.Sprintf("subframe%d sample%d C=%08x Go=%08x", k, i,
						math.Float32bits(csf.samples[i].result), math.Float32bits(goResult))
				}
				allResultsMatch = false
			}
		}
	}
	if allInputsMatch {
		for k := 0; k < int(s.NumSubframes); k++ {
			csf := c.subframes[k]
			segmentSamples := int(s.SubframeSamples + s.Order)
			for i := 0; i < segmentSamples; i++ {
				sampleIndex := k*segmentSamples + i
				xIndex := s.FrameStart - int(s.Order) + k*int(s.SubframeSamples) + i
				accSeparate := csf.samples[i].x
				accFMA := csf.samples[i].x
				for j := 0; j < silkCtrlLTPOrder; j++ {
					lagIndex := xIndex - int(s.PitchLags[k]) + silkCtrlLTPOrder/2 - j
					normalizedLag := s.PitchBuffer[lagIndex]
					product := ltpSeparateMul(s.Taps[k][j], normalizedLag)
					product = ltpSeparateMul(product, s.Scale)
					accSeparate = ltpSeparateSub(accSeparate, product)
					accFMA = opusmath.FMA32(-s.Taps[k][j], csf.samples[i].lags[j], accFMA)
				}
				accSeparate = accSeparate * s.InvGains[k]
				accFMA = accFMA * csf.invGain
				goActual := s.Residual[sampleIndex]
				cActual := csf.samples[i].result
				if math.Float32bits(accSeparate) != math.Float32bits(goActual) && goModelDiff == "" {
					goModelDiff = fmt.Sprintf("subframe%d sample%d source-order=%08x Go=%08x", k, i,
						math.Float32bits(accSeparate), math.Float32bits(goActual))
				}
				if math.Float32bits(accFMA) != math.Float32bits(cActual) && fmaModelDiff == "" {
					fmaModelDiff = fmt.Sprintf("subframe%d sample%d ordered-FMA=%08x C=%08x", k, i,
						math.Float32bits(accFMA), math.Float32bits(cActual))
				}
			}
		}
	}
	if !allInputsMatch && inputDiff == "" {
		inputDiff = "unclassified operand mismatch"
	}
	if !allResultsMatch && resultDiff == "" {
		resultDiff = "unclassified output mismatch"
	}
	return inputDiff, resultDiff, goModelDiff, fmaModelDiff
}

//go:noinline
func ltpSeparateMul(a, b float32) float32 { return a * b }

//go:noinline
func ltpSeparateSub(a, b float32) float32 { return a - b }

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
