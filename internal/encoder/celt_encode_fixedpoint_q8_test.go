//go:build gopus_fixed_point

package encoder

import "github.com/thesyncim/gopus/internal/libopustest"

func fixedQ8OracleAnalysis(info AnalysisInfo) libopustest.CELTFixedQ8Analysis {
	if !info.Valid {
		return libopustest.CELTFixedQ8Analysis{}
	}
	return libopustest.CELTFixedQ8Analysis{
		Valid:               true,
		Tonality:            info.Tonality,
		TonalitySlope:       info.TonalitySlope,
		Noisiness:           info.NoisySpeech,
		Activity:            info.Activity,
		MusicProb:           info.MusicProb,
		MusicProbMin:        info.MusicProbMin,
		MusicProbMax:        info.MusicProbMax,
		Bandwidth:           info.BandwidthIndex,
		ActivityProbability: info.VADProb,
		MaxPitchRatio:       info.MaxPitchRatio,
		LeakBoost:           info.LeakBoost,
	}
}

func fixedQ8OracleFrame(enc *Encoder) libopustest.CELTFixedQ8Frame {
	_, maxBytes, _ := enc.LastFixedCELTControls()
	return libopustest.CELTFixedQ8Frame{
		PCM:           append([]int32(nil), enc.LastFixedCELTInputQ8()...),
		MaxBytes:      maxBytes,
		Analysis:      fixedQ8OracleAnalysis(enc.LastFixedCELTAnalysis()),
		SetPrediction: true,
		Prediction:    int32(enc.celtEncoder.Prediction()),
	}
}
