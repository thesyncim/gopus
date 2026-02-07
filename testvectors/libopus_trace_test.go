package testvectors

import (
	"bytes"
	"math"
	"testing"

	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/rangecoding"
	"github.com/thesyncim/gopus/silk"
	"github.com/thesyncim/gopus/types"
)

func TestLibopusTraceSILKWB(t *testing.T) {
	const (
		sampleRate = 48000
		channels   = 1
		frameSize  = 960
		bitrate    = 32000
	)

	// Generate 1 second of test signal.
	numFrames := sampleRate / frameSize
	totalSamples := numFrames * frameSize * channels
	original := generateEncoderTestSignal(totalSamples, channels)

	// Encode with gopus.
	goEnc := encoder.NewEncoder(sampleRate, channels)
	goEnc.SetMode(encoder.ModeSILK)
	goEnc.SetBandwidth(types.BandwidthWideband)
	goEnc.SetBitrate(bitrate)

	gopusPackets := make([][]byte, 0, numFrames)
	gopusRanges := make([]uint32, 0, numFrames)
	samplesPerFrame := frameSize * channels
	for i := 0; i < numFrames; i++ {
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
		gopusRanges = append(gopusRanges, goEnc.FinalRange())
	}

	libPackets, fixtureMeta, err := loadSILKWBFloatPacketFixturePackets()
	if err != nil {
		t.Fatalf("load SILK WB libopus float fixture: %v", err)
	}
	if fixtureMeta.Version != 1 ||
		fixtureMeta.SampleRate != sampleRate ||
		fixtureMeta.Channels != channels ||
		fixtureMeta.FrameSize != frameSize ||
		fixtureMeta.Bitrate != bitrate {
		t.Fatalf("invalid SILK WB fixture metadata: %+v", fixtureMeta)
	}
	if fixtureMeta.Frames != len(libPackets) {
		t.Fatalf("invalid SILK WB fixture frame count: header=%d packets=%d", fixtureMeta.Frames, len(libPackets))
	}

	t.Logf("gopus packets: %d, libopus fixture packets: %d", len(gopusPackets), len(libPackets))

	compareCount := len(gopusPackets)
	if len(libPackets) < compareCount {
		compareCount = len(libPackets)
	}
	if compareCount == 0 {
		t.Fatal("no packets to compare")
	}

	// Compare packet sizes and final ranges for the first few frames.
	maxLog := 5
	if compareCount < maxLog {
		maxLog = compareCount
	}
	for i := 0; i < maxLog; i++ {
		t.Logf("frame %02d: gopus=%4d bytes rng=0x%08x | libopus=%4d bytes rng=0x%08x",
			i, len(gopusPackets[i]), gopusRanges[i], len(libPackets[i].data), libPackets[i].finalRange)
	}

	var totalDiff int
	var sizeMismatch int
	var rangeMismatch int
	var payloadMismatch int
	for i := 0; i < compareCount; i++ {
		diff := len(gopusPackets[i]) - len(libPackets[i].data)
		if diff < 0 {
			diff = -diff
		}
		totalDiff += diff
		if len(gopusPackets[i]) != len(libPackets[i].data) {
			sizeMismatch++
		}
		if gopusRanges[i] != libPackets[i].finalRange {
			rangeMismatch++
		}
		if bytes.Equal(gopusPackets[i], libPackets[i].data) {
			continue
		}
		payloadMismatch++
	}
	avgDiff := float64(totalDiff) / float64(compareCount)
	t.Logf("avg packet size diff: %.2f bytes", avgDiff)
	if sizeMismatch != 0 || rangeMismatch != 0 || payloadMismatch != 0 {
		t.Fatalf("SILK WB packet parity mismatch vs fixture: size=%d range=%d payload=%d", sizeMismatch, rangeMismatch, payloadMismatch)
	}
}

func TestDecoderParityLibopusPacketsSILKWB(t *testing.T) {
	const (
		sampleRate = 48000
		channels   = 1
		frameSize  = 960
		bitrate    = 32000
	)

	libPackets, packetMeta, err := loadSILKWBFloatPacketFixturePackets()
	if err != nil {
		t.Fatalf("load SILK WB packet fixture: %v", err)
	}
	if packetMeta.Version != 1 ||
		packetMeta.SampleRate != sampleRate ||
		packetMeta.Channels != channels ||
		packetMeta.FrameSize != frameSize ||
		packetMeta.Bitrate != bitrate {
		t.Fatalf("invalid SILK WB packet fixture metadata: %+v", packetMeta)
	}
	if packetMeta.Frames != len(libPackets) {
		t.Fatalf("invalid SILK WB packet fixture frame count: header=%d packets=%d", packetMeta.Frames, len(libPackets))
	}
	if len(libPackets) == 0 {
		t.Fatal("SILK WB packet fixture contains no packets")
	}

	// Convert to [][]byte for decoder.
	packetBytes := make([][]byte, len(libPackets))
	for i := range libPackets {
		packetBytes[i] = libPackets[i].data
	}

	libDecoded, decodedMeta, err := loadSILKWBFloatDecodedFixtureSamples()
	if err != nil {
		t.Fatalf("load SILK WB decoded fixture: %v", err)
	}
	if decodedMeta.Version != 1 ||
		decodedMeta.SampleRate != sampleRate ||
		decodedMeta.Channels != channels ||
		decodedMeta.FrameSize != frameSize ||
		decodedMeta.Bitrate != bitrate {
		t.Fatalf("invalid SILK WB decoded fixture metadata: %+v", decodedMeta)
	}
	if decodedMeta.Frames != len(libPackets) {
		t.Fatalf("decoded fixture frame count mismatch: packets=%d decodedFrames=%d", len(libPackets), decodedMeta.Frames)
	}
	if len(libDecoded) == 0 {
		t.Fatal("SILK WB decoded fixture contains no samples")
	}

	// Decode the same packets with the internal decoder.
	internalDecoded := decodeWithInternalDecoder(t, packetBytes, channels)
	if len(internalDecoded) == 0 {
		t.Fatal("internal decoder returned no samples")
	}

	compareLen := len(libDecoded)
	if len(internalDecoded) < compareLen {
		compareLen = len(internalDecoded)
	}
	q, delay := ComputeQualityFloat32WithDelay(libDecoded[:compareLen], internalDecoded[:compareLen], sampleRate, 2000)
	t.Logf("decoder parity (libopus packets): Q=%.2f (SNR=%.2f dB), delay=%d samples", q, SNRFromQuality(q), delay)
	if q < 20.0 {
		t.Fatalf("decoder parity regressed: Q=%.2f (SNR=%.2f dB), want Q>=20.0", q, SNRFromQuality(q))
	}
}

func TestSILKParamTraceAgainstLibopus(t *testing.T) {
	const (
		sampleRate = 48000
		channels   = 1
		frameSize  = 960
		bitrate    = 32000
	)

	// Generate 1 second of test signal.
	numFrames := sampleRate / frameSize
	totalSamples := numFrames * frameSize * channels
	original := generateEncoderTestSignal(totalSamples, channels)

	// Encode with gopus.
	goEnc := encoder.NewEncoder(sampleRate, channels)
	goEnc.SetMode(encoder.ModeSILK)
	goEnc.SetBandwidth(types.BandwidthWideband)
	goEnc.SetBitrate(bitrate)
	pitchTrace := &silk.PitchTrace{CaptureXBuf: true}
	gainTrace := &silk.GainLoopTrace{}
	nsqTrace := &silk.NSQTrace{CaptureInputs: true}
	framePreTrace := &silk.FrameStateTrace{}
	frameTrace := &silk.FrameStateTrace{}
	goEnc.SetSilkTrace(&silk.EncoderTrace{Pitch: pitchTrace, GainLoop: gainTrace, NSQ: nsqTrace, FramePre: framePreTrace, Frame: frameTrace})

	gopusPackets := make([][]byte, 0, numFrames)
	pitchTraces := make([]silk.PitchTrace, 0, numFrames)
	gainTraces := make([]silk.GainLoopTrace, 0, numFrames)
	nsqTraces := make([]silk.NSQTrace, 0, numFrames)
	framePreTraces := make([]silk.FrameStateTrace, 0, numFrames)
	frameTraces := make([]silk.FrameStateTrace, 0, numFrames)
	samplesPerFrame := frameSize * channels
	for i := 0; i < numFrames; i++ {
		start := i * samplesPerFrame
		end := start + samplesPerFrame
		pcm := float32ToFloat64(original[start:end])
		packet, err := goEnc.Encode(pcm, frameSize)
		if err != nil {
			t.Fatalf("gopus encode failed at frame %d: %v", i, err)
		}
		if i < 5 {
			t.Logf("Frame %d: SILK VAD activity=%d tiltQ15=%d opusVADProb=%.3f opusActive=%v",
				i, goEnc.LastSilkVADActivity(), goEnc.LastSilkVADInputTiltQ15(), goEnc.LastOpusVADProb(), goEnc.LastOpusVADActive())
			t.Logf("Frame %d: SILK ltpCorr=%.4f", i, goEnc.LastSilkLTPCorr())
		}
		packetCopy := make([]byte, len(packet))
		copy(packetCopy, packet)
		gopusPackets = append(gopusPackets, packetCopy)
		if pitchTrace != nil {
			pitchTraces = append(pitchTraces, *pitchTrace)
		}
		if gainTrace != nil {
			snapshot := *gainTrace
			snapshot.Iterations = append([]silk.GainLoopIter(nil), gainTrace.Iterations...)
			gainTraces = append(gainTraces, snapshot)
		}
		if nsqTrace != nil {
			nsqTraces = append(nsqTraces, cloneNSQTrace(nsqTrace))
		}
		if framePreTrace != nil {
			snapshot := *framePreTrace
			snapshot.PitchBuf = append([]float32(nil), framePreTrace.PitchBuf...)
			framePreTraces = append(framePreTraces, snapshot)
		}
		if frameTrace != nil {
			frameTraces = append(frameTraces, *frameTrace)
		}
	}

	fixturePackets, fixtureMeta, err := loadSILKWBFloatPacketFixturePackets()
	if err != nil {
		t.Fatalf("load SILK WB libopus float fixture: %v", err)
	}
	if fixtureMeta.Version != 1 ||
		fixtureMeta.SampleRate != sampleRate ||
		fixtureMeta.Channels != channels ||
		fixtureMeta.FrameSize != frameSize ||
		fixtureMeta.Bitrate != bitrate {
		t.Fatalf("invalid SILK WB fixture metadata: %+v", fixtureMeta)
	}
	if fixtureMeta.Frames != len(fixturePackets) {
		t.Fatalf("invalid SILK WB fixture frame count: header=%d packets=%d", fixtureMeta.Frames, len(fixturePackets))
	}
	if len(fixturePackets) == 0 {
		t.Fatal("SILK WB fixture contains no packets")
	}

	t.Logf("using SILK WB libopus float fixture: %d packets", len(fixturePackets))

	libPackets := make([][]byte, len(fixturePackets))
	for i, p := range fixturePackets {
		libPackets[i] = p.data
	}

	// Compare decoded SILK parameters using our decoder.
	goDec := silk.NewDecoder()
	libDec := silk.NewDecoder()
	compareCount := len(gopusPackets)
	if len(libPackets) < compareCount {
		compareCount = len(libPackets)
	}
	if compareCount == 0 {
		t.Fatal("no packets to compare")
	}

	var gainDiffSum int
	var gainCount int
	var ltpScaleDiff int
	var interpDiff int
	var perIndexDiff int
	var ltpIndexDiff int
	var ltpIndexCount int
	var signalTypeDiff int
	var lagIndexDiff int
	var contourIndexDiff int
	var seedDiff int
	var gainMismatchLogged int
	firstGainsIDMismatchFrame := -1
	var prePrevLagDiff int
	var prePrevSignalDiff int
	var preNSQLagDiff int
	var preNSQBufDiff int
	var preNSQShpBufDiff int
	var preNSQPrevGainDiff int
	var preNSQSeedDiff int
	var preNSQRewhiteDiff int
	var preNSQXQHashDiff int
	var preNSQSLTPShpHashDiff int
	var preNSQSLPCHashDiff int
	var preNSQSAR2HashDiff int
	var preECPrevLagDiff int
	var preECPrevSignalDiff int
	var preInputRateDiff int
	var preSumLogGainDiff int
	var preTargetRateDiff int
	var preSNRDiff int
	var preNBitsExceededDiff int
	var preNFramesPerPacketDiff int
	var preNFramesEncodedDiff int
	var preLastGainDiff int
	var prePitchBufLenDiff int
	var prePitchBufHashDiff int
	var prePitchWinLenDiff int
	var prePitchWinHashDiff int
	var preModeUseCBRDiff int
	var preModeMaxBitsDiff int
	var preModeBitRateDiff int
	var preNSQTopXQDiff int
	var preNSQTopSLTPShpDiff int
	var preNSQTopSLPCDiff int
	var preNSQTopSAR2Diff int
	var preNSQTopScalarDiff int
	var preNSQInputX16Diff int
	var preNSQInputPredDiff int
	var preNSQInputLTPDiff int
	var preNSQInputARDiff int
	var preNSQInputHarmDiff int
	var preNSQInputTiltDiff int
	var preNSQInputLFDiff int
	var preNSQInputGainsDiff int
	var preNSQInputPitchDiff int
	var preNSQInputScalarDiff int

	// Log first packet size difference
	firstPktSizeDiffFrame := -1
	for i := 0; i < compareCount; i++ {
		if len(gopusPackets[i]) != len(libPackets[i]) {
			firstPktSizeDiffFrame = i
			t.Logf("First packet size diff at frame %d: go=%d lib=%d (diff=%d bytes)",
				i, len(gopusPackets[i]), len(libPackets[i]), len(gopusPackets[i])-len(libPackets[i]))
			break
		}
	}
	if firstPktSizeDiffFrame == -1 {
		t.Log("All packet sizes match between gopus and libopus (float CGO)")
	}
	// Log packet sizes for first 10 frames
	for i := 0; i < compareCount && i < 10; i++ {
		t.Logf("Frame %d: go=%d bytes, lib=%d bytes (diff=%+d)",
			i, len(gopusPackets[i]), len(libPackets[i]), len(gopusPackets[i])-len(libPackets[i]))
	}

	for i := 0; i < compareCount; i++ {
		goPayload := gopusPackets[i]
		libPayload := libPackets[i]
		if len(goPayload) < 2 || len(libPayload) < 2 {
			continue
		}

		if i < len(framePreTraces) {
			if snapPre, ok := captureLibopusOpusSilkStateBeforeFrame(original, sampleRate, channels, bitrate, frameSize, i); ok {
				pre := framePreTraces[i]
				if pre.PrevLag != snapPre.PrevLag {
					prePrevLagDiff++
					if prePrevLagDiff <= 5 {
						t.Logf("Frame %d pre-state PrevLag mismatch: go=%d lib=%d", i, pre.PrevLag, snapPre.PrevLag)
					}
				}
				if pre.PrevSignalType != snapPre.PrevSignalType {
					prePrevSignalDiff++
					if prePrevSignalDiff <= 5 {
						t.Logf("Frame %d pre-state PrevSignal mismatch: go=%d lib=%d", i, pre.PrevSignalType, snapPre.PrevSignalType)
					}
				}
				if pre.NSQLagPrev != snapPre.NSQLagPrev {
					preNSQLagDiff++
					if preNSQLagDiff <= 5 {
						t.Logf("Frame %d pre-state NSQ lagPrev mismatch: go=%d lib=%d", i, pre.NSQLagPrev, snapPre.NSQLagPrev)
					}
				}
				if pre.NSQSLTPBufIdx != snapPre.NSQSLTPBufIdx {
					preNSQBufDiff++
					if preNSQBufDiff <= 5 {
						t.Logf("Frame %d pre-state NSQ sLTPBufIdx mismatch: go=%d lib=%d", i, pre.NSQSLTPBufIdx, snapPre.NSQSLTPBufIdx)
					}
				}
				if pre.NSQSLTPShpBufIdx != snapPre.NSQSLTPShpBufIdx {
					preNSQShpBufDiff++
					if preNSQShpBufDiff <= 5 {
						t.Logf("Frame %d pre-state NSQ sLTPShpBufIdx mismatch: go=%d lib=%d", i, pre.NSQSLTPShpBufIdx, snapPre.NSQSLTPShpBufIdx)
					}
				}
				if pre.NSQPrevGainQ16 != snapPre.NSQPrevGainQ16 {
					preNSQPrevGainDiff++
					if preNSQPrevGainDiff <= 5 {
						t.Logf("Frame %d pre-state NSQ prevGain mismatch: go=%d lib=%d", i, pre.NSQPrevGainQ16, snapPre.NSQPrevGainQ16)
					}
				}
				if pre.NSQRandSeed != snapPre.NSQRandSeed {
					preNSQSeedDiff++
					if preNSQSeedDiff <= 5 {
						t.Logf("Frame %d pre-state NSQ randSeed mismatch: go=%d lib=%d", i, pre.NSQRandSeed, snapPre.NSQRandSeed)
					}
				}
				if pre.NSQRewhiteFlag != snapPre.NSQRewhiteFlag {
					preNSQRewhiteDiff++
					if preNSQRewhiteDiff <= 5 {
						t.Logf("Frame %d pre-state NSQ rewhite mismatch: go=%d lib=%d", i, pre.NSQRewhiteFlag, snapPre.NSQRewhiteFlag)
					}
				}
				if pre.NSQXQHash != snapPre.NSQXQHash {
					preNSQXQHashDiff++
					if preNSQXQHashDiff <= 5 {
						t.Logf("Frame %d pre-state NSQ xq hash mismatch: go=%d lib=%d", i, pre.NSQXQHash, snapPre.NSQXQHash)
					}
				}
				if pre.NSQSLTPShpHash != snapPre.NSQSLTPShpHash {
					preNSQSLTPShpHashDiff++
					if preNSQSLTPShpHashDiff <= 5 {
						t.Logf("Frame %d pre-state NSQ sLTP_shp hash mismatch: go=%d lib=%d", i, pre.NSQSLTPShpHash, snapPre.NSQSLTPShpHash)
					}
				}
				if pre.NSQSLPCHash != snapPre.NSQSLPCHash {
					preNSQSLPCHashDiff++
					if preNSQSLPCHashDiff <= 5 {
						t.Logf("Frame %d pre-state NSQ sLPC hash mismatch: go=%d lib=%d", i, pre.NSQSLPCHash, snapPre.NSQSLPCHash)
					}
				}
				if pre.NSQSAR2Hash != snapPre.NSQSAR2Hash {
					preNSQSAR2HashDiff++
					if preNSQSAR2HashDiff <= 5 {
						t.Logf("Frame %d pre-state NSQ sAR2 hash mismatch: go=%d lib=%d", i, pre.NSQSAR2Hash, snapPre.NSQSAR2Hash)
					}
				}
				if pre.ECPrevLagIndex != snapPre.ECPrevLagIndex {
					preECPrevLagDiff++
					if preECPrevLagDiff <= 5 {
						t.Logf("Frame %d pre-state ec_prevLagIndex mismatch: go=%d lib=%d", i, pre.ECPrevLagIndex, snapPre.ECPrevLagIndex)
					}
				}
				if pre.ECPrevSignalType != snapPre.ECPrevSignalType {
					preECPrevSignalDiff++
					if preECPrevSignalDiff <= 5 {
						t.Logf("Frame %d pre-state ec_prevSignal mismatch: go=%d lib=%d", i, pre.ECPrevSignalType, snapPre.ECPrevSignalType)
					}
				}
				if pre.SumLogGainQ7 != snapPre.SumLogGainQ7 {
					preSumLogGainDiff++
					if preSumLogGainDiff <= 5 {
						t.Logf("Frame %d pre-state sumLogGain mismatch: go=%d lib=%d", i, pre.SumLogGainQ7, snapPre.SumLogGainQ7)
					}
				}
				if pre.InputRateBps != snapPre.SilkModeBitRate {
					preInputRateDiff++
					if preInputRateDiff <= 5 {
						t.Logf("Frame %d pre-state inputRate mismatch: go=%d lib=%d", i, pre.InputRateBps, snapPre.SilkModeBitRate)
					}
				}
				if pre.TargetRateBps != snapPre.TargetRateBps {
					preTargetRateDiff++
					if preTargetRateDiff <= 5 {
						t.Logf("Frame %d pre-state targetRate mismatch: go=%d lib=%d", i, pre.TargetRateBps, snapPre.TargetRateBps)
					}
				}
				if pre.SNRDBQ7 != snapPre.SNRDBQ7 {
					preSNRDiff++
					if preSNRDiff <= 5 {
						t.Logf("Frame %d pre-state SNR mismatch: go=%d lib=%d", i, pre.SNRDBQ7, snapPre.SNRDBQ7)
					}
				}
				if pre.NBitsExceeded != snapPre.NBitsExceeded {
					preNBitsExceededDiff++
					if preNBitsExceededDiff <= 25 {
						t.Logf("Frame %d pre-state nBitsExceeded mismatch: go=%d lib=%d (delta=%d)", i, pre.NBitsExceeded, snapPre.NBitsExceeded, pre.NBitsExceeded-snapPre.NBitsExceeded)
					}
				}
				if pre.NFramesPerPacket != snapPre.NFramesPerPacket {
					preNFramesPerPacketDiff++
					if preNFramesPerPacketDiff <= 5 {
						t.Logf("Frame %d pre-state nFramesPerPacket mismatch: go=%d lib=%d", i, pre.NFramesPerPacket, snapPre.NFramesPerPacket)
					}
				}
				if pre.NFramesEncoded != snapPre.NFramesEncoded {
					preNFramesEncodedDiff++
					if preNFramesEncodedDiff <= 5 {
						t.Logf("Frame %d pre-state nFramesEncoded mismatch: go=%d lib=%d", i, pre.NFramesEncoded, snapPre.NFramesEncoded)
					}
				}
				if int(pre.LastGainIndex) != snapPre.LastGainIndex {
					preLastGainDiff++
					if preLastGainDiff <= 5 {
						t.Logf("Frame %d pre-state lastGain mismatch: go=%d lib=%d", i, pre.LastGainIndex, snapPre.LastGainIndex)
					}
				}
				if pre.PitchBufLen != snapPre.PitchBufLen {
					prePitchBufLenDiff++
					if prePitchBufLenDiff <= 5 {
						t.Logf("Frame %d pre-state pitch buf len mismatch: go=%d lib=%d", i, pre.PitchBufLen, snapPre.PitchBufLen)
					}
				}
				if pre.PitchBufHash != snapPre.PitchXBufHash {
					prePitchBufHashDiff++
					if prePitchBufHashDiff <= 5 {
						t.Logf("Frame %d pre-state pitch x_buf hash mismatch: go=%d lib=%d", i, pre.PitchBufHash, snapPre.PitchXBufHash)
						t.Logf("Frame %d lib pre-state LP transition: mode=%d transitionFrame=%d state=[%d,%d]",
							i, snapPre.LPMode, snapPre.LPTransitionFrame, snapPre.LPState0, snapPre.LPState1)
					}
					if prePitchBufHashDiff <= 3 {
						if libPitchBuf, ok := captureLibopusOpusPitchXBufBeforeFrame(original, sampleRate, channels, bitrate, frameSize, i); ok {
							if idx, goVal, libVal, ok := firstFloat32BitsDiff(pre.PitchBuf, libPitchBuf); ok {
								t.Logf("Frame %d pre-state pitch x_buf first sample diff: idx=%d go=%.9f lib=%.9f lenGo=%d lenLib=%d",
									i, idx, goVal, libVal, len(pre.PitchBuf), len(libPitchBuf))
								diffCount, maxIdx, maxAbs := float32DiffStats(pre.PitchBuf, libPitchBuf)
								t.Logf("Frame %d pre-state pitch x_buf diff stats: mismatches=%d compared=%d maxAbs=%.9f maxIdx=%d",
									i, diffCount, minInt(len(pre.PitchBuf), len(libPitchBuf)), maxAbs, maxIdx)
								if i == 1 && len(pre.PitchBuf) >= 400 && len(libPitchBuf) >= 400 {
									goSeg := pre.PitchBuf[80:400]
									libSeg := libPitchBuf[80:400]
									lag, score := bestLagCorrelation(goSeg, libSeg, 48)
									scale := fitScaleAtLag(goSeg, libSeg, lag)
									t.Logf("Frame %d pre-state pitch x_buf frame-segment lag estimate: bestLag=%d score=%.6f",
										i, lag, score)
									t.Logf("Frame %d pre-state pitch x_buf frame-segment scale at lag: scale=%.6f", i, scale)
								}
							} else {
								t.Logf("Frame %d pre-state pitch x_buf samples match exactly despite hash mismatch; lenGo=%d lenLib=%d",
									i, len(pre.PitchBuf), len(libPitchBuf))
							}
						}
					}
				}
				if pre.PitchWinLen != snapPre.PitchWinLen {
					prePitchWinLenDiff++
					if prePitchWinLenDiff <= 5 {
						t.Logf("Frame %d pre-state pitch window len mismatch: go=%d lib=%d", i, pre.PitchWinLen, snapPre.PitchWinLen)
					}
				}
				if pre.PitchWinHash != snapPre.PitchWinHash {
					prePitchWinHashDiff++
					if prePitchWinHashDiff <= 5 {
						t.Logf("Frame %d pre-state pitch window hash mismatch: go=%d lib=%d", i, pre.PitchWinHash, snapPre.PitchWinHash)
					}
				}
				if i < len(gainTraces) {
					useCBRGo := gainTraces[i].UseCBR
					useCBRLib := snapPre.SilkModeUseCBR != 0
					if useCBRGo != useCBRLib {
						preModeUseCBRDiff++
						if preModeUseCBRDiff <= 5 {
							t.Logf("Frame %d pre-state mode useCBR mismatch: go=%v lib=%v", i, useCBRGo, useCBRLib)
						}
					}
					if gainTraces[i].MaxBits != snapPre.SilkModeMaxBits {
						preModeMaxBitsDiff++
						if preModeMaxBitsDiff <= 5 {
							t.Logf("Frame %d pre-state mode maxBits mismatch: go=%d lib=%d", i, gainTraces[i].MaxBits, snapPre.SilkModeMaxBits)
						}
					}
					if pre.InputRateBps != snapPre.SilkModeBitRate {
						preModeBitRateDiff++
						if preModeBitRateDiff <= 5 {
							t.Logf("Frame %d pre-state mode bitRate mismatch: go=%d lib=%d", i, pre.InputRateBps, snapPre.SilkModeBitRate)
						}
					}
				}
			}
		}

		if i < len(nsqTraces) && preNSQXQHashDiff > 0 && preNSQXQHashDiff <= 3 {
			if snapNSQ, ok := captureLibopusOpusNSQStateBeforeFrame(original, sampleRate, channels, bitrate, frameSize, i); ok {
				tr := nsqTraces[i]
				if len(tr.NSQXQ) == len(snapNSQ.XQ) {
					if idx, goVal, libVal, ok := firstInt16Diff(tr.NSQXQ, snapNSQ.XQ); ok {
						preNSQTopXQDiff++
						if preNSQTopXQDiff <= 3 {
							t.Logf("Frame %d pre-NSQ top-level xq diff idx=%d go=%d lib=%d", i, idx, goVal, libVal)
						}
					}
				}
				if len(tr.NSQSLTPShpQ14) == len(snapNSQ.SLTPShpQ14) {
					if idx, goVal, libVal, ok := firstInt32Diff(tr.NSQSLTPShpQ14, snapNSQ.SLTPShpQ14); ok {
						preNSQTopSLTPShpDiff++
						if preNSQTopSLTPShpDiff <= 3 {
							t.Logf("Frame %d pre-NSQ top-level sLTP_shp diff idx=%d go=%d lib=%d", i, idx, goVal, libVal)
						}
					}
				}
				if len(tr.NSQLPCQ14) == len(snapNSQ.SLPCQ14) {
					if idx, goVal, libVal, ok := firstInt32Diff(tr.NSQLPCQ14, snapNSQ.SLPCQ14); ok {
						preNSQTopSLPCDiff++
						if preNSQTopSLPCDiff <= 3 {
							t.Logf("Frame %d pre-NSQ top-level sLPC diff idx=%d go=%d lib=%d", i, idx, goVal, libVal)
						}
					}
				}
				if len(tr.NSQAR2Q14) == len(snapNSQ.SAR2Q14) {
					if idx, goVal, libVal, ok := firstInt32Diff(tr.NSQAR2Q14, snapNSQ.SAR2Q14); ok {
						preNSQTopSAR2Diff++
						if preNSQTopSAR2Diff <= 3 {
							t.Logf("Frame %d pre-NSQ top-level sAR2 diff idx=%d go=%d lib=%d", i, idx, goVal, libVal)
						}
					}
				}
				if tr.NSQLFARQ14 != snapNSQ.LFARQ14 || tr.NSQDiffQ14 != snapNSQ.DiffQ14 ||
					tr.NSQLagPrev != snapNSQ.LagPrev || tr.NSQSLTPBufIdx != snapNSQ.SLTPBufIdx ||
					tr.NSQSLTPShpBufIdx != snapNSQ.SLTPShpBufIdx || tr.NSQPrevGainQ16 != snapNSQ.PrevGainQ16 ||
					tr.NSQRandSeed != snapNSQ.RandSeed || tr.NSQRewhiteFlag != snapNSQ.RewhiteFlag {
					preNSQTopScalarDiff++
					if preNSQTopScalarDiff <= 3 {
						t.Logf("Frame %d pre-NSQ top-level scalar diff: lfAR go=%d lib=%d diff go=%d lib=%d lagPrev go=%d lib=%d sLTPBufIdx go=%d lib=%d sLTPShpBufIdx go=%d lib=%d prevGain go=%d lib=%d randSeed go=%d lib=%d rewhite go=%d lib=%d",
							i,
							tr.NSQLFARQ14, snapNSQ.LFARQ14,
							tr.NSQDiffQ14, snapNSQ.DiffQ14,
							tr.NSQLagPrev, snapNSQ.LagPrev,
							tr.NSQSLTPBufIdx, snapNSQ.SLTPBufIdx,
							tr.NSQSLTPShpBufIdx, snapNSQ.SLTPShpBufIdx,
							tr.NSQPrevGainQ16, snapNSQ.PrevGainQ16,
							tr.NSQRandSeed, snapNSQ.RandSeed,
							tr.NSQRewhiteFlag, snapNSQ.RewhiteFlag,
						)
					}
				}
			}
		}
		if i > 0 && i-1 < len(nsqTraces) {
			if snapIn, ok := captureLibopusOpusNSQInputsAtFrame(original, sampleRate, channels, bitrate, frameSize, i-1); ok {
				tr := nsqTraces[i-1]
				if tr.FrameLength == snapIn.FrameLength && len(tr.InputQ0) >= snapIn.FrameLength {
					if idx, goVal, libVal, ok := firstInt16Diff(tr.InputQ0[:snapIn.FrameLength], snapIn.X16); ok {
						preNSQInputX16Diff++
						if preNSQInputX16Diff <= 3 {
							t.Logf("Frame %d prev-frame NSQ input x16 diff idx=%d go=%d lib=%d", i, idx, goVal, libVal)
						}
					}
				}
				if len(tr.PredCoefQ12) == len(snapIn.PredCoefQ12) {
					if idx, goVal, libVal, ok := firstInt16Diff(tr.PredCoefQ12, snapIn.PredCoefQ12); ok {
						preNSQInputPredDiff++
						if preNSQInputPredDiff <= 3 {
							t.Logf("Frame %d prev-frame NSQ input pred diff idx=%d go=%d lib=%d", i, idx, goVal, libVal)
						}
					}
				}
				if len(tr.LTPCoefQ14) == len(snapIn.LTPCoefQ14) {
					if idx, goVal, libVal, ok := firstInt16Diff(tr.LTPCoefQ14, snapIn.LTPCoefQ14); ok {
						preNSQInputLTPDiff++
						if preNSQInputLTPDiff <= 3 {
							t.Logf("Frame %d prev-frame NSQ input LTP diff idx=%d go=%d lib=%d", i, idx, goVal, libVal)
						}
					}
				}
				if len(tr.ARShpQ13) == len(snapIn.ARQ13) {
					if idx, goVal, libVal, ok := firstInt16Diff(tr.ARShpQ13, snapIn.ARQ13); ok {
						preNSQInputARDiff++
						if preNSQInputARDiff <= 3 {
							t.Logf("Frame %d prev-frame NSQ input AR diff idx=%d go=%d lib=%d", i, idx, goVal, libVal)
						}
					}
				}
				if len(tr.HarmShapeGainQ14) == len(snapIn.HarmShapeGainQ14) {
					if idx, goVal, libVal, ok := firstIntIntDiff(tr.HarmShapeGainQ14, snapIn.HarmShapeGainQ14); ok {
						preNSQInputHarmDiff++
						if preNSQInputHarmDiff <= 3 {
							t.Logf("Frame %d prev-frame NSQ input harm diff idx=%d go=%d lib=%d", i, idx, goVal, libVal)
						}
					}
				}
				if len(tr.TiltQ14) == len(snapIn.TiltQ14) {
					if idx, goVal, libVal, ok := firstIntIntDiff(tr.TiltQ14, snapIn.TiltQ14); ok {
						preNSQInputTiltDiff++
						if preNSQInputTiltDiff <= 3 {
							t.Logf("Frame %d prev-frame NSQ input tilt diff idx=%d go=%d lib=%d", i, idx, goVal, libVal)
						}
					}
				}
				if len(tr.LFShpQ14) == len(snapIn.LFShpQ14) {
					if idx, goVal, libVal, ok := firstInt32Diff(tr.LFShpQ14, snapIn.LFShpQ14); ok {
						preNSQInputLFDiff++
						if preNSQInputLFDiff <= 3 {
							t.Logf("Frame %d prev-frame NSQ input LF diff idx=%d go=%d lib=%d", i, idx, goVal, libVal)
						}
					}
				}
				if len(tr.GainsQ16) == len(snapIn.GainsQ16) {
					if idx, goVal, libVal, ok := firstInt32Diff(tr.GainsQ16, snapIn.GainsQ16); ok {
						preNSQInputGainsDiff++
						if preNSQInputGainsDiff <= 3 {
							t.Logf("Frame %d prev-frame NSQ input gains diff idx=%d go=%d lib=%d", i, idx, goVal, libVal)
						}
					}
				}
				if len(tr.PitchL) == len(snapIn.PitchL) {
					if idx, goVal, libVal, ok := firstIntIntDiff(tr.PitchL, snapIn.PitchL); ok {
						preNSQInputPitchDiff++
						if preNSQInputPitchDiff <= 3 {
							t.Logf("Frame %d prev-frame NSQ input pitch diff idx=%d go=%d lib=%d", i, idx, goVal, libVal)
						}
					}
				}
				if tr.SignalType != snapIn.SignalType ||
					tr.QuantOffsetType != snapIn.QuantOffsetType ||
					tr.NLSFInterpCoefQ2 != snapIn.NLSFInterpCoefQ2 ||
					tr.SeedIn != snapIn.SeedIn ||
					tr.LambdaQ10 != snapIn.LambdaQ10 ||
					tr.LTPScaleQ14 != snapIn.LTPScaleQ14 ||
					tr.WarpingQ16 != snapIn.WarpingQ16 ||
					tr.NStatesDelayedDecision != snapIn.NStatesDelayedDecision {
					preNSQInputScalarDiff++
					if preNSQInputScalarDiff <= 3 {
						t.Logf("Frame %d prev-frame NSQ input scalar diff: signal go=%d lib=%d qOff go=%d lib=%d interp go=%d lib=%d seedIn go=%d lib=%d lambda go=%d lib=%d ltpScale go=%d lib=%d warping go=%d lib=%d nStates go=%d lib=%d",
							i,
							tr.SignalType, snapIn.SignalType,
							tr.QuantOffsetType, snapIn.QuantOffsetType,
							tr.NLSFInterpCoefQ2, snapIn.NLSFInterpCoefQ2,
							tr.SeedIn, snapIn.SeedIn,
							tr.LambdaQ10, snapIn.LambdaQ10,
							tr.LTPScaleQ14, snapIn.LTPScaleQ14,
							tr.WarpingQ16, snapIn.WarpingQ16,
							tr.NStatesDelayedDecision, snapIn.NStatesDelayedDecision,
						)
					}
				}
			}
		}

		// Skip TOC byte for SILK-only packets.
		goPayload = goPayload[1:]
		libPayload = libPayload[1:]

		var rdGo, rdLib rangecoding.Decoder
		rdGo.Init(goPayload)
		rdLib.Init(libPayload)

		_, err := goDec.DecodeFrame(&rdGo, silk.BandwidthWideband, silk.Frame20ms, true)
		if err != nil {
			t.Fatalf("gopus decode failed at frame %d: %v", i, err)
		}
		_, err = libDec.DecodeFrame(&rdLib, silk.BandwidthWideband, silk.Frame20ms, true)
		if err != nil {
			t.Fatalf("libopus decode failed at frame %d: %v", i, err)
		}

		goParams := goDec.GetLastFrameParams()
		libParams := libDec.GetLastFrameParams()
		if i < 2 && i < len(frameTraces) {
			if snap, ok := captureLibopusOpusSilkState(original, sampleRate, channels, bitrate, frameSize, i); ok {
				ft := frameTraces[i]
				t.Logf("Frame %d gain state: go gains=%v lastGain=%d | lib gains=%v lastGain=%d",
					i, ft.GainIndices, ft.LastGainIndex, snap.GainIndices, snap.LastGainIndex)
				t.Logf("Frame %d ctl state: go speechQ8=%d tiltQ15=%d thresQ16=%d nStates=%d warping=%d sumLogGainQ7=%d | lib speechQ8=%d tiltQ15=%d thresQ16=%d nStates=%d warping=%d sumLogGainQ7=%d",
					i,
					ft.SpeechActivityQ8, ft.InputTiltQ15, ft.PitchEstThresholdQ16, ft.NStatesDelayedDecision, ft.WarpingQ16, ft.SumLogGainQ7,
					snap.SpeechActivityQ8, snap.InputTiltQ15, snap.PitchEstThresQ16, snap.NStatesDelayedDec, snap.WarpingQ16, snap.SumLogGainQ7)
				t.Logf("Frame %d nsq hashes: go xq=%d sLTP_shp=%d sLPC=%d sAR2=%d | lib xq=%d sLTP_shp=%d sLPC=%d sAR2=%d",
					i, ft.NSQXQHash, ft.NSQSLTPShpHash, ft.NSQSLPCHash, ft.NSQSAR2Hash,
					snap.NSQXQHash, snap.NSQSLTPShpHash, snap.NSQSLPCHash, snap.NSQSAR2Hash)
				t.Logf("Frame %d frame cfg: go bitRate=%d nFramesPerPacket=%d nFramesEncoded=%d | lib bitRate=%d nFramesPerPacket=%d nFramesEncoded=%d",
					i, ft.InputRateBps, ft.NFramesPerPacket, ft.NFramesEncoded, snap.SilkModeBitRate, snap.NFramesPerPacket, snap.NFramesEncoded)
			}
			if i < len(nsqTraces) {
				tr := nsqTraces[i]
				if tr.FrameLength > 0 && len(tr.InputQ0) >= tr.FrameLength {
					if msg := compareNSQTraceWithLibopus(tr); msg != "" {
						t.Logf("Frame %d nsq compare: %s", i, msg)
					}
				}
				// Early-frame NSQ input comparison (unconditional)
				// Use the float path (opus_encode_float) to match gopus encoder path.
				// The int16 path (opus_encode24) uses a different signal pipeline.
				if libNSQ, ok := captureLibopusOpusNSQInputsAtFrame(original, sampleRate, channels, bitrate, frameSize, i); ok {
					x16Diff := 0
					x16MaxDiff := int16(0)
					x16MinLen := len(tr.InputQ0)
					if len(libNSQ.X16) < x16MinLen {
						x16MinLen = len(libNSQ.X16)
					}
					for si := 0; si < x16MinLen; si++ {
						d := tr.InputQ0[si] - libNSQ.X16[si]
						if d < 0 {
							d = -d
						}
						if d > 0 {
							x16Diff++
							if d > x16MaxDiff {
								x16MaxDiff = d
							}
						}
					}
					arDiff := 0
					arMaxDiff := int16(0)
					arMinLen := len(tr.ARShpQ13)
					if len(libNSQ.ARQ13) < arMinLen {
						arMinLen = len(libNSQ.ARQ13)
					}
					for si := 0; si < arMinLen; si++ {
						d := tr.ARShpQ13[si] - libNSQ.ARQ13[si]
						if d < 0 {
							d = -d
						}
						if d > 0 {
							arDiff++
							if d > arMaxDiff {
								arMaxDiff = d
							}
						}
					}
					gainsDiff := 0
					gainsMinLen := len(tr.GainsQ16)
					if len(libNSQ.GainsQ16) < gainsMinLen {
						gainsMinLen = len(libNSQ.GainsQ16)
					}
					for si := 0; si < gainsMinLen; si++ {
						if tr.GainsQ16[si] != libNSQ.GainsQ16[si] {
							gainsDiff++
						}
					}
					t.Logf("Frame %d EARLY NSQ inputs: x16 diffs=%d/%d(maxDiff=%d) ARQ13 diffs=%d/%d(maxDiff=%d) gains diffs=%d/%d",
						i, x16Diff, x16MinLen, x16MaxDiff, arDiff, arMinLen, arMaxDiff, gainsDiff, gainsMinLen)
					t.Logf("Frame %d EARLY NSQ scalars: go seed=%d lib seed=%d go interp=%d lib interp=%d go signal=%d lib signal=%d go lambda=%d lib lambda=%d",
						i, tr.SeedIn, libNSQ.SeedIn, tr.NLSFInterpCoefQ2, libNSQ.NLSFInterpCoefQ2, tr.SignalType, libNSQ.SignalType, tr.LambdaQ10, libNSQ.LambdaQ10)
					// Print first few AR diffs for diagnosis
					if arDiff > 0 && arDiff <= arMinLen {
						for si := 0; si < arMinLen && si < 10; si++ {
							if tr.ARShpQ13[si] != libNSQ.ARQ13[si] {
								t.Logf("  Frame %d ARQ13[%d]: go=%d lib=%d diff=%d", i, si, tr.ARShpQ13[si], libNSQ.ARQ13[si], tr.ARShpQ13[si]-libNSQ.ARQ13[si])
							}
						}
					}
				}
			}
		}
		if goDec.GetLastSignalType() != libDec.GetLastSignalType() {
			signalTypeDiff++
			if signalTypeDiff <= 5 {
				t.Logf("Frame %d: SignalType mismatch: go=%d lib=%d", i, goDec.GetLastSignalType(), libDec.GetLastSignalType())
			}
		}

		if goParams.LTPScaleIndex != libParams.LTPScaleIndex {
			ltpScaleDiff++
			if ltpScaleDiff <= 5 {
				t.Logf("Frame %d: LTPScale mismatch: go=%d lib=%d", i, goParams.LTPScaleIndex, libParams.LTPScaleIndex)
			}
		}
		if goParams.NLSFInterpCoefQ2 != libParams.NLSFInterpCoefQ2 {
			interpDiff++
			if interpDiff <= 5 {
				t.Logf("Frame %d: NLSF interp mismatch: go=%d lib=%d", i, goParams.NLSFInterpCoefQ2, libParams.NLSFInterpCoefQ2)
			}
		}
		if goParams.PERIndex != libParams.PERIndex {
			perIndexDiff++
			if perIndexDiff <= 5 {
				t.Logf("Frame %d: PER index mismatch: go=%d lib=%d", i, goParams.PERIndex, libParams.PERIndex)
			}
		}
		if goParams.LagIndex != libParams.LagIndex {
			if lagIndexDiff < 5 {
				t.Logf("Frame %d: LagIndex mismatch: go=%d lib=%d", i, goParams.LagIndex, libParams.LagIndex)
			}
			lagIndexDiff++
		}
		if goParams.ContourIndex != libParams.ContourIndex {
			contourIndexDiff++
		}
		if goParams.Seed != libParams.Seed {
			seedDiff++
			if seedDiff <= 3 {
				goSeedIn := -1
				if i < len(gainTraces) {
					goSeedIn = gainTraces[i].SeedIn
				}
				t.Logf("Frame %d: Seed mismatch: go=%d lib=%d (gopus seedIn=%d, fc&3=%d)", i, goParams.Seed, libParams.Seed, goSeedIn, i&3)
				// Capture libopus NSQ inputs using int16 path (matches opus_demo -16 exactly)
				if i < len(nsqTraces) {
					goTr := nsqTraces[i]
					// Convert float32 to int16 same as writeRawPCM16 / opus_demo -16
					samplesInt16 := make([]int16, len(original))
					for si, s := range original {
						v := int32(math.RoundToEven(float64(s * 32768.0)))
						if v > 32767 {
							v = 32767
						} else if v < -32768 {
							v = -32768
						}
						samplesInt16[si] = int16(v)
					}
					if libNSQ, ok := captureLibopusOpusNSQInputsAtFrameInt16(samplesInt16, sampleRate, channels, bitrate, frameSize, i); ok {
						goGainIters := 0
						goGainSeedOut := -1
						if i < len(gainTraces) {
							goGainIters = len(gainTraces[i].Iterations)
							goGainSeedOut = gainTraces[i].SeedOut
						}
						t.Logf("  Frame %d NSQ inputs (int16 path): lib seedIn=%d seedOut=%d callsInFrame=%d lambdaQ10=%d | go seedIn=%d seedOut=%d gainIters=%d gainSeedOut=%d lambdaQ10=%d",
							i, libNSQ.SeedIn, libNSQ.SeedOut, libNSQ.CallsInFrame, libNSQ.LambdaQ10, goTr.SeedIn, goTr.SeedOut, goGainIters, goGainSeedOut, goTr.LambdaQ10)
						t.Logf("  Frame %d NSQ scalars (int16 path): lib nlsfInterp=%d signalType=%d quantOff=%d ltpScale=%d warp=%d nStates=%d | go nlsfInterp=%d signalType=%d quantOff=%d ltpScale=%d warp=%d nStates=%d",
							i, libNSQ.NLSFInterpCoefQ2, libNSQ.SignalType, libNSQ.QuantOffsetType, libNSQ.LTPScaleQ14, libNSQ.WarpingQ16, libNSQ.NStatesDelayedDecision,
							goTr.NLSFInterpCoefQ2, goTr.SignalType, goTr.QuantOffsetType, goTr.LTPScaleQ14, goTr.WarpingQ16, goTr.NStatesDelayedDecision)
						// Compare x16 arrays (gopus InputQ0 vs libopus X16)
						x16Diff := 0
						x16MaxDiff := int16(0)
						x16MinLen := len(goTr.InputQ0)
						if len(libNSQ.X16) < x16MinLen {
							x16MinLen = len(libNSQ.X16)
						}
						for si := 0; si < x16MinLen; si++ {
							d := goTr.InputQ0[si] - libNSQ.X16[si]
							if d < 0 {
								d = -d
							}
							if d > 0 {
								x16Diff++
								if d > x16MaxDiff {
									x16MaxDiff = d
								}
							}
						}
						t.Logf("  Frame %d x16 diffs: %d/%d (maxDiff=%d)", i, x16Diff, x16MinLen, x16MaxDiff)
						// Compare AR shaping coefficients (gopus ARShpQ13 vs libopus ARQ13)
						arDiff := 0
						arMinLen := len(goTr.ARShpQ13)
						if len(libNSQ.ARQ13) < arMinLen {
							arMinLen = len(libNSQ.ARQ13)
						}
						for si := 0; si < arMinLen; si++ {
							if goTr.ARShpQ13[si] != libNSQ.ARQ13[si] {
								arDiff++
							}
						}
						t.Logf("  Frame %d ARQ13 diffs: %d/%d", i, arDiff, arMinLen)
						// Compare PredCoefQ12
						predDiff := 0
						predMinLen := len(goTr.PredCoefQ12)
						if len(libNSQ.PredCoefQ12) < predMinLen {
							predMinLen = len(libNSQ.PredCoefQ12)
						}
						for si := 0; si < predMinLen; si++ {
							if goTr.PredCoefQ12[si] != libNSQ.PredCoefQ12[si] {
								predDiff++
							}
						}
						t.Logf("  Frame %d PredCoefQ12 diffs: %d/%d", i, predDiff, predMinLen)
						// Compare LTP coefficients
						ltpDiff := 0
						ltpMinLen := len(goTr.LTPCoefQ14)
						if len(libNSQ.LTPCoefQ14) < ltpMinLen {
							ltpMinLen = len(libNSQ.LTPCoefQ14)
						}
						for si := 0; si < ltpMinLen; si++ {
							if goTr.LTPCoefQ14[si] != libNSQ.LTPCoefQ14[si] {
								ltpDiff++
							}
						}
						t.Logf("  Frame %d LTPCoefQ14 diffs: %d/%d", i, ltpDiff, ltpMinLen)
						// Compare per-subframe arrays
						nbSf := goTr.NbSubfr
						if libNSQ.NumSubframes < nbSf {
							nbSf = libNSQ.NumSubframes
						}
						for sf := 0; sf < nbSf; sf++ {
							gH, lH := 0, 0
							gT, lT := 0, 0
							gL, lL := int32(0), int32(0)
							gG, lG := int32(0), int32(0)
							gP, lP := 0, 0
							if sf < len(goTr.HarmShapeGainQ14) {
								gH = goTr.HarmShapeGainQ14[sf]
							}
							if sf < len(libNSQ.HarmShapeGainQ14) {
								lH = libNSQ.HarmShapeGainQ14[sf]
							}
							if sf < len(goTr.TiltQ14) {
								gT = goTr.TiltQ14[sf]
							}
							if sf < len(libNSQ.TiltQ14) {
								lT = libNSQ.TiltQ14[sf]
							}
							if sf < len(goTr.LFShpQ14) {
								gL = goTr.LFShpQ14[sf]
							}
							if sf < len(libNSQ.LFShpQ14) {
								lL = libNSQ.LFShpQ14[sf]
							}
							if sf < len(goTr.GainsQ16) {
								gG = goTr.GainsQ16[sf]
							}
							if sf < len(libNSQ.GainsQ16) {
								lG = libNSQ.GainsQ16[sf]
							}
							if sf < len(goTr.PitchL) {
								gP = goTr.PitchL[sf]
							}
							if sf < len(libNSQ.PitchL) {
								lP = libNSQ.PitchL[sf]
							}
							if gH != lH || gT != lT || gL != lL || gG != lG || gP != lP {
								t.Logf("  Frame %d sf %d: harm go=%d lib=%d tilt go=%d lib=%d lf go=%d lib=%d gain go=%d lib=%d pitch go=%d lib=%d",
									i, sf, gH, lH, gT, lT, gL, lL, gG, lG, gP, lP)
							}
						}
						// (detailed array comparisons already printed above via int16 path)
					}
				}
			}
		}
		n := len(goParams.GainIndices)
		if len(libParams.GainIndices) < n {
			n = len(libParams.GainIndices)
		}
		for j := 0; j < n; j++ {
			diff := goParams.GainIndices[j] - libParams.GainIndices[j]
			if diff < 0 {
				diff = -diff
			}
			gainDiffSum += diff
			gainCount++
		}
		nLtp := len(goParams.LTPIndices)
		if len(libParams.LTPIndices) < nLtp {
			nLtp = len(libParams.LTPIndices)
		}
		for j := 0; j < nLtp; j++ {
			if goParams.LTPIndices[j] != libParams.LTPIndices[j] {
				ltpIndexDiff++
			}
			ltpIndexCount++
		}

		nbSubfr := len(goParams.GainIndices)
		goGainsID := gainsIDFromIndices(goParams.GainIndices, nbSubfr)
		libGainsID := gainsIDFromIndices(libParams.GainIndices, nbSubfr)
		if goGainsID != libGainsID && firstGainsIDMismatchFrame < 0 {
			firstGainsIDMismatchFrame = i
		}

		if gainMismatchLogged < 5 {
			if goGainsID != libGainsID {
				t.Logf("Frame %d: GainsID mismatch: go=%d lib=%d", i, goGainsID, libGainsID)
				t.Logf("  Decoded gains: go=%v lib=%v", goParams.GainIndices, libParams.GainIndices)
				if i < len(gainTraces) {
					trace := gainTraces[i]
					t.Logf("  Gain loop: seedIn=%d seedOut=%d delayed=%v warpingQ16=%d nStates=%d maxBits=%d useCBR=%v",
						trace.SeedIn, trace.SeedOut, trace.UsedDelayedDecision, trace.WarpingQ16, trace.NStatesDelayedDecision, trace.MaxBits, trace.UseCBR)
					if trace.NumSubframes > 0 {
						gainsUnq := make([]int32, trace.NumSubframes)
						for gi := 0; gi < trace.NumSubframes && gi < len(trace.GainsUnqQ16); gi++ {
							gainsUnq[gi] = trace.GainsUnqQ16[gi]
						}
						t.Logf("  GainsUnqQ16=%v lastGainPrev=%d conditional=%v", gainsUnq, trace.LastGainIndexPrev, trace.ConditionalCoding)
						if libInd, libPrev, ok := libopusQuantizeGainsVector(gainsUnq, trace.LastGainIndexPrev, trace.ConditionalCoding, trace.NumSubframes); ok {
							t.Logf("  libopus quant on Go gains: indices=%v prevOut=%d", libInd, libPrev)
						}
						gainsIn := make([]float32, trace.NumSubframes)
						resNrgIn := make([]float32, trace.NumSubframes)
						gainsAfter := make([]float32, trace.NumSubframes)
						for gi := 0; gi < trace.NumSubframes; gi++ {
							gainsIn[gi] = trace.GainsBefore[gi]
							resNrgIn[gi] = trace.ResNrgBefore[gi]
							gainsAfter[gi] = trace.GainsAfter[gi]
						}
						t.Logf("  process_gains inputs: signal=%d predGainQ7=%d snrQ7=%d tiltQ15=%d speechQ8=%d subfr=%d qOffBefore=%d",
							trace.SignalType, trace.PredGainQ7, trace.SNRDBQ7, trace.InputTiltQ15, trace.SpeechActivityQ8, trace.SubframeSamples, trace.QuantOffsetBefore)
						t.Logf("  process_gains vectors: gainsIn=%v resNrg=%v gainsAfterGo=%v qOffAfterGo=%d",
							gainsIn, resNrgIn, gainsAfter, trace.QuantOffsetAfter)
						if libSnap, ok := libopusProcessGainsFromTraceInputs(
							gainsIn,
							resNrgIn,
							trace.NumSubframes,
							trace.SubframeSamples,
							trace.SignalType,
							trace.PredGainQ7,
							trace.InputTiltQ15,
							trace.SNRDBQ7,
							trace.SpeechActivityQ8,
							trace.NStatesDelayedDecision,
							trace.LastGainIndexPrev,
							trace.ConditionalCoding,
						); ok {
							t.Logf("  libopus process_gains on Go inputs: indices=%v lastGainOut=%d qOff=%d gainsUnq=%v lambda=%.6f",
								libSnap.GainsIndices, libSnap.LastGainIndexOut, libSnap.QuantOffsetType, libSnap.GainsUnqQ16, libSnap.Lambda)
						}
						if libFull, ok := captureLibopusProcessGainsAtFrame(original, sampleRate, channels, bitrate, frameSize, i); ok {
							libGainsIn := make([]float32, libFull.NumSubframes)
							libResNrgIn := make([]float32, libFull.NumSubframes)
							libGainsAfter := make([]float32, libFull.NumSubframes)
							libGainsInd := make([]int8, libFull.NumSubframes)
							libGainsUnq := make([]int32, libFull.NumSubframes)
							for gi := 0; gi < libFull.NumSubframes; gi++ {
								libGainsIn[gi] = libFull.GainsBefore[gi]
								libResNrgIn[gi] = libFull.ResNrgBefore[gi]
								libGainsAfter[gi] = libFull.GainsAfter[gi]
								libGainsInd[gi] = libFull.GainsIndices[gi]
								libGainsUnq[gi] = libFull.GainsUnqQ16[gi]
							}
							t.Logf("  libopus full process_gains: calls=%d cond=%d signal=%d predGain=%.6f snrQ7=%d tiltQ15=%d speechQ8=%d subfr=%d nStates=%d",
								libFull.CallsInFrame, libFull.CondCoding, libFull.SignalType, libFull.LTPPredCodGain, libFull.SNRDBQ7, libFull.InputTiltQ15, libFull.SpeechActivityQ8, libFull.SubframeLength, libFull.NStatesDelayedDecision)
							t.Logf("  libopus full vectors: gainsIn=%v resNrg=%v gainsAfter=%v qOff=%d->%d",
								libGainsIn, libResNrgIn, libGainsAfter, libFull.QuantOffsetBefore, libFull.QuantOffsetAfter)
							t.Logf("  libopus full quant: indices=%v lastGain=%d->%d gainsUnq=%v lambda=%.6f",
								libGainsInd, libFull.LastGainIndexPrev, libFull.LastGainIndexOut, libGainsUnq, libFull.Lambda)
							if idx, goVal, libVal, ok := firstFloat32BitsDiff(gainsIn, libGainsIn); ok {
								t.Logf("  process_gains gainsIn diff idx=%d go=%.8f lib=%.8f", idx, goVal, libVal)
							}
							if idx, goVal, libVal, ok := firstFloat32BitsDiff(resNrgIn, libResNrgIn); ok {
								t.Logf("  process_gains resNrg diff idx=%d go=%.8f lib=%.8f", idx, goVal, libVal)
							}
							if idx, goVal, libVal, ok := firstFloat32BitsDiff(gainsAfter, libGainsAfter); ok {
								t.Logf("  process_gains gainsAfter diff idx=%d go=%.8f lib=%.8f", idx, goVal, libVal)
							}
							if idx, goVal, libVal, ok := firstIntSliceDiff(goParams.GainIndices, libGainsInd); ok {
								t.Logf("  process_gains gain index diff idx=%d go=%d lib=%d", idx, goVal, libVal)
							}
							if trace.LastGainIndexPrev != int8(libFull.LastGainIndexPrev) {
								t.Logf("  process_gains lastGainPrev diff go=%d lib=%d", trace.LastGainIndexPrev, libFull.LastGainIndexPrev)
							}
							if trace.QuantOffsetBefore != libFull.QuantOffsetBefore || trace.QuantOffsetAfter != libFull.QuantOffsetAfter {
								t.Logf("  process_gains qOff diff go=%d->%d lib=%d->%d",
									trace.QuantOffsetBefore, trace.QuantOffsetAfter, libFull.QuantOffsetBefore, libFull.QuantOffsetAfter)
							}
						}
					}
					for _, iter := range trace.Iterations {
						t.Logf("  iter %d: gainMultQ8=%d gainsID=%d quantOffset=%d bits=%d (beforeIdx=%d afterIdx=%d afterPulses=%d) foundL=%v foundU=%v skippedNSQ=%v seedIn=%d seedAfterNSQ=%d seedOut=%d",
							iter.Iter, iter.GainMultQ8, iter.GainsID, iter.QuantOffset, iter.Bits, iter.BitsBeforeIndices, iter.BitsAfterIndices, iter.BitsAfterPulses, iter.FoundLower, iter.FoundUpper, iter.SkippedNSQ, iter.SeedIn, iter.SeedAfterNSQ, iter.SeedOut)
					}
				}
				if i < len(nsqTraces) {
					tr := nsqTraces[i]
					if tr.FrameLength > 0 && len(tr.InputQ0) >= tr.FrameLength {
						if msg := compareNSQTraceWithLibopus(tr); msg != "" {
							t.Logf("  NSQ compare: %s", msg)
						}
						if snap, ok := captureLibopusNSQState(original, sampleRate, bitrate, frameSize, i); ok {
							if idx, goVal, libVal, ok := firstInt16Diff(tr.NSQXQ, snap.XQ); ok {
								t.Logf("  NSQ state xq diff idx=%d go=%d lib=%d", idx, goVal, libVal)
							}
							if idx, goVal, libVal, ok := firstInt32Diff(tr.NSQSLTPShpQ14, snap.SLTPShpQ14); ok {
								t.Logf("  NSQ state sLTP_shp diff idx=%d go=%d lib=%d", idx, goVal, libVal)
							}
							if idx, goVal, libVal, ok := firstInt32Diff(tr.NSQLPCQ14, snap.SLPCQ14); ok {
								t.Logf("  NSQ state sLPC diff idx=%d go=%d lib=%d", idx, goVal, libVal)
							}
							if idx, goVal, libVal, ok := firstInt32Diff(tr.NSQAR2Q14, snap.SAR2Q14); ok {
								t.Logf("  NSQ state sAR2 diff idx=%d go=%d lib=%d", idx, goVal, libVal)
							}
							if tr.NSQLFARQ14 != snap.LFARQ14 || tr.NSQDiffQ14 != snap.DiffQ14 {
								t.Logf("  NSQ state scalars diff: lfAR go=%d lib=%d diff go=%d lib=%d",
									tr.NSQLFARQ14, snap.LFARQ14, tr.NSQDiffQ14, snap.DiffQ14)
							}
							if tr.NSQLagPrev != snap.LagPrev || tr.NSQSLTPBufIdx != snap.SLTPBufIdx || tr.NSQSLTPShpBufIdx != snap.SLTPShpBufIdx {
								t.Logf("  NSQ state idx diff: lagPrev go=%d lib=%d sLTPBufIdx go=%d lib=%d sLTPShpBufIdx go=%d lib=%d",
									tr.NSQLagPrev, snap.LagPrev, tr.NSQSLTPBufIdx, snap.SLTPBufIdx, tr.NSQSLTPShpBufIdx, snap.SLTPShpBufIdx)
							}
							if tr.NSQRandSeed != snap.RandSeed || tr.NSQPrevGainQ16 != snap.PrevGainQ16 || tr.NSQRewhiteFlag != snap.RewhiteFlag {
								t.Logf("  NSQ state flags diff: randSeed go=%d lib=%d prevGain go=%d lib=%d rewhite go=%d lib=%d",
									tr.NSQRandSeed, snap.RandSeed, tr.NSQPrevGainQ16, snap.PrevGainQ16, tr.NSQRewhiteFlag, snap.RewhiteFlag)
							}
						}
					}
				}
				if i < len(frameTraces) {
					ft := frameTraces[i]
					if snap, ok := captureLibopusOpusSilkState(original, sampleRate, channels, bitrate, frameSize, i); ok {
						if ft.SignalType != snap.SignalType || ft.LagIndex != snap.LagIndex || ft.Contour != snap.ContourIndex ||
							ft.PrevLag != snap.PrevLag || ft.PrevSignalType != snap.PrevSignalType {
							t.Logf("  Opus state frame diff: sig go=%d lib=%d lagIdx go=%d lib=%d contour go=%d lib=%d prevLag go=%d lib=%d prevSig go=%d lib=%d",
								ft.SignalType, snap.SignalType, ft.LagIndex, snap.LagIndex, ft.Contour, snap.ContourIndex, ft.PrevLag, snap.PrevLag, ft.PrevSignalType, snap.PrevSignalType)
						}
						if math.Abs(float64(ft.LTPCorr-snap.LTPCorr)) > 1e-6 || ft.FirstFrameAfterReset != (snap.FirstFrameAfterReset != 0) {
							t.Logf("  Opus pitch state diff: ltpCorr go=%.6f lib=%.6f firstAfterReset go=%v lib=%v",
								ft.LTPCorr, snap.LTPCorr, ft.FirstFrameAfterReset, snap.FirstFrameAfterReset != 0)
						}
						if ft.NSQLagPrev != snap.NSQLagPrev || ft.NSQSLTPBufIdx != snap.NSQSLTPBufIdx || ft.NSQSLTPShpBufIdx != snap.NSQSLTPShpBufIdx {
							t.Logf("  Opus NSQ idx diff: lagPrev go=%d lib=%d sLTPBufIdx go=%d lib=%d sLTPShpBufIdx go=%d lib=%d",
								ft.NSQLagPrev, snap.NSQLagPrev, ft.NSQSLTPBufIdx, snap.NSQSLTPBufIdx, ft.NSQSLTPShpBufIdx, snap.NSQSLTPShpBufIdx)
						}
						if ft.NSQPrevGainQ16 != snap.NSQPrevGainQ16 || ft.NSQRandSeed != snap.NSQRandSeed || ft.NSQRewhiteFlag != snap.NSQRewhiteFlag {
							t.Logf("  Opus NSQ scalar diff: prevGain go=%d lib=%d randSeed go=%d lib=%d rewhite go=%d lib=%d",
								ft.NSQPrevGainQ16, snap.NSQPrevGainQ16, ft.NSQRandSeed, snap.NSQRandSeed, ft.NSQRewhiteFlag, snap.NSQRewhiteFlag)
						}
						if ft.NSQXQHash != snap.NSQXQHash || ft.NSQSLTPShpHash != snap.NSQSLTPShpHash || ft.NSQSLPCHash != snap.NSQSLPCHash || ft.NSQSAR2Hash != snap.NSQSAR2Hash {
							t.Logf("  Opus NSQ hash diff: xq go=%d lib=%d sLTP_shp go=%d lib=%d sLPC go=%d lib=%d sAR2 go=%d lib=%d",
								ft.NSQXQHash, snap.NSQXQHash, ft.NSQSLTPShpHash, snap.NSQSLTPShpHash, ft.NSQSLPCHash, snap.NSQSLPCHash, ft.NSQSAR2Hash, snap.NSQSAR2Hash)
						}
						if ft.ECPrevLagIndex != snap.ECPrevLagIndex || ft.ECPrevSignalType != snap.ECPrevSignalType {
							t.Logf("  Opus entropy state diff: ecPrevLag go=%d lib=%d ecPrevSig go=%d lib=%d",
								ft.ECPrevLagIndex, snap.ECPrevLagIndex, ft.ECPrevSignalType, snap.ECPrevSignalType)
						}
						if ft.GainIndices != snap.GainIndices || int(ft.LastGainIndex) != snap.LastGainIndex {
							t.Logf("  Opus gain state diff: gains go=%v lib=%v lastGain go=%d lib=%d",
								ft.GainIndices, snap.GainIndices, ft.LastGainIndex, snap.LastGainIndex)
						}
						if ft.SpeechActivityQ8 != snap.SpeechActivityQ8 || ft.InputTiltQ15 != snap.InputTiltQ15 ||
							ft.PitchEstThresholdQ16 != snap.PitchEstThresQ16 || ft.NStatesDelayedDecision != snap.NStatesDelayedDec ||
							ft.WarpingQ16 != snap.WarpingQ16 || ft.SumLogGainQ7 != snap.SumLogGainQ7 {
							t.Logf("  Opus ctl state diff: speechQ8 go=%d lib=%d tiltQ15 go=%d lib=%d thresQ16 go=%d lib=%d nStates go=%d lib=%d warping go=%d lib=%d sumLogGainQ7 go=%d lib=%d",
								ft.SpeechActivityQ8, snap.SpeechActivityQ8,
								ft.InputTiltQ15, snap.InputTiltQ15,
								ft.PitchEstThresholdQ16, snap.PitchEstThresQ16,
								ft.NStatesDelayedDecision, snap.NStatesDelayedDec,
								ft.WarpingQ16, snap.WarpingQ16,
								ft.SumLogGainQ7, snap.SumLogGainQ7)
						}
						if ft.TargetRateBps != snap.TargetRateBps || ft.SNRDBQ7 != snap.SNRDBQ7 || ft.NBitsExceeded != snap.NBitsExceeded {
							t.Logf("  Opus rate state diff: targetRate go=%d lib=%d snrQ7 go=%d lib=%d nBitsExceeded go=%d lib=%d",
								ft.TargetRateBps, snap.TargetRateBps,
								ft.SNRDBQ7, snap.SNRDBQ7,
								ft.NBitsExceeded, snap.NBitsExceeded)
						}
						if ft.InputRateBps != snap.SilkModeBitRate || ft.NFramesPerPacket != snap.NFramesPerPacket || ft.NFramesEncoded != snap.NFramesEncoded {
							t.Logf("  Opus frame cfg diff: bitRate go=%d lib=%d nFramesPerPacket go=%d lib=%d nFramesEncoded go=%d lib=%d",
								ft.InputRateBps, snap.SilkModeBitRate, ft.NFramesPerPacket, snap.NFramesPerPacket, ft.NFramesEncoded, snap.NFramesEncoded)
						}
						if i < len(gainTraces) {
							gtr := gainTraces[i]
							useCBRLib := snap.SilkModeUseCBR != 0
							if gtr.MaxBits != snap.SilkModeMaxBits || gtr.UseCBR != useCBRLib || ft.InputRateBps != snap.SilkModeBitRate {
								t.Logf("  Opus mode diff: maxBits go=%d lib=%d useCBR go=%v lib=%v bitRate go=%d lib=%d",
									gtr.MaxBits, snap.SilkModeMaxBits, gtr.UseCBR, useCBRLib, ft.InputRateBps, snap.SilkModeBitRate)
							}
						}
					}
				}
				gainMismatchLogged++
			}
		}
	}

	if gainCount > 0 {
		avgGainDiff := float64(gainDiffSum) / float64(gainCount)
		t.Logf("gain index avg abs diff: %.2f (frames=%d)", avgGainDiff, compareCount)
		// Regression guard: gain-path parity should stay tightly aligned.
		if avgGainDiff > 0.20 {
			t.Fatalf("gain index avg abs diff regressed: got %.2f, want <= 0.20", avgGainDiff)
		}
	}
	t.Logf("LTP scale index mismatches: %d/%d", ltpScaleDiff, compareCount)
	t.Logf("NLSF interp coef mismatches: %d/%d", interpDiff, compareCount)
	t.Logf("PER index mismatches: %d/%d", perIndexDiff, compareCount)
	t.Logf("Pitch Lag mismatches: %d/%d", lagIndexDiff, compareCount)
	t.Logf("Pitch Contour mismatches: %d/%d", contourIndexDiff, compareCount)
	if ltpIndexCount > 0 {
		t.Logf("LTP index mismatches: %d/%d", ltpIndexDiff, ltpIndexCount)
	}
	t.Logf("Signal type mismatches: %d/%d", signalTypeDiff, compareCount)
	t.Logf("Seed mismatches: %d/%d", seedDiff, compareCount)
	if firstGainsIDMismatchFrame >= 0 {
		t.Logf("First GainsID mismatch frame: %d", firstGainsIDMismatchFrame)
	}
	t.Logf("Pre-state mismatches: prevLag=%d prevSignal=%d nsqLagPrev=%d nsqSLTPBufIdx=%d nsqSLTPShpBufIdx=%d nsqPrevGain=%d nsqSeed=%d nsqRewhite=%d nsqXQHash=%d nsqSLTPShpHash=%d nsqSLPCHash=%d nsqSAR2Hash=%d ecPrevLag=%d ecPrevSignal=%d inputRate=%d sumLogGain=%d targetRate=%d snr=%d nBitsExceeded=%d nFramesPerPacket=%d nFramesEncoded=%d lastGain=%d modeUseCBR=%d modeMaxBits=%d modeBitRate=%d pitchBufLen=%d pitchBufHash=%d pitchWinLen=%d pitchWinHash=%d",
		prePrevLagDiff, prePrevSignalDiff, preNSQLagDiff, preNSQBufDiff, preNSQShpBufDiff, preNSQPrevGainDiff, preNSQSeedDiff, preNSQRewhiteDiff, preNSQXQHashDiff, preNSQSLTPShpHashDiff, preNSQSLPCHashDiff, preNSQSAR2HashDiff, preECPrevLagDiff, preECPrevSignalDiff, preInputRateDiff, preSumLogGainDiff, preTargetRateDiff, preSNRDiff, preNBitsExceededDiff, preNFramesPerPacketDiff, preNFramesEncodedDiff, preLastGainDiff, preModeUseCBRDiff, preModeMaxBitsDiff, preModeBitRateDiff, prePitchBufLenDiff, prePitchBufHashDiff, prePitchWinLenDiff, prePitchWinHashDiff)
	t.Logf("Pre-NSQ top-level full-state diffs: xq=%d sLTP_shp=%d sLPC=%d sAR2=%d scalar=%d",
		preNSQTopXQDiff, preNSQTopSLTPShpDiff, preNSQTopSLPCDiff, preNSQTopSAR2Diff, preNSQTopScalarDiff)
	t.Logf("Pre-NSQ top-level input diffs (prev frame): x16=%d pred=%d ltp=%d ar=%d harm=%d tilt=%d lf=%d gains=%d pitch=%d scalar=%d",
		preNSQInputX16Diff, preNSQInputPredDiff, preNSQInputLTPDiff, preNSQInputARDiff, preNSQInputHarmDiff, preNSQInputTiltDiff, preNSQInputLFDiff, preNSQInputGainsDiff, preNSQInputPitchDiff, preNSQInputScalarDiff)
	if ltpScaleDiff != 0 {
		t.Fatalf("LTP scale index mismatches regressed: got %d/%d, want 0", ltpScaleDiff, compareCount)
	}
	if perIndexDiff != 0 {
		t.Fatalf("PER index mismatches regressed: got %d/%d, want 0", perIndexDiff, compareCount)
	}
	if lagIndexDiff != 0 {
		t.Fatalf("pitch lag mismatches regressed: got %d/%d, want 0", lagIndexDiff, compareCount)
	}
	if contourIndexDiff != 0 {
		t.Fatalf("pitch contour mismatches regressed: got %d/%d, want 0", contourIndexDiff, compareCount)
	}
	if ltpIndexDiff != 0 {
		t.Fatalf("LTP index mismatches regressed: got %d/%d, want 0", ltpIndexDiff, ltpIndexCount)
	}
	if signalTypeDiff != 0 {
		t.Fatalf("signal type mismatches regressed: got %d/%d, want 0", signalTypeDiff, compareCount)
	}
	if preNSQPrevGainDiff > 2 {
		t.Fatalf("pre-state NSQ prevGain mismatches regressed: got %d/%d, want <= 2", preNSQPrevGainDiff, compareCount)
	}
	if preLastGainDiff > 2 {
		t.Fatalf("pre-state lastGain mismatches regressed: got %d/%d, want <= 2", preLastGainDiff, compareCount)
	}
	// Regression guard: this test used to diverge for nearly every frame before
	// SILK mono handoff alignment was fixed.
	if prePitchBufHashDiff > 2 {
		t.Fatalf("pre-state pitch x_buf hash mismatches regressed: got %d/%d, want <= 2", prePitchBufHashDiff, compareCount)
	}
	if prePitchWinHashDiff > 2 {
		t.Fatalf("pre-state pitch window hash mismatches regressed: got %d/%d, want <= 2", prePitchWinHashDiff, compareCount)
	}
	// Regression guard: with CGO float restricted-silk encoding, gain indices
	// should match exactly (no GainsID mismatch) for this canonical WB signal.
	if firstGainsIDMismatchFrame >= 0 {
		t.Errorf("unexpected GainsID mismatch at frame %d (expect no gain mismatches with float CGO comparison)", firstGainsIDMismatchFrame)
	}
	// Keep tiny tolerance for rare 1-LSB trace noise while enforcing near-exact parity.
	if seedDiff > 2 {
		t.Fatalf("seed mismatches regressed: got %d/%d, want <= 2", seedDiff, compareCount)
	}
	if interpDiff > 1 {
		t.Fatalf("NLSF interp mismatches regressed: got %d/%d, want <= 1", interpDiff, compareCount)
	}
}

func gainsIDFromIndices(indices []int, nbSubfr int) int32 {
	var gainsID int32
	n := nbSubfr
	if n > len(indices) {
		n = len(indices)
	}
	for k := 0; k < n; k++ {
		gainsID = int32(indices[k]) + (gainsID << 8)
	}
	return gainsID
}

func cloneNSQTrace(src *silk.NSQTrace) silk.NSQTrace {
	dst := *src
	dst.InputQ0 = append([]int16(nil), src.InputQ0...)
	dst.PredCoefQ12 = append([]int16(nil), src.PredCoefQ12...)
	dst.LTPCoefQ14 = append([]int16(nil), src.LTPCoefQ14...)
	dst.ARShpQ13 = append([]int16(nil), src.ARShpQ13...)
	dst.HarmShapeGainQ14 = append([]int(nil), src.HarmShapeGainQ14...)
	dst.TiltQ14 = append([]int(nil), src.TiltQ14...)
	dst.LFShpQ14 = append([]int32(nil), src.LFShpQ14...)
	dst.GainsQ16 = append([]int32(nil), src.GainsQ16...)
	dst.PitchL = append([]int(nil), src.PitchL...)
	dst.XScSubfrHash = append([]uint64(nil), src.XScSubfrHash...)
	dst.XScQ10 = append([]int32(nil), src.XScQ10...)
	dst.SLTPQ15 = append([]int32(nil), src.SLTPQ15...)
	dst.SLTPRaw = append([]int16(nil), src.SLTPRaw...)
	dst.DelayedGainQ10 = append([]int32(nil), src.DelayedGainQ10...)
	dst.NSQXQ = append([]int16(nil), src.NSQXQ...)
	dst.NSQSLTPShpQ14 = append([]int32(nil), src.NSQSLTPShpQ14...)
	dst.NSQLPCQ14 = append([]int32(nil), src.NSQLPCQ14...)
	dst.NSQAR2Q14 = append([]int32(nil), src.NSQAR2Q14...)
	dst.NSQPostXQ = append([]int16(nil), src.NSQPostXQ...)
	dst.NSQPostSLTPShpQ14 = append([]int32(nil), src.NSQPostSLTPShpQ14...)
	dst.NSQPostLPCQ14 = append([]int32(nil), src.NSQPostLPCQ14...)
	dst.NSQPostAR2Q14 = append([]int32(nil), src.NSQPostAR2Q14...)
	return dst
}

func hashInt8Slice(vals []int8) uint64 {
	var h uint64 = 1469598103934665603
	const prime uint64 = 1099511628211
	for _, v := range vals {
		h ^= uint64(uint8(v))
		h *= prime
	}
	return h
}

func hashInt16Slice(vals []int16) uint64 {
	var h uint64 = 1469598103934665603
	const prime uint64 = 1099511628211
	for _, v := range vals {
		h ^= uint64(uint16(v))
		h *= prime
	}
	return h
}

func hashInt32Slice(vals []int32) uint64 {
	var h uint64 = 1469598103934665603
	const prime uint64 = 1099511628211
	for _, v := range vals {
		h ^= uint64(uint32(v))
		h *= prime
	}
	return h
}

func hashFloat32SliceForTrace(vals []float32) uint64 {
	var h uint64 = 1469598103934665603
	const prime uint64 = 1099511628211
	for _, v := range vals {
		h ^= uint64(math.Float32bits(v))
		h *= prime
	}
	return h
}

func firstFloat32BitsDiff(goVals, libVals []float32) (int, float32, float32, bool) {
	n := len(goVals)
	if len(libVals) < n {
		n = len(libVals)
	}
	for i := 0; i < n; i++ {
		if math.Float32bits(goVals[i]) != math.Float32bits(libVals[i]) {
			return i, goVals[i], libVals[i], true
		}
	}
	if len(goVals) != len(libVals) {
		if len(goVals) > n {
			return n, goVals[n], 0, true
		}
		return n, 0, libVals[n], true
	}
	return -1, 0, 0, false
}

func float32DiffStats(goVals, libVals []float32) (diffCount int, maxIdx int, maxAbs float32) {
	n := minInt(len(goVals), len(libVals))
	maxIdx = -1
	for i := 0; i < n; i++ {
		if math.Float32bits(goVals[i]) != math.Float32bits(libVals[i]) {
			diffCount++
		}
		diff := goVals[i] - libVals[i]
		if diff < 0 {
			diff = -diff
		}
		if diff > maxAbs {
			maxAbs = diff
			maxIdx = i
		}
	}
	diffCount += absInt(len(goVals) - len(libVals))
	return diffCount, maxIdx, maxAbs
}

func minInt(a, b int) int {
	if a < b {
		return a
	}
	return b
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}

func bestLagCorrelation(a, b []float32, maxLag int) (bestLag int, bestScore float64) {
	if len(a) == 0 || len(b) == 0 {
		return 0, 0
	}
	bestLag = 0
	bestScore = -1
	for lag := -maxLag; lag <= maxLag; lag++ {
		var ab, aa, bb float64
		for i := 0; i < len(a); i++ {
			j := i + lag
			if j < 0 || j >= len(b) {
				continue
			}
			av := float64(a[i])
			bv := float64(b[j])
			ab += av * bv
			aa += av * av
			bb += bv * bv
		}
		if aa == 0 || bb == 0 {
			continue
		}
		score := ab / math.Sqrt(aa*bb)
		if score > bestScore {
			bestScore = score
			bestLag = lag
		}
	}
	if bestScore < 0 {
		return 0, 0
	}
	return bestLag, bestScore
}

func fitScaleAtLag(a, b []float32, lag int) float64 {
	var num float64
	var den float64
	for i := 0; i < len(a); i++ {
		j := i + lag
		if j < 0 || j >= len(b) {
			continue
		}
		av := float64(a[i])
		bv := float64(b[j])
		num += av * bv
		den += av * av
	}
	if den == 0 {
		return 0
	}
	return num / den
}

func firstInt16Diff(goVals, libVals []int16) (int, int16, int16, bool) {
	n := len(goVals)
	if len(libVals) < n {
		n = len(libVals)
	}
	for i := 0; i < n; i++ {
		if goVals[i] != libVals[i] {
			return i, goVals[i], libVals[i], true
		}
	}
	return -1, 0, 0, false
}

func firstInt32Diff(goVals, libVals []int32) (int, int32, int32, bool) {
	n := len(goVals)
	if len(libVals) < n {
		n = len(libVals)
	}
	for i := 0; i < n; i++ {
		if goVals[i] != libVals[i] {
			return i, goVals[i], libVals[i], true
		}
	}
	return -1, 0, 0, false
}

func firstIntSliceDiff(goVals []int, libVals []int8) (int, int, int8, bool) {
	n := len(goVals)
	if len(libVals) < n {
		n = len(libVals)
	}
	for i := 0; i < n; i++ {
		if goVals[i] != int(libVals[i]) {
			return i, goVals[i], libVals[i], true
		}
	}
	return -1, 0, 0, false
}

func firstIntIntDiff(goVals, libVals []int) (int, int, int, bool) {
	n := len(goVals)
	if len(libVals) < n {
		n = len(libVals)
	}
	for i := 0; i < n; i++ {
		if goVals[i] != libVals[i] {
			return i, goVals[i], libVals[i], true
		}
	}
	return -1, 0, 0, false
}
