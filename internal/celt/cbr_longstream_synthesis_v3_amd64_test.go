//go:build amd64.v3 && !gopus_fixed_point

package celt

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"math"
	"strings"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/libopustooling"
	"github.com/thesyncim/gopus/internal/testsignal"
)

const (
	celtLongstreamSampleRate = 48000
	celtLongstreamFrameSize  = 120
	celtLongstreamFrameCount = 400
)

var celtLongstreamCBRHelper libopustest.HelperCache

func buildCELTV3LongstreamCBRHelper() (string, error) {
	return celtLongstreamCBRHelper.Path(func() (string, error) {
		return libopustest.BuildPublicAPIHelper(libopustest.CHelperConfig{
			Label:      "CELT 2.5 ms CBR longstream packet source",
			OutputBase: "gopus_libopus_cbr_encode_packets",
			SourceFile: "libopus_cbr_encode_packets.c",
			CFlags:     []string{"-DHAVE_CONFIG_H"},
		})
	})
}

// TestCELTV3CBRLongStreamFirstDecodeDivergence replays the CELT-FB 2.5 ms
// mono, 64 kbit/s, 400-packet CBR stream used by the CBR contract. It finds the
// first PCM mismatch between persistent C and Go decoders, then captures every
// synthesis stage after replaying the exact same packet prefix through each
// decoder. The C trace helper is checked against the ordinary C decoder output
// at the target frame before it is used as a stage oracle.
func TestCELTV3CBRLongStreamFirstDecodeDivergence(t *testing.T) {
	if level := libopustooling.CompiledGoAMD64Level(); level != "v3" {
		t.Fatalf("CELT v3 longstream witness was compiled for GOAMD64=%q, want v3", level)
	}
	target, err := libopustooling.ResolveLibopusAMD64Target()
	if err != nil {
		t.Fatalf("resolve matching libopus AMD64 target: %v", err)
	}
	if target != "v3" {
		t.Fatalf("CELT v3 longstream witness requires matching libopus v3 oracle, got target %q", target)
	}
	libopustest.RequireOracle(t)
	packets := celtV3CBRLongstreamPackets(t)
	if len(packets) != celtLongstreamFrameCount {
		t.Fatalf("C CBR encoder returned %d packets, want %d", len(packets), celtLongstreamFrameCount)
	}

	cases := make([]libopustest.DecodeDiffCase, len(packets))
	for i, packet := range packets {
		if len(packet) < 2 {
			t.Fatalf("C CBR packet %d has length %d", i, len(packet))
		}
		cases[i] = libopustest.DecodeDiffCase{
			Packet:    packet,
			Format:    libopustest.DecodeDiffFormatFloat32,
			FrameSize: celtLongstreamFrameSize,
		}
	}
	cResults, err := libopustest.ProbeDecodeSequence(celtLongstreamSampleRate, 1, cases)
	if err != nil {
		t.Fatalf("decode CBR packets with ordinary C decoder: %v", err)
	}
	if len(cResults) != len(packets) {
		t.Fatalf("ordinary C decoder returned %d results, want %d", len(cResults), len(packets))
	}

	goDecoder := newCELTV3LongstreamDecoder(t)
	goFrame := make([]float32, celtLongstreamFrameSize)
	targetFrame, firstSample := -1, -1
	var untracedTargetPCM []float32
	for i, result := range cResults {
		if result.Code != celtLongstreamFrameSize {
			t.Fatalf("ordinary C decoder packet %d returned %d samples, want %d", i, result.Code, celtLongstreamFrameSize)
		}
		cFrame := result.Float32()
		if len(cFrame) != celtLongstreamFrameSize {
			t.Fatalf("ordinary C decoder packet %d returned %d PCM samples, want %d", i, len(cFrame), celtLongstreamFrameSize)
		}
		if err := goDecoder.DecodeFrameWithPacketStereoToFloat32AtAPIRate(packets[i][1:], celtLongstreamFrameSize, false, goFrame); err != nil {
			t.Fatalf("Go decoder packet %d: %v", i, err)
		}
		if sample := firstCELTLongstreamFloat32Difference(goFrame, cFrame); sample >= 0 {
			targetFrame, firstSample = i, sample
			untracedTargetPCM = append([]float32(nil), goFrame...)
			t.Logf("first same-packet PCM divergence: frame=%d frame_sample=%d stream_sample=%d packet_prefix=%d prefix_sha256=%s prefix_packets=%s",
				i, sample, i*celtLongstreamFrameSize+sample, i+1,
				celtLongstreamPacketPrefixHash(packets[:i+1]), formatCELTV3LongstreamPacketPrefix(packets[:i+1]))
			break
		}
	}
	if targetFrame < 0 {
		t.Logf("same-packet PCM is bit-exact across all CBR frames: frames=%d samples=%d",
			len(packets), len(packets)*celtLongstreamFrameSize)
		return
	}

	prefix := packets[:targetFrame+1]
	cTrace := traceLibopusCELTSynthesis(t, celtLongstreamSampleRate, 1, celtLongstreamFrameSize, targetFrame, prefix)
	if len(cTrace.final) != celtLongstreamFrameSize {
		t.Fatalf("C synthesis trace final PCM length=%d, want %d", len(cTrace.final), celtLongstreamFrameSize)
	}
	ordinaryPCM := cResults[targetFrame].Float32()
	if sample := firstCELTLongstreamFloat32Difference(cTrace.final, ordinaryPCM); sample >= 0 {
		t.Fatalf("C synthesis trace altered ordinary decoder output at frame %d sample %d: trace=%08x ordinary=%08x",
			targetFrame, sample, math.Float32bits(cTrace.final[sample]), math.Float32bits(ordinaryPCM[sample]))
	}

	stageDecoder := newCELTV3LongstreamDecoder(t)
	var stage *synthesisStageTrace
	goFrame = make([]float32, celtLongstreamFrameSize)
	for i, packet := range prefix {
		if i == targetFrame {
			stage = stageDecoder.EnableSynthesisStageTrace()
		}
		if err := stageDecoder.DecodeFrameWithPacketStereoToFloat32AtAPIRate(packet[1:], celtLongstreamFrameSize, false, goFrame); err != nil {
			t.Fatalf("Go replay packet %d: %v", i, err)
		}
	}
	if stage == nil || !stage.Captured() {
		t.Fatal("Go synthesis-stage trace did not capture the target frame")
	}
	if sample := firstCELTLongstreamFloat32Difference(goFrame, untracedTargetPCM); sample >= 0 {
		t.Fatalf("Go synthesis trace altered target PCM at frame %d sample %d: traced=%08x untraced=%08x",
			targetFrame, sample, math.Float32bits(goFrame[sample]), math.Float32bits(untracedTargetPCM[sample]))
	}

	assertCELTV3LongstreamStageEqual(t, targetFrame, firstSample, "base energy", stage.BaseEnergy(0), cTrace.baseEnergy[0])
	edges := stageDecoder.modeEdges()
	baseBands := len(cTrace.baseEnergy[0])
	if baseBands >= len(edges) {
		t.Fatalf("C base energy count %d exceeds CELT band-edge table length %d", baseBands, len(edges))
	}
	activeNormCount := edges[baseBands]
	if activeNormCount > len(stage.BaseNorm(0)) || activeNormCount > len(cTrace.baseNorm[0]) {
		t.Fatalf("active normalized coefficient count %d exceeds Go/C buffers %d/%d", activeNormCount,
			len(stage.BaseNorm(0)), len(cTrace.baseNorm[0]))
	}
	assertCELTV3LongstreamStageEqual(t, targetFrame, firstSample, "base normalized coefficients", stage.BaseNorm(0)[:activeNormCount], cTrace.baseNorm[0][:activeNormCount])
	assertCELTV3LongstreamStageEqual(t, targetFrame, firstSample, "post-denormalise spectrum", stage.Spec(0), cTrace.freq[0])
	assertCELTV3LongstreamStageEqual(t, targetFrame, firstSample, "post-IMDCT", stage.IMDCT(0), cTrace.imdct[0])
	assertCELTV3LongstreamStageEqual(t, targetFrame, firstSample, "post-comb-filter", stage.PostComb(0), cTrace.postComb[0])
	assertCELTV3LongstreamStageEqual(t, targetFrame, firstSample, "post-deemphasis PCM", goFrame, cTrace.final)
	t.Fatalf("expected the known longstream PCM divergence at frame %d sample %d to be localized", targetFrame, firstSample)
}

func newCELTV3LongstreamDecoder(t *testing.T) *Decoder {
	t.Helper()
	dec := NewDecoder(1)
	if err := dec.SetAPISampleRate(celtLongstreamSampleRate); err != nil {
		t.Fatalf("SetAPISampleRate: %v", err)
	}
	dec.SetBandwidth(CELTFullband)
	return dec
}

func celtV3CBRLongstreamPackets(t *testing.T) [][]byte {
	t.Helper()
	pcm, err := testsignal.GenerateEncoderSignalVariant(
		testsignal.EncoderVariantAMMultisineV1,
		celtLongstreamSampleRate,
		celtLongstreamSampleRate,
		1,
	)
	if err != nil {
		t.Fatalf("generate CBR contract input: %v", err)
	}
	// Match testvectors.quantizeCBRPCM exactly before the public C encoder sees
	// these samples.
	for i, sample := range pcm {
		pcm[i] = float32(math.Floor(0.5+float64(sample)*8388608.0) / 8388608.0)
	}

	payload := libopustest.NewOraclePayloadVersion("GCBR", 1,
		3,    // OPUS_APPLICATION_RESTRICTED_CELT
		1105, // OPUS_BANDWIDTH_FULLBAND
		1,
		64000,
		celtLongstreamFrameSize,
		10,
		celtLongstreamFrameCount,
	)
	payload.Float32s(pcm...)
	binPath, err := buildCELTV3LongstreamCBRHelper()
	if err != nil {
		libopustest.HelperUnavailable(t, "CELT CBR packet source", err)
	}
	output, err := libopustest.RunHelper(binPath, payload.Bytes())
	if err != nil {
		t.Fatalf("run CBR packet source: %v", err)
	}
	reader, version, err := libopustest.NewOracleReaderVersion("CELT CBR packet source", "GCBO", output)
	if err != nil {
		t.Fatal(err)
	}
	if version != 2 {
		t.Fatalf("CBR helper output version=%d want 2", version)
	}
	versionLen := int(reader.U32())
	versionString := string(reader.Bytes(versionLen))
	if !strings.Contains(versionString, "1.6.1") {
		t.Fatalf("CBR helper reports %q, want pinned libopus 1.6.1", versionString)
	}
	archMask := reader.U32()
	buildFeatures := reader.U32()
	selectedArch := reader.U32()
	t.Logf("C packet source reference: version=%q arch_mask=%d build_features=0x%x selected_arch=%d",
		versionString, archMask, buildFeatures, selectedArch)
	count := reader.Count(celtLongstreamFrameCount)
	packets := make([][]byte, count)
	for i := range count {
		packetLen := int(reader.U32())
		_ = reader.U32() // final range
		if packetLen <= 0 {
			t.Fatalf("CBR helper emitted empty packet %d", i)
		}
		packets[i] = append([]byte(nil), reader.Bytes(packetLen)...)
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatalf("parse CBR helper output: %v", err)
	}
	return packets
}

func firstCELTLongstreamFloat32Difference(got, want []float32) int {
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

func celtLongstreamPacketPrefixHash(packets [][]byte) string {
	h := sha256.New()
	for _, packet := range packets {
		var lenBytes [4]byte
		lenBytes[0] = byte(len(packet))
		lenBytes[1] = byte(len(packet) >> 8)
		lenBytes[2] = byte(len(packet) >> 16)
		lenBytes[3] = byte(len(packet) >> 24)
		_, _ = h.Write(lenBytes[:])
		_, _ = h.Write(packet)
	}
	return hex.EncodeToString(h.Sum(nil))
}

func assertCELTV3LongstreamStageEqual(t *testing.T, frame, firstPCM int, stageName string, got, want []float32) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("first longstream CELT divergence before PCM frame=%d sample=%d: stage=%s lengths Go/C=%d/%d",
			frame, firstPCM, stageName, len(got), len(want))
	}
	for i := range got {
		gotBits, wantBits := math.Float32bits(got[i]), math.Float32bits(want[i])
		if gotBits != wantBits {
			t.Fatalf("first longstream CELT divergence before PCM frame=%d sample=%d: stage=%s[%d] Go=%08x %.9g C=%08x %.9g; replayed_prefix=%d",
				frame, firstPCM, stageName, i, gotBits, got[i], wantBits, want[i], frame+1)
		}
	}
}

func formatCELTV3LongstreamPacketPrefix(packets [][]byte) string {
	var out strings.Builder
	for i, packet := range packets {
		if i > 0 {
			out.WriteByte(';')
		}
		fmt.Fprintf(&out, "%d:%s", i, hex.EncodeToString(packet))
	}
	return out.String()
}
