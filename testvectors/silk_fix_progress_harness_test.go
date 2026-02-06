//go:build cgo_libopus

package testvectors

import (
	"bytes"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/rangecoding"
	"github.com/thesyncim/gopus/silk"
	"github.com/thesyncim/gopus/types"
)

// TestSILKFixProgressHarness is a focused parity/quality tracker for SILK WB 20ms.
// It is intentionally dashboard-style so agents can:
// 1) run one focused subtest while fixing a specific path, and
// 2) track movement against stable baselines and stretch goals.
//
// Examples:
// go test ./testvectors -tags cgo_libopus -run TestSILKFixProgressHarness -v
// go test ./testvectors -tags cgo_libopus -run TestSILKFixProgressHarness/seed_focus -v
// GOPUS_PROGRESS_FRAMES=30 go test ./testvectors -tags cgo_libopus -run TestSILKFixProgressHarness -v
// GOPUS_PROGRESS_JSON=/tmp/silk_progress.json go test ./testvectors -tags cgo_libopus -run TestSILKFixProgressHarness -v
func TestSILKFixProgressHarness(t *testing.T) {
	frames := progressFrameCountFromEnv(50)
	metrics := collectSILKFixProgressMetrics(t, frames)
	scoreboard := buildSILKProgressScoreboard(metrics)

	t.Run("dashboard", func(t *testing.T) {
		logSILKProgressScoreboard(t, metrics, scoreboard)
		writeSILKProgressJSONIfRequested(t, metrics, scoreboard)
	})

	// Stable baseline guards (must not regress).
	t.Run("gain_guard", func(t *testing.T) {
		if metrics.GainAvgAbsDiff > 0.20 {
			t.Fatalf("gain index avg abs diff regressed: got %.4f, want <= 0.20", metrics.GainAvgAbsDiff)
		}
		if metrics.CompareFrames >= 24 && metrics.GainsID.FirstFrame >= 0 && metrics.GainsID.FirstFrame < 23 {
			t.Fatalf("first GainsID mismatch regressed early: got frame %d, want >= 23", metrics.GainsID.FirstFrame)
		}
	})

	// Focused tracking guards by area. Thresholds are set from observed baseline
	// with small headroom so they catch regressions but do not block ongoing work.
	t.Run("seed_focus", func(t *testing.T) {
		seedRate := mismatchRate(metrics.Seed, metrics.CompareFrames)
		if seedRate > 0.84 {
			t.Fatalf("seed mismatch rate regressed: got %.3f, want <= 0.84", seedRate)
		}
	})

	t.Run("pitch_focus", func(t *testing.T) {
		lagRate := mismatchRate(metrics.PitchLag, metrics.CompareFrames)
		contourRate := mismatchRate(metrics.PitchContour, metrics.CompareFrames)
		if lagRate > 0.60 {
			t.Fatalf("pitch lag mismatch rate regressed: got %.3f, want <= 0.60", lagRate)
		}
		if contourRate > 0.60 {
			t.Fatalf("pitch contour mismatch rate regressed: got %.3f, want <= 0.60", contourRate)
		}
	})

	t.Run("nsq_focus", func(t *testing.T) {
		prevGainRate := mismatchRate(metrics.PreNSQPrevGain, metrics.CompareFrames)
		sltpIdxRate := mismatchRate(metrics.PreNSQSLTPBufIdx, metrics.CompareFrames)
		pitchBufRate := mismatchRate(metrics.PrePitchBufHash, metrics.CompareFrames)
		if prevGainRate > 0.20 {
			t.Fatalf("pre-state NSQ prevGain mismatch rate regressed: got %.3f, want <= 0.20", prevGainRate)
		}
		if sltpIdxRate > 0.04 {
			t.Fatalf("pre-state NSQ sLTPBufIdx mismatch rate regressed: got %.3f, want <= 0.04", sltpIdxRate)
		}
		if pitchBufRate > 0.04 {
			t.Fatalf("pre-state pitch x_buf hash mismatch rate regressed: got %.3f, want <= 0.04", pitchBufRate)
		}
	})
}

type frameMismatch struct {
	Count      int `json:"count"`
	FirstFrame int `json:"first_frame"`
}

func newFrameMismatch() frameMismatch {
	return frameMismatch{FirstFrame: -1}
}

func (m *frameMismatch) add(frame int) {
	m.Count++
	if m.FirstFrame < 0 {
		m.FirstFrame = frame
	}
}

type silkFixProgressMetrics struct {
	FramesRequested int `json:"frames_requested"`
	CompareFrames   int `json:"compare_frames"`

	GainAbsDiffSum   int     `json:"gain_abs_diff_sum"`
	GainAbsDiffCount int     `json:"gain_abs_diff_count"`
	GainAvgAbsDiff   float64 `json:"gain_avg_abs_diff"`

	GainsID      frameMismatch `json:"gains_id"`
	LTPScale     frameMismatch `json:"ltp_scale"`
	NLSFInterp   frameMismatch `json:"nlsf_interp"`
	PER          frameMismatch `json:"per"`
	PitchLag     frameMismatch `json:"pitch_lag"`
	PitchContour frameMismatch `json:"pitch_contour"`
	Seed         frameMismatch `json:"seed"`
	SignalType   frameMismatch `json:"signal_type"`

	LTPIndexMismatch int `json:"ltp_index_mismatch"`
	LTPIndexCount    int `json:"ltp_index_count"`

	PrePrevLag          frameMismatch `json:"pre_prev_lag"`
	PreNSQLagPrev       frameMismatch `json:"pre_nsq_lag_prev"`
	PreNSQSLTPBufIdx    frameMismatch `json:"pre_nsq_sltp_buf_idx"`
	PreNSQSLTPShpBufIdx frameMismatch `json:"pre_nsq_sltp_shp_buf_idx"`
	PreNSQPrevGain      frameMismatch `json:"pre_nsq_prev_gain"`
	PreNSQSeed          frameMismatch `json:"pre_nsq_seed"`
	PrePitchBufHash     frameMismatch `json:"pre_pitch_buf_hash"`
	PreNBitsExceeded    frameMismatch `json:"pre_nbits_exceeded"`
	PreTargetRate       frameMismatch `json:"pre_target_rate"`
	PreSNR              frameMismatch `json:"pre_snr"`
}

func newSILKFixProgressMetrics(framesRequested int) silkFixProgressMetrics {
	return silkFixProgressMetrics{
		FramesRequested:     framesRequested,
		GainsID:             newFrameMismatch(),
		LTPScale:            newFrameMismatch(),
		NLSFInterp:          newFrameMismatch(),
		PER:                 newFrameMismatch(),
		PitchLag:            newFrameMismatch(),
		PitchContour:        newFrameMismatch(),
		Seed:                newFrameMismatch(),
		SignalType:          newFrameMismatch(),
		PrePrevLag:          newFrameMismatch(),
		PreNSQLagPrev:       newFrameMismatch(),
		PreNSQSLTPBufIdx:    newFrameMismatch(),
		PreNSQSLTPShpBufIdx: newFrameMismatch(),
		PreNSQPrevGain:      newFrameMismatch(),
		PreNSQSeed:          newFrameMismatch(),
		PrePitchBufHash:     newFrameMismatch(),
		PreNBitsExceeded:    newFrameMismatch(),
		PreTargetRate:       newFrameMismatch(),
		PreSNR:              newFrameMismatch(),
	}
}

func collectSILKFixProgressMetrics(t *testing.T, frames int) silkFixProgressMetrics {
	t.Helper()
	opusDemo := findOpusDemo(t)

	const (
		sampleRate = 48000
		channels   = 1
		frameSize  = 960
		bitrate    = 32000
	)

	original := generateEncoderTestSignal(frameSize*frames*channels, channels)
	original = quantizeFloat32SignalToPCM16(original)
	metrics := newSILKFixProgressMetrics(frames)

	goEnc := encoder.NewEncoder(sampleRate, channels)
	goEnc.SetMode(encoder.ModeSILK)
	goEnc.SetBandwidth(types.BandwidthWideband)
	goEnc.SetBitrate(bitrate)
	goEnc.SetLSBDepth(16)

	framePreTrace := &silk.FrameStateTrace{}
	goEnc.SetSilkTrace(&silk.EncoderTrace{FramePre: framePreTrace})

	gopusPackets := make([][]byte, 0, frames)
	framePreTraces := make([]silk.FrameStateTrace, 0, frames)

	samplesPerFrame := frameSize * channels
	for i := 0; i < frames; i++ {
		start := i * samplesPerFrame
		end := start + samplesPerFrame
		pcm := float32ToFloat64(original[start:end])
		packet, err := goEnc.Encode(pcm, frameSize)
		if err != nil {
			t.Fatalf("gopus encode failed at frame %d: %v", i, err)
		}
		packetCopy := make([]byte, len(packet))
		copy(packetCopy, packet)
		gopusPackets = append(gopusPackets, packetCopy)

		snapshot := *framePreTrace
		snapshot.PitchBuf = append([]float32(nil), framePreTrace.PitchBuf...)
		framePreTraces = append(framePreTraces, snapshot)
	}

	tmpdir := t.TempDir()
	inRaw := filepath.Join(tmpdir, "input.pcm")
	outBit := filepath.Join(tmpdir, "output.bit")
	if err := writeRawPCM16(inRaw, original); err != nil {
		t.Fatalf("write input pcm: %v", err)
	}

	args := []string{
		"-e", "restricted-silk",
		fmt.Sprintf("%d", sampleRate),
		fmt.Sprintf("%d", channels),
		fmt.Sprintf("%d", bitrate),
		"-bandwidth", "WB",
		"-framesize", "20",
		"-16",
		inRaw, outBit,
	}
	cmd := exec.Command(opusDemo, args...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		t.Fatalf("opus_demo failed: %v\n%s", err, stderr.String())
	}

	libPackets, err := parseOpusDemoBitstream(outBit)
	if err != nil {
		t.Fatalf("parse opus_demo output: %v", err)
	}
	if len(libPackets) == 0 {
		t.Fatal("no libopus packets produced")
	}

	compareCount := len(gopusPackets)
	if len(libPackets) < compareCount {
		compareCount = len(libPackets)
	}
	if compareCount == 0 {
		t.Fatal("no packets to compare")
	}
	metrics.CompareFrames = compareCount

	for i := 0; i < compareCount; i++ {
		if i < len(framePreTraces) {
			snapPre, ok := captureLibopusOpusSilkStateBeforeFrame(original, sampleRate, channels, bitrate, frameSize, i)
			if !ok {
				t.Fatalf("failed to capture libopus pre-state at frame %d", i)
			}
			pre := framePreTraces[i]
			if pre.PrevLag != snapPre.PrevLag {
				metrics.PrePrevLag.add(i)
			}
			if pre.NSQLagPrev != snapPre.NSQLagPrev {
				metrics.PreNSQLagPrev.add(i)
			}
			if pre.NSQSLTPBufIdx != snapPre.NSQSLTPBufIdx {
				metrics.PreNSQSLTPBufIdx.add(i)
			}
			if pre.NSQSLTPShpBufIdx != snapPre.NSQSLTPShpBufIdx {
				metrics.PreNSQSLTPShpBufIdx.add(i)
			}
			if pre.NSQPrevGainQ16 != snapPre.NSQPrevGainQ16 {
				metrics.PreNSQPrevGain.add(i)
			}
			if pre.NSQRandSeed != snapPre.NSQRandSeed {
				metrics.PreNSQSeed.add(i)
			}
			if pre.PitchBufHash != snapPre.PitchXBufHash {
				metrics.PrePitchBufHash.add(i)
			}
			if pre.NBitsExceeded != snapPre.NBitsExceeded {
				metrics.PreNBitsExceeded.add(i)
			}
			if pre.TargetRateBps != snapPre.TargetRateBps {
				metrics.PreTargetRate.add(i)
				t.Logf("Frame %d targetRate mismatch: go=%d lib=%d (diff=%d) | nBitsExceeded: go=%d lib=%d (diff=%d) | inputRate: go=%d lib=%d | SNRDBQ7: go=%d lib=%d",
					i, pre.TargetRateBps, snapPre.TargetRateBps, pre.TargetRateBps-snapPre.TargetRateBps,
					pre.NBitsExceeded, snapPre.NBitsExceeded, pre.NBitsExceeded-snapPre.NBitsExceeded,
					pre.InputRateBps, snapPre.SilkModeBitRate,
					pre.SNRDBQ7, snapPre.SNRDBQ7)
			} else if pre.NBitsExceeded != snapPre.NBitsExceeded {
				// nBitsExceeded diverges but targetRate still matches
				t.Logf("Frame %d nBitsExceeded diverge (targetRate MATCH): go=%d lib=%d (diff=%d) | targetRate=%d | nBitsExceeded: go=%d lib=%d",
					i, pre.NBitsExceeded, snapPre.NBitsExceeded, pre.NBitsExceeded-snapPre.NBitsExceeded,
					pre.TargetRateBps, pre.NBitsExceeded, snapPre.NBitsExceeded)
			}
			if pre.SNRDBQ7 != snapPre.SNRDBQ7 {
				metrics.PreSNR.add(i)
				if pre.TargetRateBps == snapPre.TargetRateBps {
					t.Logf("Frame %d SNR mismatch (targetRate MATCH=%d): go=%d lib=%d",
						i, pre.TargetRateBps, pre.SNRDBQ7, snapPre.SNRDBQ7)
				}
			}
		}
	}

	goDec := silk.NewDecoder()
	libDec := silk.NewDecoder()

	for i := 0; i < compareCount; i++ {
		goPayload := gopusPackets[i]
		libPayload := libPackets[i].data
		if len(goPayload) < 2 || len(libPayload) < 2 {
			continue
		}

		goPayload = goPayload[1:]
		libPayload = libPayload[1:]

		var rdGo, rdLib rangecoding.Decoder
		rdGo.Init(goPayload)
		rdLib.Init(libPayload)

		if _, err := goDec.DecodeFrame(&rdGo, silk.BandwidthWideband, silk.Frame20ms, true); err != nil {
			t.Fatalf("gopus decode failed at frame %d: %v", i, err)
		}
		if _, err := libDec.DecodeFrame(&rdLib, silk.BandwidthWideband, silk.Frame20ms, true); err != nil {
			t.Fatalf("libopus decode failed at frame %d: %v", i, err)
		}

		goParams := goDec.GetLastFrameParams()
		libParams := libDec.GetLastFrameParams()

		if goDec.GetLastSignalType() != libDec.GetLastSignalType() {
			metrics.SignalType.add(i)
		}
		if goParams.LTPScaleIndex != libParams.LTPScaleIndex {
			metrics.LTPScale.add(i)
		}
		if goParams.NLSFInterpCoefQ2 != libParams.NLSFInterpCoefQ2 {
			metrics.NLSFInterp.add(i)
		}
		if goParams.PERIndex != libParams.PERIndex {
			metrics.PER.add(i)
		}
		if goParams.LagIndex != libParams.LagIndex {
			metrics.PitchLag.add(i)
		}
		if goParams.ContourIndex != libParams.ContourIndex {
			metrics.PitchContour.add(i)
		}
		if goParams.Seed != libParams.Seed {
			metrics.Seed.add(i)
		}

		nGains := len(goParams.GainIndices)
		if len(libParams.GainIndices) < nGains {
			nGains = len(libParams.GainIndices)
		}
		for k := 0; k < nGains; k++ {
			diff := goParams.GainIndices[k] - libParams.GainIndices[k]
			if diff < 0 {
				diff = -diff
			}
			metrics.GainAbsDiffSum += diff
			metrics.GainAbsDiffCount++
		}

		nbSubfr := len(goParams.GainIndices)
		goGainsID := gainsIDFromIndices(goParams.GainIndices, nbSubfr)
		libGainsID := gainsIDFromIndices(libParams.GainIndices, nbSubfr)
		if goGainsID != libGainsID {
			metrics.GainsID.add(i)
		}

		nLTP := len(goParams.LTPIndices)
		if len(libParams.LTPIndices) < nLTP {
			nLTP = len(libParams.LTPIndices)
		}
		for k := 0; k < nLTP; k++ {
			if goParams.LTPIndices[k] != libParams.LTPIndices[k] {
				metrics.LTPIndexMismatch++
			}
			metrics.LTPIndexCount++
		}
	}

	if metrics.GainAbsDiffCount > 0 {
		metrics.GainAvgAbsDiff = float64(metrics.GainAbsDiffSum) / float64(metrics.GainAbsDiffCount)
	}

	return metrics
}

type progressScoreRow struct {
	Name           string  `json:"name"`
	Group          string  `json:"group"`
	Value          float64 `json:"value"`
	Baseline       float64 `json:"baseline"`
	Goal           float64 `json:"goal"`
	HigherIsBetter bool    `json:"higher_is_better"`
	Status         string  `json:"status"`
	DistanceToGoal float64 `json:"distance_to_goal"`
}

func (r progressScoreRow) scoreStatus() string {
	if r.HigherIsBetter {
		if r.Value >= r.Goal {
			return "goal"
		}
		if r.Value >= r.Baseline {
			return "ok"
		}
		return "regressed"
	}
	if r.Value <= r.Goal {
		return "goal"
	}
	if r.Value <= r.Baseline {
		return "ok"
	}
	return "regressed"
}

func (r progressScoreRow) distance() float64 {
	if r.HigherIsBetter {
		if r.Value >= r.Goal {
			return 0
		}
		span := r.Goal - r.Baseline
		if span <= 0 {
			span = 1
		}
		return (r.Goal - r.Value) / span
	}
	if r.Value <= r.Goal {
		return 0
	}
	span := r.Baseline - r.Goal
	if span <= 0 {
		span = 1
	}
	return (r.Value - r.Goal) / span
}

func buildSILKProgressScoreboard(metrics silkFixProgressMetrics) []progressScoreRow {
	frames := metrics.CompareFrames
	rows := []progressScoreRow{
		{
			Name:           "gain_avg_abs_diff",
			Group:          "gain",
			Value:          metrics.GainAvgAbsDiff,
			Baseline:       0.20,
			Goal:           0.05,
			HigherIsBetter: false,
		},
		{
			Name:           "first_gainsid_mismatch_frame",
			Group:          "gain",
			Value:          mismatchFirstAsScore(metrics.GainsID, frames),
			Baseline:       23,
			Goal:           45,
			HigherIsBetter: true,
		},
		{
			Name:           "seed_mismatch_rate",
			Group:          "seed",
			Value:          mismatchRate(metrics.Seed, frames),
			Baseline:       0.84,
			Goal:           0.10,
			HigherIsBetter: false,
		},
		{
			Name:           "pitch_lag_mismatch_rate",
			Group:          "pitch",
			Value:          mismatchRate(metrics.PitchLag, frames),
			Baseline:       0.60,
			Goal:           0.10,
			HigherIsBetter: false,
		},
		{
			Name:           "pitch_contour_mismatch_rate",
			Group:          "pitch",
			Value:          mismatchRate(metrics.PitchContour, frames),
			Baseline:       0.60,
			Goal:           0.10,
			HigherIsBetter: false,
		},
		{
			Name:           "pre_nsq_prev_gain_mismatch_rate",
			Group:          "nsq",
			Value:          mismatchRate(metrics.PreNSQPrevGain, frames),
			Baseline:       0.20,
			Goal:           0.00,
			HigherIsBetter: false,
		},
		{
			Name:           "pre_nsq_sltp_bufidx_mismatch_rate",
			Group:          "nsq",
			Value:          mismatchRate(metrics.PreNSQSLTPBufIdx, frames),
			Baseline:       0.04,
			Goal:           0.00,
			HigherIsBetter: false,
		},
		{
			Name:           "pre_pitch_buf_hash_mismatch_rate",
			Group:          "nsq",
			Value:          mismatchRate(metrics.PrePitchBufHash, frames),
			Baseline:       0.04,
			Goal:           0.00,
			HigherIsBetter: false,
		},
	}

	for i := range rows {
		rows[i].Status = rows[i].scoreStatus()
		rows[i].DistanceToGoal = rows[i].distance()
	}
	return rows
}

func logSILKProgressScoreboard(t *testing.T, metrics silkFixProgressMetrics, scoreboard []progressScoreRow) {
	t.Helper()
	t.Logf("SILK fix progress: requested=%d compare=%d", metrics.FramesRequested, metrics.CompareFrames)
	t.Logf("raw mismatches: gainsID=%d ltpScale=%d nlsfInterp=%d per=%d lag=%d contour=%d seed=%d signalType=%d ltpIndex=%d/%d prePrevLag=%d preNSQLagPrev=%d preNSQSLTPBufIdx=%d preNSQSLTPShpBufIdx=%d preNSQPrevGain=%d preNSQSeed=%d prePitchBufHash=%d preNBitsExceeded=%d preTargetRate=%d preSNR=%d",
		metrics.GainsID.Count,
		metrics.LTPScale.Count,
		metrics.NLSFInterp.Count,
		metrics.PER.Count,
		metrics.PitchLag.Count,
		metrics.PitchContour.Count,
		metrics.Seed.Count,
		metrics.SignalType.Count,
		metrics.LTPIndexMismatch,
		metrics.LTPIndexCount,
		metrics.PrePrevLag.Count,
		metrics.PreNSQLagPrev.Count,
		metrics.PreNSQSLTPBufIdx.Count,
		metrics.PreNSQSLTPShpBufIdx.Count,
		metrics.PreNSQPrevGain.Count,
		metrics.PreNSQSeed.Count,
		metrics.PrePitchBufHash.Count,
		metrics.PreNBitsExceeded.Count,
		metrics.PreTargetRate.Count,
		metrics.PreSNR.Count,
	)

	for _, row := range scoreboard {
		t.Logf("score %-32s group=%-5s value=%8.4f baseline=%8.4f goal=%8.4f status=%-9s dist=%6.3f",
			row.Name, row.Group, row.Value, row.Baseline, row.Goal, row.Status, row.DistanceToGoal)
	}

	blockers := append([]progressScoreRow(nil), scoreboard...)
	sort.Slice(blockers, func(i, j int) bool {
		if blockers[i].DistanceToGoal == blockers[j].DistanceToGoal {
			return blockers[i].Name < blockers[j].Name
		}
		return blockers[i].DistanceToGoal > blockers[j].DistanceToGoal
	})
	top := 3
	if len(blockers) < top {
		top = len(blockers)
	}
	for i := 0; i < top; i++ {
		b := blockers[i]
		t.Logf("top blocker %d: %s (group=%s status=%s value=%.4f)", i+1, b.Name, b.Group, b.Status, b.Value)
	}
}

func writeSILKProgressJSONIfRequested(t *testing.T, metrics silkFixProgressMetrics, scoreboard []progressScoreRow) {
	t.Helper()
	outPath := strings.TrimSpace(os.Getenv("GOPUS_PROGRESS_JSON"))
	if outPath == "" {
		return
	}

	payload := struct {
		FramesRequested int                    `json:"frames_requested"`
		CompareFrames   int                    `json:"compare_frames"`
		Metrics         silkFixProgressMetrics `json:"metrics"`
		Scoreboard      []progressScoreRow     `json:"scoreboard"`
	}{
		FramesRequested: metrics.FramesRequested,
		CompareFrames:   metrics.CompareFrames,
		Metrics:         metrics,
		Scoreboard:      scoreboard,
	}

	data, err := json.MarshalIndent(payload, "", "  ")
	if err != nil {
		t.Fatalf("marshal progress json: %v", err)
	}
	if err := os.WriteFile(outPath, data, 0o644); err != nil {
		t.Fatalf("write progress json (%s): %v", outPath, err)
	}
	t.Logf("wrote SILK progress JSON: %s", outPath)
}

func mismatchRate(m frameMismatch, total int) float64 {
	if total <= 0 {
		return 0
	}
	return float64(m.Count) / float64(total)
}

func mismatchFirstAsScore(m frameMismatch, total int) float64 {
	if total <= 0 {
		return 0
	}
	if m.FirstFrame < 0 {
		return float64(total)
	}
	return float64(m.FirstFrame)
}

func progressFrameCountFromEnv(defaultFrames int) int {
	raw := strings.TrimSpace(os.Getenv("GOPUS_PROGRESS_FRAMES"))
	if raw == "" {
		return defaultFrames
	}
	v, err := strconv.Atoi(raw)
	if err != nil || v <= 0 {
		return defaultFrames
	}
	return v
}

func quantizeFloat32SignalToPCM16(in []float32) []float32 {
	if len(in) == 0 {
		return in
	}
	out := make([]float32, len(in))
	for i, s := range in {
		v := int32(s * 32768.0)
		if s >= 0 {
			v = int32(s*32768.0 + 0.5)
		} else {
			v = int32(s*32768.0 - 0.5)
		}
		if v > 32767 {
			v = 32767
		} else if v < -32768 {
			v = -32768
		}
		out[i] = float32(v) / 32768.0
	}
	return out
}
