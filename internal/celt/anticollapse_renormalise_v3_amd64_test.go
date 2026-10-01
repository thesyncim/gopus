//go:build linux && amd64.v3 && (!goexperiment.simd || nosimd || purego) && !gopus_fixed_point && !gopus_qext && !gopus_dred && !gopus_osce

package celt

import (
	"encoding/hex"
	"math"
	"os"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

const celtAntiCollapseRenormalisePacketHex = "f88a80f9b1f279043ccaa4438b1af0a01a527afff72a64d64fa8335d6bdaaa73ef0ca69d8b45928fc7874db179baa757dd607ed384984fcec62aaca858b3c26aa0b05f88753cc837f8721e125980e1c2003036864468a9e3e551ad57e1341dbd3e69f5aff85cdcbc48b3398bbc421fb4817df56c6e9fd633c1a1dda8f647bc7efff1061f79b4dad2182b0fdcf30214a28f0f03ae735245cd8becada0df434bfef3643ddcd482739a48088d273929a77e3cc6e3ee201255495a0fadd30495ca66e72b68ac282d93bd5e1556a996a95237494b12fcdf4a222eace55fe71d4bbbea95e1fcb155fc7e7bc33b1f194fcdccdb45b8324308d74a146032b42efe25f32b92584d4dbed2d3e9242f9ef5feb3ba950069d3e85cb67fd01313a154"

var celtAntiCollapseRenormaliseTraceHelper libopustest.HelperCache

func buildCELTAntiCollapseRenormaliseTraceHelper() (string, error) {
	cfg := libopustest.CHelperConfig{
		Label:       "CELT anti-collapse renormalise trace",
		OutputBase:  "gopus_libopus_celt_renormalise_trace",
		SourceFile:  "libopus_celt_renormalise_trace.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"celt", "silk", "src"},
		LDFlags:     []string{"-Wl,--wrap=anti_collapse", "-Wl,--wrap=renormalise_vector"},
		DeadStrip:   true,
	}
	configureCELTOracleReference(&cfg)
	return libopustest.BuildCHelper(cfg)
}

type celtRenormaliseCall struct {
	n      int
	gain   float32
	before []float32
	after  []float32
}

type goAntiCollapseRenormaliseCall struct {
	band    int
	channel int
	radius  float32
	before  []float32
	after   []float32
}

func traceLibopusCELTRenormalise(t *testing.T, packet []byte) (int, int, []celtRenormaliseCall, []float32) {
	t.Helper()
	binPath, err := celtAntiCollapseRenormaliseTraceHelper.Path(buildCELTAntiCollapseRenormaliseTraceHelper)
	if err != nil {
		libopustest.HelperUnavailable(t, "CELT anti-collapse renormalise trace", err)
	}
	payload := libopustest.NewOraclePayload("GARI", 48000, 1, 960, uint32(len(packet)))
	payload.Raw(packet)
	reader, err := libopustest.RunOracleVersion(binPath, payload.Bytes(), "CELT anti-collapse renormalise trace", "GARO", 1)
	if err != nil {
		libopustest.HelperUnavailable(t, "CELT anti-collapse renormalise trace", err)
	}
	decoded := int(reader.U32())
	antiCalls := int(reader.U32())
	callCount := reader.U32()
	overflow := reader.U32()
	if err := reader.Err(); err != nil {
		t.Fatalf("selected C renormalise trace header: %v", err)
	}
	if decoded != 960 {
		t.Fatalf("selected C decode returned %d samples, want 960", decoded)
	}
	if overflow != 0 {
		t.Fatalf("selected C renormalise trace overflowed its bounded records")
	}
	if callCount > 64 {
		t.Fatalf("selected C renormalise call count=%d exceeds 64", callCount)
	}
	calls := make([]celtRenormaliseCall, int(callCount))
	for i := range calls {
		n := reader.U32()
		if n == 0 || n > 2048 {
			t.Fatalf("selected C renormalise call[%d] length=%d exceeds 1..2048", i, n)
		}
		calls[i].n = int(n)
		calls[i].gain = reader.Float32()
		calls[i].before = make([]float32, int(n))
		calls[i].after = make([]float32, int(n))
		for j := range calls[i].before {
			calls[i].before[j] = reader.Float32()
		}
		for j := range calls[i].after {
			calls[i].after[j] = reader.Float32()
		}
	}
	pcm := make([]float32, decoded)
	for i := range pcm {
		pcm[i] = reader.Float32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
	return decoded, antiCalls, calls, pcm
}

func TestCELTAntiCollapseRenormaliseBoundaryV3(t *testing.T) {
	libopustest.RequireOracle(t)
	if target := os.Getenv("GOPUS_LIBOPUS_AMD64_TARGET"); target != "v3" {
		message := "CELT anti-collapse renormalise trace requires GOPUS_LIBOPUS_AMD64_TARGET=v3, got " + target
		if libopustest.StrictRefRequired() {
			t.Fatal(message)
		}
		t.Skip(message)
	}

	packet, err := hex.DecodeString(celtAntiCollapseRenormalisePacketHex)
	if err != nil {
		t.Fatalf("decode captured packet: %v", err)
	}
	decoded, antiCalls, cCalls, tracedPCM := traceLibopusCELTRenormalise(t, packet)
	if decoded != 960 {
		t.Fatalf("traced C decode returned %d samples, want 960", decoded)
	}
	if antiCalls != 1 {
		t.Fatalf("selected C anti-collapse calls=%d, want one", antiCalls)
	}
	ordinary, err := libopustest.ProbeDecodeDiff(48000, 1, []libopustest.DecodeDiffCase{{
		Packet: packet, Format: libopustest.DecodeDiffFormatFloat32, FrameSize: 960,
	}})
	if err != nil {
		t.Fatalf("ordinary libopus decode: %v", err)
	}
	if len(ordinary) != 1 || ordinary[0].Code != 960 {
		t.Fatalf("ordinary libopus decode result count/code=%d/%d, want 1/960", len(ordinary), ordinary[0].Code)
	}
	assertCELTDecodeStageEqual(t, "renormalise-traced C vs ordinary C PCM", tracedPCM, ordinary[0].Float32())

	dec := NewDecoder(1)
	if err := dec.SetAPISampleRate(48000); err != nil {
		t.Fatalf("SetAPISampleRate: %v", err)
	}
	dec.SetBandwidth(CELTFullband)
	prev1 := append([]celtGLog(nil), dec.prevLogE...)
	prev2 := append([]celtGLog(nil), dec.prevLogE2...)
	stage := dec.EnableSynthesisStageTrace()
	goPCM := make([]float32, 960)
	if err := dec.DecodeFrameWithPacketStereoToFloat32AtAPIRate(packet[1:], 960, false, goPCM); err != nil {
		t.Fatalf("Go decode: %v", err)
	}
	if !stage.Captured() {
		t.Fatal("Go CELT synthesis trace did not capture the failing packet")
	}
	ordinaryGoDec := NewDecoder(1)
	if err := ordinaryGoDec.SetAPISampleRate(48000); err != nil {
		t.Fatalf("ordinary Go SetAPISampleRate: %v", err)
	}
	ordinaryGoDec.SetBandwidth(CELTFullband)
	ordinaryGo := make([]float32, 960)
	if err := ordinaryGoDec.DecodeFrameWithPacketStereoToFloat32AtAPIRate(packet[1:], 960, false, ordinaryGo); err != nil {
		t.Fatalf("ordinary Go decode: %v", err)
	}
	assertCELTDecodeStageEqual(t, "renormalise-traced Go vs ordinary Go PCM", goPCM, ordinaryGo)

	mode := dec.modeConfig(960)
	end := len(stage.BaseEnergy(0))
	edges := dec.modeEdges()
	if end <= 0 || end >= len(edges) || end > len(dec.scratchEnergies) || end > len(stage.antiCollapsePulses) {
		t.Fatalf("invalid replay dimensions: bands=%d edges=%d energies=%d pulses=%d", end, len(edges), len(dec.scratchEnergies), len(stage.antiCollapsePulses))
	}
	if len(stage.collapseMasks) != len(edges)-1 {
		t.Fatalf("captured Go collapse mask count=%d, want %d", len(stage.collapseMasks), len(edges)-1)
	}
	coeffs := make([]celtNorm, len(stage.antiCollapseNormPre[0]))
	for i, value := range stage.antiCollapseNormPre[0] {
		coeffs[i] = celtNorm(value)
	}
	logE := append([]celtGLog(nil), dec.scratchEnergies[:end]...)
	pulses := append([]int32(nil), stage.antiCollapsePulses[:end]...)
	collapse := append([]byte(nil), stage.collapseMasks...)
	goCalls := replayAntiCollapseRenormaliseInputs(coeffs, collapse, mode.LM, 1, 0, end,
		logE, prev1, prev2, pulses, stage.antiCollapseSeed, edges, len(edges)-1)
	activeNormCount := (1 << mode.LM) * edges[end]
	if activeNormCount > len(coeffs) || activeNormCount > len(stage.antiCollapseNormPost[0]) {
		t.Fatalf("active replay norm count=%d exceeds Go buffers %d/%d", activeNormCount, len(coeffs), len(stage.antiCollapseNormPost[0]))
	}
	if diff := antiCollapseFirstFloatDiff(float32sFromCELTNorm(coeffs[:activeNormCount]), stage.antiCollapseNormPost[0][:activeNormCount]); diff >= 0 {
		t.Fatalf("anti-collapse replay differs from live Go at norm[%d]: replay=%08x live=%08x", diff,
			math.Float32bits(float32(coeffs[diff])), math.Float32bits(stage.antiCollapseNormPost[0][diff]))
	}
	for i := range goCalls {
		start := edges[goCalls[i].band] << mode.LM
		end := edges[goCalls[i].band+1] << mode.LM
		goCalls[i].after = float32sFromCELTNorm(coeffs[start:end])
	}
	if len(goCalls) == 0 {
		t.Fatal("Go anti-collapse replay made no renormalise_vector calls")
	}
	if len(cCalls) != len(goCalls) {
		t.Fatalf("live C/Go-replay anti-collapse renormalise call counts=%d/%d; C lengths=%v Go replay bands=%v", len(cCalls), len(goCalls), renormaliseCallLengths(cCalls), antiCollapseCallBands(goCalls))
	}
	for i := range goCalls {
		goCall, cCall := goCalls[i], cCalls[i]
		if goCall.channel != 0 || len(goCall.before) != cCall.n || cCall.n != len(goCall.after) {
			t.Fatalf("anti-collapse renormalise call[%d] shape Go band/channel/size=%d/%d/%d C size=%d", i, goCall.band, goCall.channel, len(goCall.before), cCall.n)
		}
		if math.Float32bits(cCall.gain) != math.Float32bits(1) {
			t.Fatalf("C anti-collapse renormalise call[%d] gain=%08x, want 1.0", i, math.Float32bits(cCall.gain))
		}
		if diff := antiCollapseFirstFloatDiff(goCall.before, cCall.before); diff >= 0 {
			t.Fatalf("first C/Go-replay difference is in anti-collapse fill before renormalise at call[%d] band=%d coeff=%d: Go replay=%08x C=%08x radius=%08x", i, goCall.band, diff,
				math.Float32bits(goCall.before[diff]), math.Float32bits(cCall.before[diff]), math.Float32bits(goCall.radius))
		}
		if diff := antiCollapseFirstFloatDiff(goCall.after, cCall.after); diff >= 0 {
			t.Fatalf("first C/Go-replay difference is inside renormalise_vector at call[%d] band=%d coeff=%d: Go replay=%08x C=%08x; pre-inputs match", i, goCall.band, diff,
				math.Float32bits(goCall.after[diff]), math.Float32bits(cCall.after[diff]))
		}
	}
}

func antiCollapseFirstFloatDiff(got, want []float32) int {
	if len(got) != len(want) {
		return min(len(got), len(want))
	}
	for i := range got {
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
			return i
		}
	}
	return -1
}

func float32sFromCELTNorm(values []celtNorm) []float32 {
	out := make([]float32, len(values))
	for i, value := range values {
		out[i] = float32(value)
	}
	return out
}

func renormaliseCallLengths(calls []celtRenormaliseCall) []int {
	out := make([]int, len(calls))
	for i := range calls {
		out[i] = calls[i].n
	}
	return out
}

func antiCollapseCallBands(calls []goAntiCollapseRenormaliseCall) []int {
	out := make([]int, len(calls))
	for i := range calls {
		out[i] = calls[i].band
	}
	return out
}

// replayAntiCollapseRenormaliseInputs mirrors the packet's Go anti-collapse
// loop while recording each exact vector passed to renormalizeVector. The live
// result is checked against the decoder's synthesis trace above, so this
// diagnostic cannot silently substitute its replay for the production path.
func replayAntiCollapseRenormaliseInputs(
	coeffs []celtNorm,
	collapse []byte,
	lm, channels, start, end int,
	logE, prev1LogE, prev2LogE []celtGLog,
	pulses []int32,
	seed uint32,
	edges []int,
	nbEBands int,
) []goAntiCollapseRenormaliseCall {
	if channels < 1 || channels > 2 || len(edges) < 2 || nbEBands <= 0 || len(collapse) < channels*nbEBands {
		return nil
	}
	if start < 0 {
		start = 0
	}
	if end > nbEBands {
		end = nbEBands
	}
	if end <= start {
		return nil
	}
	blocks := 1 << lm
	if blocks <= 0 {
		return nil
	}
	logEStride := end
	if len(logE) > 0 {
		logEStride = max(len(logE)/channels, end)
	}
	calls := make([]goAntiCollapseRenormaliseCall, 0, end-start)
	for band := start; band < end; band++ {
		width := edges[band+1] - edges[band]
		if width <= 0 || band >= len(pulses) {
			continue
		}
		depth := celtUdiv(1+int(pulses[band]), width) >> lm
		threshold := float32(0.5) * celtExp2(float32(-0.125)*float32(depth))
		sqrt1 := celtRSqrt(float32(width << lm))
		offset := edges[band] << lm
		bandLen := width << lm
		for channel := range channels {
			logIndex := channel*logEStride + band
			prevIndex := channel*nbEBands + band
			if logIndex >= len(logE) || prevIndex >= len(prev1LogE) || prevIndex >= len(prev2LogE) || offset+bandLen > len(coeffs) {
				continue
			}
			prev1, prev2 := prev1LogE[prevIndex], prev2LogE[prevIndex]
			if channels == 1 && len(prev1LogE) >= 2*nbEBands && len(prev2LogE) >= 2*nbEBands {
				if alternate := prev1LogE[nbEBands+band]; alternate > prev1 {
					prev1 = alternate
				}
				if alternate := prev2LogE[nbEBands+band]; alternate > prev2 {
					prev2 = alternate
				}
			}
			prevMin := prev1
			if prev2 < prevMin {
				prevMin = prev2
			}
			ediff := float32(logE[logIndex] - prevMin)
			if ediff < 0 {
				ediff = 0
			}
			radius := float32(2) * celtExp2(-ediff)
			if lm == 3 {
				radius *= 1.41421356
			}
			if radius > threshold {
				radius = threshold
			}
			radius *= sqrt1
			if radius <= 0 {
				continue
			}
			mask := collapse[band*channels+channel]
			needsRenormalize := false
			for block := range blocks {
				if mask&(1<<uint(block)) != 0 {
					continue
				}
				for j := range width {
					seed = seed*1664525 + 1013904223
					sample := radius
					if seed&0x8000 == 0 {
						sample = -sample
					}
					coeffs[offset+(j<<lm)+block] = celtNorm(sample)
				}
				needsRenormalize = true
			}
			if needsRenormalize {
				vector := coeffs[offset : offset+bandLen]
				call := goAntiCollapseRenormaliseCall{band: band, channel: channel, radius: radius, before: float32sFromCELTNorm(vector)}
				renormalizeVector(vector, 1)
				call.after = float32sFromCELTNorm(vector)
				calls = append(calls, call)
			}
		}
	}
	return calls
}
