//go:build gopus_qext && !gopus_fixed_point

package celt

import (
	"bytes"
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestHD96kAutoCELTRawTraceMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const frameSize = 1920
	bitrates := []int{15000, 25000, 14000, 17000, 23000}
	pcm := make([]float32, frameSize*2*len(bitrates))
	for frame := range len(bitrates) {
		for i := range frameSize {
			low := 0.34 * math.Sin(2*math.Pi*6000*float64(i)/96000)
			high := 0.21 * math.Sin(2*math.Pi*30000*float64(i)/96000)
			for ch := range 2 {
				v := low + high*float64(ch+1)/2
				sample := int16(math.Round(v * 32767))
				pcm[(frame*frameSize+i)*2+ch] = float32(sample) / 32768
			}
		}
	}
	frames := make([]libopustest.QEXT96kAutoFrame, len(bitrates))
	for frame, bitrate := range bitrates {
		lo := frame * frameSize * 2
		frames[frame] = libopustest.QEXT96kAutoFrame{Bitrate: bitrate, PCM: pcm[lo : lo+frameSize*2]}
	}
	for _, qext := range []bool{false, true} {
		t.Run(fmt.Sprintf("qext_%t", qext), func(t *testing.T) {
			want, err := libopustest.ProbeQEXT96kAutoChannelsState(frameSize, 4000, 10, 24, qext, frames)
			if err != nil {
				libopustest.HelperUnavailable(t, "native 96 kHz raw CELT trace", err)
				return
			}
			enc := NewEncoder(2)
			enc.EnableHD96kMode()
			var stateDiffs []string
			var packetDiffs []string
			var pitchDiffs []string
			for frame, ref := range want {
				enc.SetQEXTEnabled(qext)
				enc.SetStreamChannels(int(ref.CELTStream))
				enc.SetLFE(false)
				enc.SetBitrate(int(ref.CELTBitrate))
				enc.SetVBR(true)
				enc.SetConstrainedVBR(false)
				enc.SetLSBDepth(24)
				enc.SetPrediction(2)
				enc.SetMaxPayloadBytes(int(ref.CELTMaxBytes))
				enc.SetTopLevelDelayCompensatedInput(true)
				enc.SetDCRejectEnabled(false)
				enc.SetLSBQuantizationEnabled(false)
				enc.SetDelayCompensationEnabled(false)
				previousPeriod := enc.prefilterPeriod
				previousGain := enc.prefilterGain
				payload, err := enc.EncodeFrame(ref.CELTInput, frameSize)
				if err != nil {
					t.Fatalf("frame %d encode: %v", frame, err)
				}
				wantMain, wantSide := splitHD96kOraclePacket(t, ref.Packet)
				if !bytes.Equal(payload, wantMain) || !bytes.Equal(enc.LastQEXTPayload(), wantSide) ||
					enc.FinalRange() != ref.FinalRange {
					packetDiffs = append(packetDiffs, fmt.Sprintf("frame %d main{%s} side{%s} range=%08x/%08x",
						frame, byteDiff(payload, wantMain), byteDiff(enc.LastQEXTPayload(), wantSide),
						enc.FinalRange(), ref.FinalRange))
				}
				got := hd96kFloatStateWords(enc, qext)
				if len(ref.PitchBuffer) != 0 {
					if ref.CELTCalls != 1 || ref.CELTFrameSize != uint32(frameSize) {
						pitchDiffs = append(pitchDiffs, fmt.Sprintf("frame %d C CELT calls/size=%d/%d want 1/%d", frame, ref.CELTCalls, ref.CELTFrameSize, frameSize))
					}
					pitchBuffer := enc.scratch.prefilterPitchBuf
					if len(pitchBuffer) < len(ref.PitchBuffer) {
						pitchDiffs = append(pitchDiffs, fmt.Sprintf("frame %d downsample buffer length Go=%d C=%d", frame, len(pitchBuffer), len(ref.PitchBuffer)))
					} else {
						if index := firstFloat32Diff(pitchBuffer[:len(ref.PitchBuffer)], ref.PitchBuffer); index >= 0 {
							pitchDiffs = append(pitchDiffs, fmt.Sprintf("frame %d downsample[%d]=%08x/%08x", frame, index,
								math.Float32bits(pitchBuffer[index]), math.Float32bits(ref.PitchBuffer[index])))
						}
						maxPeriod := enc.combMaxPeriod()
						minPeriod := enc.combMinPeriod()
						var stageScratch encoderScratch
						searchResult := pitchSearch(pitchBuffer[maxPeriod>>1:], pitchBuffer, frameSize, maxPeriod-3*minPeriod, &stageScratch)
						period := maxPeriod - searchResult
						gain := removeDoubling(pitchBuffer, maxPeriod, minPeriod, frameSize, &period,
							previousPeriod, previousGain, &stageScratch)
						gainBits := math.Float32bits(gain)
						if int32(searchResult) != ref.PitchSearch || int32(maxPeriod-searchResult) != ref.RemoveInput ||
							int32(period) != ref.RemoveOutput || gainBits != ref.RemoveGain {
							pitchDiffs = append(pitchDiffs, fmt.Sprintf("frame %d search=%d/%d remove=%d->%d/%d->%d gain=%08x/%08x",
								frame, searchResult, ref.PitchSearch, maxPeriod-searchResult, ref.RemoveInput, period, ref.RemoveOutput, gainBits, ref.RemoveGain)+
								fmt.Sprintf(" state period=%d/%d direct-period=%d", enc.prefilterPeriod, ref.CELTState[7], period/enc.combScale()))
						}
					}
				}
				// AnalysisInfo.bandwidth is not initialized unless analysis.valid is
				// set. Its source value is indeterminate in this oracle case.
				if len(got) > 24 && len(ref.CELTState) > 24 && got[23] == 0 {
					got[24] = ref.CELTState[24]
				}
				if !equalWords(got, ref.CELTState) {
					first := firstWordDiff(got, ref.CELTState)
					stateDiffs = append(stateDiffs, fmt.Sprintf("frame %d first word=%d Go=%08x C=%08x lengths=%d/%d differences=%d",
						frame, first, wordAt(got, first), wordAt(ref.CELTState, first), len(got), len(ref.CELTState), wordDiffCount(got, ref.CELTState))+" "+summarizeStateDiffs(got, ref.CELTState))
				}
			}
			if len(stateDiffs) != 0 || len(packetDiffs) != 0 || len(pitchDiffs) != 0 {
				t.Fatalf("raw CELT packet differences: %v; state differences: %v; pitch stages: %v", packetDiffs, stateDiffs, pitchDiffs)
			}
		})
	}
}

func firstFloat32Diff(a, b []float32) int {
	if len(a) != len(b) {
		return min(len(a), len(b))
	}
	for i := range a {
		if math.Float32bits(a[i]) != math.Float32bits(b[i]) {
			return i
		}
	}
	return -1
}

func hd96kFloatStateWords(enc *Encoder, qext bool) []uint32 {
	words := []uint32{
		enc.rng,
		uint32(enc.spreadDecision),
		math.Float32bits(float32(enc.delayedIntra)),
		uint32(enc.tonalAverage),
		uint32(enc.lastCodedBands),
		uint32(enc.hfAverage),
		uint32(enc.tapsetDecision),
		uint32(enc.prefilterPeriod),
		math.Float32bits(enc.prefilterGain),
		uint32(enc.prefilterTapset),
		uint32(enc.consecTransient),
		uint32(enc.vbrReservoir),
		uint32(enc.vbrDrift),
		uint32(enc.vbrOffset),
		uint32(enc.vbrCount),
		math.Float32bits(float32(enc.overlapMax)),
		math.Float32bits(float32(enc.lastStereoSaving)),
		uint32(enc.intensity),
		math.Float32bits(float32(enc.specAvg)),
		boolWord(enc.forceIntra),
		boolWord(enc.disablePrefilter),
		uint32(enc.silkSignalType),
		uint32(enc.silkOffset),
		boolWord(enc.analysisValid),
		uint32(enc.analysisBandwidth),
		uint32(enc.targetBitrate),
		uint32(enc.streamChannels),
		boolWord(qext),
		2, // QEXT scale at Fs=96000.
		uint32(enc.lsbDepth),
		0,  // start band
		21, // end band
		boolWord(enc.vbr),
		boolWord(enc.constrainedVBR),
		uint32(enc.packetLoss),
	}
	appendFloatArray := func(values []float32) {
		words = append(words, uint32(len(values)))
		for _, value := range values {
			words = append(words, math.Float32bits(value))
		}
	}
	appendGLogArray := func(values []celtGLog) {
		words = append(words, uint32(len(values)))
		for _, value := range values {
			words = append(words, math.Float32bits(float32(value)))
		}
	}
	appendFloatArray(enc.preemphState)
	appendFloatArray(enc.overlapBuffer)
	appendFloatArray(enc.prefilterMem)
	appendGLogArray(enc.prevEnergy) // oldBandE
	appendGLogArray(enc.prevLogEnergy)
	appendGLogArray(enc.prevEnergy2)
	appendGLogArray(enc.energyError)
	qextOldBandE := make([]celtGLog, 2*14)
	if qext && enc.qext != nil {
		copy(qextOldBandE, enc.qext.oldBandE)
	}
	appendGLogArray(qextOldBandE)
	return words
}

func boolWord(value bool) uint32 {
	if value {
		return 1
	}
	return 0
}

func equalWords(a, b []uint32) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func firstWordDiff(a, b []uint32) int {
	limit := min(len(a), len(b))
	for i := 0; i < limit; i++ {
		if a[i] != b[i] {
			return i
		}
	}
	return limit
}

func wordDiffCount(a, b []uint32) int {
	limit := min(len(a), len(b))
	diffs := abs(len(a) - len(b))
	for i := range limit {
		if a[i] != b[i] {
			diffs++
		}
	}
	return diffs
}

func summarizeStateDiffs(a, b []uint32) string {
	const scalarCount = 35
	names := []string{"rng", "spread", "delayed-intra", "tonal-average", "last-bands", "hf-average", "tapset-decision", "prefilter-period", "prefilter-gain", "prefilter-tapset", "consecutive-transient", "vbr-reservoir", "vbr-drift", "vbr-offset", "vbr-count", "overlap-max", "stereo-saving", "intensity", "spec-average", "force-intra", "disable-prefilter", "silk-signal", "silk-offset", "analysis-valid", "analysis-bandwidth", "bitrate", "stream-channels", "qext", "qext-scale", "lsb-depth", "start", "end", "vbr", "constrained-vbr", "packet-loss"}
	if len(a) < scalarCount || len(b) < scalarCount {
		return "short state"
	}
	var result []string
	for i, name := range names {
		if a[i] != b[i] {
			result = append(result, fmt.Sprintf("%s=%08x/%08x", name, a[i], b[i]))
		}
	}
	offsetA, offsetB := scalarCount, scalarCount
	arrayNames := []string{"preemph", "overlap", "prefilter", "old-band-energy", "old-log-energy", "old-log-energy2", "energy-error", "qext-old-band-energy"}
	for _, name := range arrayNames {
		if offsetA >= len(a) || offsetB >= len(b) {
			result = append(result, name+"=missing")
			break
		}
		countA, countB := int(a[offsetA]), int(b[offsetB])
		offsetA++
		offsetB++
		diffs := 0
		firstIndex := -1
		for i := 0; i < min(countA, countB) && offsetA+i < len(a) && offsetB+i < len(b); i++ {
			if a[offsetA+i] != b[offsetB+i] {
				diffs++
				if firstIndex == -1 {
					firstIndex = i
				}
			}
		}
		if countA != countB || diffs != 0 {
			firstGot, firstWant := uint32(0), uint32(0)
			if firstIndex >= 0 {
				firstGot, firstWant = a[offsetA+firstIndex], b[offsetB+firstIndex]
			}
			result = append(result, fmt.Sprintf("%s=%d/%d values-differ=%d first[%d]=%08x/%08x", name, countA, countB, diffs, firstIndex, firstGot, firstWant))
		}
		offsetA += countA
		offsetB += countB
	}
	return fmt.Sprint(result)
}

func wordAt(words []uint32, index int) uint32 {
	if index < 0 || index >= len(words) {
		return 0
	}
	return words[index]
}

func splitHD96kOraclePacket(t *testing.T, packet []byte) (main, side []byte) {
	t.Helper()
	if len(packet) < 1 {
		t.Fatal("empty selected CELT packet")
	}
	if packet[0]&3 == 0 {
		return packet[1:], nil
	}
	if len(packet) < 4 || packet[0]&3 != 3 || packet[1] != 0x41 {
		t.Fatalf("unexpected native 96 kHz packet header %x", packet[:min(2, len(packet))])
	}
	offset, padding := 2, 0
	for {
		if offset >= len(packet) {
			t.Fatal("truncated native 96 kHz packet padding")
		}
		b := int(packet[offset])
		offset++
		if b == 255 {
			padding += 254
			continue
		}
		padding += b
		break
	}
	end := len(packet) - padding
	if end <= offset || end >= len(packet) || packet[end] != 0xf8 {
		t.Fatalf("invalid native 96 kHz QEXT layout: offset=%d end=%d packet=%d", offset, end, len(packet))
	}
	return packet[offset:end], packet[end+1:]
}

func byteDiff(got, want []byte) string {
	limit := min(len(got), len(want))
	first := limit
	for i := range limit {
		if got[i] != want[i] {
			first = i
			break
		}
	}
	return fmt.Sprintf("len=%d/%d first=%d", len(got), len(want), first)
}
