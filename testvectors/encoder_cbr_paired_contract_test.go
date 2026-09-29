package testvectors

import (
	"fmt"
	"math"
	"runtime"
	"testing"

	gopus "github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/testsignal"
)

const (
	cbrContractSampleRate  = 48000
	cbrContractCaseCount   = 19
	cbrContractPacketCount = 2175
)

type cbrContractDecode struct {
	pcm            []float32
	samplesPerCall []int
	ranges         []uint32
	issues         contractIssues
}

type contractIssues struct {
	count int
	first string
}

func (i *contractIssues) add(format string, args ...any) {
	i.count++
	if i.first == "" {
		i.first = fmt.Sprintf(format, args...)
	}
}

func (i contractIssues) summary(label string) string {
	if i.count == 0 {
		return ""
	}
	return fmt.Sprintf("%s: %d issue(s), first: %s", label, i.count, i.first)
}

// TestEncoderCBRPairedOracleContract checks CBR packets through both persistent
// decoders. Packet and same-packet PCM differences remain unresolved until a
// reviewed scope classifies them; quality checks do not accept those differences.
func TestEncoderCBRPairedOracleContract(t *testing.T) {
	requireTestTier(t, testTierParity)
	libopustest.RequireOracle(t)

	cases := cbrTestMatrix()
	if len(cases) != cbrContractCaseCount {
		t.Fatalf("CBR contract matrix has %d rows, want %d", len(cases), cbrContractCaseCount)
	}
	expectedPacketCount := 0
	for _, tc := range cases {
		expectedPacketCount += cbrContractSampleRate / tc.frameSize
	}
	if expectedPacketCount != cbrContractPacketCount {
		t.Fatalf("CBR contract matrix has %d packets, want %d", expectedPacketCount, cbrContractPacketCount)
	}

	oraclePath, err := cbrEncoderOraclePath()
	if err != nil {
		t.Fatalf("build paired CBR oracle: %v", err)
	}
	stamp, err := cbrReferenceBuildStamp()
	if err != nil {
		t.Fatalf("resolve paired CBR reference: %v", err)
	}
	t.Logf("paired reference: variant=%s stamp=%s sha256=%s", stamp.Variant, stamp.Path, stamp.Digest)

	caseCount, pairedPacketCount, decodePaths, unresolvedCases := 0, 0, 0, 0
	for _, tc := range cases {
		tc := tc
		rowUnresolved := false
		t.Run(tc.name, func(t *testing.T) {
			caseCount++
			frameCount := cbrContractSampleRate / tc.frameSize
			wantPCMCount := cbrContractSampleRate * tc.channels
			hardIssueCount := 0
			recordIssue := func(message string) {
				rowUnresolved = true
				hardIssueCount++
				t.Errorf("%s", message)
			}
			recordIssues := func(issues contractIssues) {
				if message := issues.summary(tc.name); message != "" {
					recordIssue(message)
				}
			}

			pcm, err := testsignal.GenerateEncoderSignalVariant(
				testsignal.EncoderVariantAMMultisineV1,
				cbrContractSampleRate,
				frameCount*tc.frameSize*tc.channels,
				tc.channels,
			)
			if err != nil {
				recordIssue(fmt.Sprintf("%s: generate signal: %v", tc.name, err))
				return
			}
			inputID := cbrPCMIdentity(pcm)

			cEncoded, err := runCBROracleEncode(oraclePath, tc, pcm)
			if err != nil {
				recordIssue(fmt.Sprintf("%s: run paired CBR oracle: %v", tc.name, err))
				return
			}
			if err := validateCBROracleDispatch(cEncoded, stamp.Variant, runtime.GOARCH); err != nil {
				recordIssue(fmt.Sprintf("%s: paired CBR oracle dispatch: %v", tc.name, err))
			}
			gEncoded, err := encodeGopusCBR(tc, pcm)
			if err != nil {
				recordIssue(fmt.Sprintf("%s: gopus CBR encode: %v", tc.name, err))
				return
			}
			recordIssues(checkCBREncoderOutput("C encoder", cEncoded.Packets, cEncoded.FinalRanges, frameCount))
			recordIssues(checkCBREncoderOutput("Go encoder", gEncoded.Packets, gEncoded.FinalRanges, frameCount))
			pairedPacketCount += countCBRContractPairedPackets(gEncoded.Packets, cEncoded.Packets)
			recordIssues(checkCBRContractPacketLengths(gEncoded.Packets, cEncoded.Packets))

			cPacketSamples, cPacketIssues := validateCBRContractPackets("C packets", cEncoded.Packets, tc.frameSize)
			gPacketSamples, gPacketIssues := validateCBRContractPackets("Go packets", gEncoded.Packets, tc.frameSize)
			recordIssues(cPacketIssues)
			recordIssues(gPacketIssues)

			packetDiffs, firstPacketFrame, firstPacketByte := compareCBRPacketStreams(gEncoded.Packets, cEncoded.Packets)
			rangeDiffs := countEncoderRangeDifferences(gEncoded.FinalRanges, cEncoded.FinalRanges)
			if firstPacketFrame >= 0 && firstPacketFrame < len(gEncoded.Packets) && firstPacketFrame < len(cEncoded.Packets) {
				reportCBRByteDiff(t, firstPacketFrame, gEncoded.Packets[firstPacketFrame], cEncoded.Packets[firstPacketFrame])
			}
			if packetDiffs == 0 {
				t.Logf("packet equality: %d/%d packets match byte-for-byte; input=AMMultisineV1/%s", len(cEncoded.Packets), frameCount, inputID)
			} else {
				t.Logf("packet equality: %d/%d packets differ; first frame=%d byte=%d, C/Go encoder range differences=%d; input=AMMultisineV1/%s",
					packetDiffs, max(len(gEncoded.Packets), len(cEncoded.Packets)), firstPacketFrame, firstPacketByte, rangeDiffs, inputID)
			}
			for i := 0; i < min(len(gEncoded.Packets), len(cEncoded.Packets)); i++ {
				if firstCBRByteDifference(gEncoded.Packets[i], cEncoded.Packets[i]) < 0 &&
					i < len(gEncoded.FinalRanges) && i < len(cEncoded.FinalRanges) &&
					gEncoded.FinalRanges[i] != cEncoded.FinalRanges[i] {
					recordIssue(fmt.Sprintf("%s: equal packet %d has different encoder final ranges Go=0x%08x C=0x%08x",
						tc.name, i, gEncoded.FinalRanges[i], cEncoded.FinalRanges[i]))
				}
			}

			pathsRun := 0
			runCDecode := func(label string, packets [][]byte) (cbrContractDecode, bool) {
				if len(packets) == 0 {
					recordIssue(fmt.Sprintf("%s: %s decoder path has no packets", tc.name, label))
					return cbrContractDecode{}, false
				}
				decoded, err := decodeCBRContractWithC(packets, tc.channels)
				if err != nil {
					recordIssue(fmt.Sprintf("%s: %s C decoder probe: %v", tc.name, label, err))
					return cbrContractDecode{}, false
				}
				pathsRun++
				return decoded, true
			}
			runGoDecode := func(label string, packets [][]byte) (cbrContractDecode, bool) {
				if len(packets) == 0 {
					recordIssue(fmt.Sprintf("%s: %s decoder path has no packets", tc.name, label))
					return cbrContractDecode{}, false
				}
				decoded, err := decodeCBRContractWithGo(packets, tc.channels)
				if err != nil {
					recordIssue(fmt.Sprintf("%s: %s Go decoder: %v", tc.name, label, err))
					return cbrContractDecode{}, false
				}
				pathsRun++
				return decoded, true
			}

			cDecodeC, haveCDecodeC := runCDecode("C packets", cEncoded.Packets)
			goDecodeC, haveGoDecodeC := runGoDecode("C packets", cEncoded.Packets)
			cDecodeGo, haveCDecodeGo := runCDecode("Go packets", gEncoded.Packets)
			goDecodeGo, haveGoDecodeGo := runGoDecode("Go packets", gEncoded.Packets)
			decodePaths += pathsRun

			for _, item := range []struct {
				label   string
				decoded cbrContractDecode
				have    bool
				samples []int
			}{
				{"C decode of C packets", cDecodeC, haveCDecodeC, cPacketSamples},
				{"Go decode of C packets", goDecodeC, haveGoDecodeC, cPacketSamples},
				{"C decode of Go packets", cDecodeGo, haveCDecodeGo, gPacketSamples},
				{"Go decode of Go packets", goDecodeGo, haveGoDecodeGo, gPacketSamples},
			} {
				if !item.have {
					continue
				}
				recordIssues(item.decoded.issues)
				recordIssues(validateCBRContractDecode(item.label, item.decoded, item.samples, wantPCMCount))
			}

			recordIssues(checkCBRContractRanges("C packet stream", cEncoded.FinalRanges, cDecodeC, haveCDecodeC, goDecodeC, haveGoDecodeC))
			recordIssues(checkCBRContractRanges("Go packet stream", gEncoded.FinalRanges, cDecodeGo, haveCDecodeGo, goDecodeGo, haveGoDecodeGo))

			samePacketPCMDiffs := 0
			if haveCDecodeC && haveGoDecodeC && len(cDecodeC.pcm) == wantPCMCount && len(goDecodeC.pcm) == wantPCMCount {
				diffs, first := compareCBRContractPCM(cDecodeC.pcm, goDecodeC.pcm)
				samePacketPCMDiffs += diffs
				if diffs > 0 {
					t.Logf("same-packet PCM: C/Go decoder output differs on C packets at %d samples (first=%d)", diffs, first)
				}
			}
			if haveCDecodeGo && haveGoDecodeGo && len(cDecodeGo.pcm) == wantPCMCount && len(goDecodeGo.pcm) == wantPCMCount {
				diffs, first := compareCBRContractPCM(cDecodeGo.pcm, goDecodeGo.pcm)
				samePacketPCMDiffs += diffs
				if diffs > 0 {
					t.Logf("same-packet PCM: C/Go decoder output differs on Go packets at %d samples (first=%d)", diffs, first)
				}
			}

			qualityChecks := 0
			score := func(label string, candidate, reference []float32) {
				if len(candidate) != wantPCMCount || len(reference) != wantPCMCount {
					recordIssue(fmt.Sprintf("%s: %s quality requires complete %d-sample streams (candidate=%d reference=%d)",
						tc.name, label, wantPCMCount, len(candidate), len(reference)))
					return
				}
				if !finiteCBRContractPCM(candidate) || !finiteCBRContractPCM(reference) {
					recordIssue(fmt.Sprintf("%s: %s quality inputs contain non-finite PCM", tc.name, label))
					return
				}
				cmp, err := CompareDecodedFloat32(candidate, reference, cbrContractSampleRate, tc.channels, 0)
				if err != nil {
					recordIssue(fmt.Sprintf("%s: %s IntentNearExact comparison: %v", tc.name, label, err))
					return
				}
				qualityChecks++
				t.Logf("%s IntentNearExact: Q=%.2f corr=%.6f rms=%.4f (zero-delay, full %d-sample streams)",
					label, cmp.Q, cmp.Corr, cmp.RMSRatio, wantPCMCount)
				if failures := QualityBarNearExact.Check(cmp); len(failures) > 0 {
					recordIssue(fmt.Sprintf("%s: %s below IntentNearExact bar: %v", tc.name, label, failures))
				}
			}
			if haveCDecodeC && haveGoDecodeC {
				score("Go decode vs C decode of C packets", goDecodeC.pcm, cDecodeC.pcm)
			}
			if haveCDecodeGo && haveGoDecodeGo {
				score("Go decode vs C decode of Go packets", goDecodeGo.pcm, cDecodeGo.pcm)
			}
			if haveCDecodeC && haveCDecodeGo {
				score("C decode of Go packets vs C packets", cDecodeGo.pcm, cDecodeC.pcm)
			}
			if haveCDecodeC && haveGoDecodeGo {
				score("Go decode of Go packets vs C decode of C packets", goDecodeGo.pcm, cDecodeC.pcm)
			}

			if packetDiffs > 0 || samePacketPCMDiffs > 0 {
				rowUnresolved = true
				t.Errorf("%s: unresolved CBR parity differences: packet_differences=%d same_packet_pcm_sample_differences=%d",
					tc.name, packetDiffs, samePacketPCMDiffs)
			}
			if pathsRun != 4 {
				recordIssue(fmt.Sprintf("%s: completed %d/4 persistent decoder paths", tc.name, pathsRun))
			}
			t.Logf("contract row: packets=%d/%d packet_differences=%d same_packet_pcm_sample_differences=%d quality_checks=%d/4 decode_paths=%d/4 hard_issues=%d",
				max(len(cEncoded.Packets), len(gEncoded.Packets)), frameCount, packetDiffs, samePacketPCMDiffs, qualityChecks, pathsRun, hardIssueCount)
		})
		if rowUnresolved {
			unresolvedCases++
		}
	}

	if caseCount != cbrContractCaseCount {
		t.Errorf("CBR contract ran %d rows, want %d", caseCount, cbrContractCaseCount)
	}
	fmt.Printf("CBR_CONTRACT cases=%d packets=%d decode_paths=%d unresolved_cases=%d\n",
		caseCount, pairedPacketCount, decodePaths, unresolvedCases)
	if unresolvedCases > 0 {
		t.Errorf("CBR contract has %d unresolved row(s)", unresolvedCases)
	}
}

func checkCBREncoderOutput(label string, packets [][]byte, ranges []uint32, expected int) contractIssues {
	var issues contractIssues
	if len(packets) != expected {
		issues.add("%s returned %d packets, want %d", label, len(packets), expected)
	}
	if len(ranges) != expected {
		issues.add("%s returned %d final ranges, want %d", label, len(ranges), expected)
	}
	if len(packets) != len(ranges) {
		issues.add("%s packet/range counts differ: %d/%d", label, len(packets), len(ranges))
	}
	return issues
}

func countCBRContractPairedPackets(got, want [][]byte) int {
	count := 0
	for i := 0; i < min(len(got), len(want)); i++ {
		if len(got[i]) > 0 && len(want[i]) > 0 {
			count++
		}
	}
	return count
}

func checkCBRContractPacketLengths(got, want [][]byte) contractIssues {
	var issues contractIssues
	if len(got) != len(want) {
		issues.add("Go/C encoder packet counts differ: %d/%d", len(got), len(want))
	}
	for i := 0; i < min(len(got), len(want)); i++ {
		if len(got[i]) != len(want[i]) {
			issues.add("CBR packet %d byte lengths differ: Go=%d C=%d", i, len(got[i]), len(want[i]))
		}
	}
	return issues
}

func validateCBRContractPackets(label string, packets [][]byte, frameSize int) ([]int, contractIssues) {
	var issues contractIssues
	samples := make([]int, len(packets))
	for i, packet := range packets {
		info, err := gopus.ParsePacket(packet)
		if err != nil {
			issues.add("%s packet %d has invalid framing: %v", label, i, err)
			continue
		}
		samples[i] = info.TOC.FrameSize * info.FrameCount
		if samples[i] != frameSize {
			issues.add("%s packet %d decodes to %d samples/channel, want input frame size %d", label, i, samples[i], frameSize)
		}
	}
	return samples, issues
}

func decodeCBRContractWithC(packets [][]byte, channels int) (cbrContractDecode, error) {
	cases := make([]libopustest.DecodeDiffCase, len(packets))
	for i, packet := range packets {
		cases[i] = libopustest.DecodeDiffCase{
			Packet:    packet,
			Format:    libopustest.DecodeDiffFormatFloat32,
			FrameSize: 5760,
		}
	}
	results, err := libopustest.ProbeDecodeSequence(cbrContractSampleRate, channels, cases)
	if err != nil {
		return cbrContractDecode{}, err
	}

	decoded := cbrContractDecode{
		pcm:            make([]float32, 0, cbrContractSampleRate*channels),
		samplesPerCall: make([]int, 0, len(packets)),
		ranges:         make([]uint32, 0, len(packets)),
	}
	if len(results) != len(packets) {
		decoded.issues.add("C decoder returned %d packet results, want %d", len(results), len(packets))
	}
	for i, result := range results {
		decoded.samplesPerCall = append(decoded.samplesPerCall, int(result.Code))
		decoded.ranges = append(decoded.ranges, result.FinalRange)
		if result.Code <= 0 {
			decoded.issues.add("C decoder packet %d returned status %d", i, result.Code)
			if len(result.PCM) != 0 {
				decoded.issues.add("C decoder packet %d returned %d PCM bytes on failure", i, len(result.PCM))
			}
			continue
		}
		wantBytes := int(result.Code) * channels * 4
		if len(result.PCM) != wantBytes {
			decoded.issues.add("C decoder packet %d returned %d PCM bytes, want %d", i, len(result.PCM), wantBytes)
			continue
		}
		decoded.pcm = append(decoded.pcm, result.Float32()...)
	}
	return decoded, nil
}

func decodeCBRContractWithGo(packets [][]byte, channels int) (cbrContractDecode, error) {
	decoder, err := gopus.NewDecoder(gopus.DefaultDecoderConfig(cbrContractSampleRate, channels))
	if err != nil {
		return cbrContractDecode{}, err
	}
	decoded := cbrContractDecode{
		pcm:            make([]float32, 0, cbrContractSampleRate*channels),
		samplesPerCall: make([]int, 0, len(packets)),
		ranges:         make([]uint32, 0, len(packets)),
	}
	output := make([]float32, 5760*channels)
	for i, packet := range packets {
		n, decodeErr := decoder.Decode(packet, output)
		decoded.samplesPerCall = append(decoded.samplesPerCall, n)
		decoded.ranges = append(decoded.ranges, decoder.FinalRange())
		if decodeErr != nil {
			decoded.issues.add("Go decoder packet %d returned error: %v", i, decodeErr)
		}
		if n <= 0 {
			decoded.issues.add("Go decoder packet %d returned sample count %d", i, n)
			continue
		}
		if n > 5760 || n*channels > len(output) {
			decoded.issues.add("Go decoder packet %d returned out-of-range sample count %d", i, n)
			continue
		}
		decoded.pcm = append(decoded.pcm, output[:n*channels]...)
	}
	return decoded, nil
}

func validateCBRContractDecode(label string, decoded cbrContractDecode, expectedSamples []int, expectedPCMCount int) contractIssues {
	var issues contractIssues
	if len(decoded.samplesPerCall) != len(expectedSamples) {
		issues.add("%s returned %d sample counts, want %d", label, len(decoded.samplesPerCall), len(expectedSamples))
	}
	for i, expected := range expectedSamples {
		if expected <= 0 || i >= len(decoded.samplesPerCall) {
			continue
		}
		if decoded.samplesPerCall[i] != expected {
			issues.add("%s packet %d decoded %d samples/channel, packet duration is %d", label, i, decoded.samplesPerCall[i], expected)
		}
	}
	if len(decoded.pcm) != expectedPCMCount {
		issues.add("%s produced %d interleaved samples, want complete %d-sample sequence", label, len(decoded.pcm), expectedPCMCount)
	}
	if count, first := nonFiniteCBRContractPCM(decoded.pcm); count > 0 {
		issues.add("%s produced %d non-finite PCM samples (first=%d)", label, count, first)
	}
	return issues
}

func checkCBRContractRanges(label string, encoderRanges []uint32, cDecode cbrContractDecode, haveC bool, goDecode cbrContractDecode, haveGo bool) contractIssues {
	var issues contractIssues
	if !haveC || !haveGo {
		issues.add("%s range check lacks both decoder paths", label)
		return issues
	}
	if len(cDecode.ranges) != len(goDecode.ranges) {
		issues.add("%s C/Go decoder range counts differ: %d/%d", label, len(cDecode.ranges), len(goDecode.ranges))
	}
	if len(encoderRanges) != len(cDecode.ranges) || len(encoderRanges) != len(goDecode.ranges) {
		issues.add("%s encoder/C-decoder/Go-decoder range counts differ: %d/%d/%d", label, len(encoderRanges), len(cDecode.ranges), len(goDecode.ranges))
	}
	limit := min(len(encoderRanges), min(len(cDecode.ranges), len(goDecode.ranges)))
	for i := 0; i < limit; i++ {
		if encoderRanges[i] != cDecode.ranges[i] {
			issues.add("%s packet %d C decoder final range 0x%08x differs from encoder 0x%08x", label, i, cDecode.ranges[i], encoderRanges[i])
		}
		if encoderRanges[i] != goDecode.ranges[i] {
			issues.add("%s packet %d Go decoder final range 0x%08x differs from encoder 0x%08x", label, i, goDecode.ranges[i], encoderRanges[i])
		}
		if cDecode.ranges[i] != goDecode.ranges[i] {
			issues.add("%s packet %d same-packet C/Go decoder ranges differ: 0x%08x/0x%08x", label, i, cDecode.ranges[i], goDecode.ranges[i])
		}
	}
	return issues
}

func compareCBRPacketStreams(got, want [][]byte) (diffs, firstFrame, firstByte int) {
	firstFrame, firstByte = -1, -1
	limit := min(len(got), len(want))
	for i := 0; i < limit; i++ {
		if at := firstCBRByteDifference(got[i], want[i]); at >= 0 {
			diffs++
			if firstFrame < 0 {
				firstFrame, firstByte = i, at
			}
		}
	}
	diffs += abs(len(got) - len(want))
	if firstFrame < 0 && len(got) != len(want) {
		firstFrame = limit
	}
	return diffs, firstFrame, firstByte
}

func countEncoderRangeDifferences(got, want []uint32) int {
	count := abs(len(got) - len(want))
	for i := 0; i < min(len(got), len(want)); i++ {
		if got[i] != want[i] {
			count++
		}
	}
	return count
}

func compareCBRContractPCM(got, want []float32) (diffs, first int) {
	first = -1
	limit := min(len(got), len(want))
	for i := 0; i < limit; i++ {
		if math.Float32bits(got[i]) != math.Float32bits(want[i]) {
			diffs++
			if first < 0 {
				first = i
			}
		}
	}
	return diffs + abs(len(got)-len(want)), first
}

func finiteCBRContractPCM(pcm []float32) bool {
	count, _ := nonFiniteCBRContractPCM(pcm)
	return count == 0
}

func nonFiniteCBRContractPCM(pcm []float32) (count, first int) {
	first = -1
	for i, sample := range pcm {
		if math.IsNaN(float64(sample)) || math.IsInf(float64(sample), 0) {
			count++
			if first < 0 {
				first = i
			}
		}
	}
	return count, first
}

func TestCBRContractValidatorsRejectInvalidResults(t *testing.T) {
	if _, issues := validateCBRContractPackets("malformed", [][]byte{nil}, 960); issues.count == 0 {
		t.Fatal("packet validator accepted empty packet framing")
	}

	decoded := cbrContractDecode{
		pcm:            []float32{float32(math.NaN())},
		samplesPerCall: []int{240},
		ranges:         []uint32{0x1234},
	}
	if issues := validateCBRContractDecode("invalid decode", decoded, []int{480}, 2); issues.count == 0 {
		t.Fatal("decode validator accepted an incomplete, non-finite stream")
	}

	issues := checkCBRContractRanges(
		"range mismatch",
		[]uint32{0x1234},
		cbrContractDecode{ranges: []uint32{0x2345}}, true,
		cbrContractDecode{ranges: []uint32{0x3456}}, true,
	)
	if issues.count == 0 {
		t.Fatal("range validator accepted encoder/decoder final-range mismatches")
	}
	if issues := checkCBRContractPacketLengths([][]byte{{1, 2, 3}}, [][]byte{{1, 2}}); issues.count == 0 {
		t.Fatal("CBR packet budget validator accepted different packet byte lengths")
	}
}
