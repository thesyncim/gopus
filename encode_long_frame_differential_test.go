package gopus

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/testsignal"
)

// TestEncodeDifferentialLongFrames encodes the packet durations that
// opus_encode_native splits into several frames and repacketizes (SILK 80, 100
// and 120 ms; hybrid and CELT 40 and 60 ms; the same durations in auto mode)
// against libopus with the same 4000-byte output buffer, and requires every
// packet and final range to match.
func TestEncodeDifferentialLongFrames(t *testing.T) {
	libopustest.RequireOracle(t)
	if _, err := libopustest.EncodeDiffHelperPath(); err != nil {
		libopustest.HelperUnavailable(t, "encode diff oracle", err)
	}

	const sampleRate = 48000
	const framesPerSpec = 6
	type longMode struct {
		name      string
		forceMode int
		gmode     EncoderMode
		bwCode    int
		gbw       Bandwidth
		frames    []ExpertFrameDuration
		bitrates  []int
		signal    uint32
		gsignal   Signal
		sigClass  string
	}
	silkFrames := []ExpertFrameDuration{ExpertFrameDuration80Ms, ExpertFrameDuration100Ms, ExpertFrameDuration120Ms}
	multiFrames := []ExpertFrameDuration{ExpertFrameDuration40Ms, ExpertFrameDuration60Ms}
	modes := []longMode{
		{"silk_nb", libopustest.EncodeDiffForceModeSILKOnly, EncoderModeSILK, libopustest.EncodeDiffBandwidthNarrowband, BandwidthNarrowband, silkFrames, []int{8000, 16000}, libopustest.EncodeDiffSignalVoice, SignalVoice, testsignal.CorpusCleanSpeechV1},
		{"silk_wb", libopustest.EncodeDiffForceModeSILKOnly, EncoderModeSILK, libopustest.EncodeDiffBandwidthWideband, BandwidthWideband, silkFrames, []int{16000, 40000}, libopustest.EncodeDiffSignalVoice, SignalVoice, testsignal.CorpusSpeechInNoiseV1},
		{"hybrid_fb", libopustest.EncodeDiffForceModeHybrid, EncoderModeHybrid, libopustest.EncodeDiffBandwidthFullband, BandwidthFullband, multiFrames, []int{32000, 64000}, libopustest.EncodeDiffSignalVoice, SignalVoice, testsignal.CorpusMixedV1},
		{"celt_fb", libopustest.EncodeDiffForceModeCELTOnly, EncoderModeCELT, libopustest.EncodeDiffBandwidthFullband, BandwidthFullband, multiFrames, []int{64000, 128000}, libopustest.EncodeDiffSignalMusic, SignalMusic, testsignal.CorpusMusicV1},
		{"auto", libopustest.EncodeDiffForceModeAuto, EncoderModeAuto, libopustest.EncodeDiffBandwidthAuto, BandwidthFullband, append(multiFrames, silkFrames...), []int{16000, 64000}, libopustest.EncodeDiffSignalVoice, SignalVoice, testsignal.CorpusMixedV1},
	}
	for _, m := range modes {
		for _, channels := range []int{1, 2} {
			for _, fr := range m.frames {
				for _, br := range m.bitrates {
					for _, mode := range []BitrateMode{BitrateModeVBR, BitrateModeCVBR, BitrateModeCBR} {
						spec := encDiffSpec{
							name:      fmt.Sprintf("%s_ch%d_%dms_%dbps_vbr%d", m.name, channels, encMsOf(fr), br, mode),
							forceMode: m.forceMode,
							gmode:     m.gmode,
							bwCode:    m.bwCode,
							gbw:       m.gbw,
							autoBW:    m.forceMode == libopustest.EncodeDiffForceModeAuto,
							frameMs:   fr,
							bitrate:   br,
							channels:  channels,
							vbr:       mode,
							signal:    m.signal,
							gsignal:   m.gsignal,
							sigClass:  m.sigClass,
						}
						t.Run(spec.name, func(t *testing.T) {
							fs := encFrameSamples48k(spec.frameMs)
							pcm, err := testsignal.GenerateCorpusSignal(spec.sigClass, sampleRate, fs*framesPerSpec*spec.channels, spec.channels)
							if err != nil {
								t.Fatalf("GenerateCorpusSignal(%s): %v", spec.sigClass, err)
							}
							vbr, constraint := vbrFlags(spec.vbr)
							want, err := libopustest.ProbeEncodeDiff(libopustest.EncodeDiffParams{
								SampleRate:    sampleRate,
								Channels:      spec.channels,
								Application:   libopustest.EncodeDiffApplicationAudio,
								ForceMode:     spec.forceMode,
								Bandwidth:     spec.bwCode,
								MaxBandwidth:  spec.bwCode,
								Bitrate:       spec.bitrate,
								Complexity:    10,
								Signal:        spec.signal,
								VBR:           vbr,
								VBRConstraint: constraint,
								ForceChannels: spec.channels,
								FrameSize:     fs,
								FrameCount:    framesPerSpec,
								PCM:           pcm,
							})
							if err != nil {
								libopustest.HelperUnavailable(t, "encode diff oracle", err)
								return
							}
							enc, ok := configureEncDiff(t, spec)
							if !ok {
								t.Fatalf("gopus rejected config %s", spec.name)
							}
							for f := range framesPerSpec {
								pkt, err := encDiffEncodeOneFrame(enc, pcm[f*fs*spec.channels:(f+1)*fs*spec.channels])
								if err != nil {
									t.Fatalf("frame %d: gopus encode: %v", f, err)
								}
								o := want[f]
								if !bytes.Equal(pkt, o.Packet) || enc.FinalRange() != o.FinalRange {
									t.Fatalf("frame %d: gopus len=%d toc=%02x rng=%08x, libopus len=%d toc=%02x rng=%08x",
										f, len(pkt), byte0(pkt), enc.FinalRange(), len(o.Packet), byte0(o.Packet), o.FinalRange)
								}
							}
						})
					}
				}
			}
		}
	}
}
