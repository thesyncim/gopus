//go:build gopus_fixedpoint

package silk

import (
	"bytes"
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/rangecoding"
)

// TestPublicSILKEncodeFrameFixedByteExact drives the PUBLIC silk.Encoder API
// (EncodeFrame) under the gopus_fixedpoint build and asserts the SILK frame
// payload it produces is byte-for-byte identical to the libopus FIXED_POINT
// silk_encode_frame_FIX reference, replayed on the exact int16 x_buf / inputBuf
// and pre-encode state the public encoder consumed.
//
// It covers mono NB/MB/WB at 10 and 20 ms in CBR and VBR. This is the
// public-API capstone for the integer SILK encode path: the encoder's own
// buffer management, LP filtering, int16 conversion and cross-frame state
// threading feed the validated payload driver, and the result matches libopus.
func TestPublicSILKEncodeFrameFixedByteExact(t *testing.T) {
	libopustest.RequireOracle(t)

	type kase struct {
		name      string
		bandwidth Bandwidth
		fsKHz     int
		frameMs   int
		cbr       bool
		bitrate   int
		gen       int
	}
	var cases []kase
	bws := []struct {
		bw    Bandwidth
		fsKHz int
	}{
		{BandwidthNarrowband, 8},
		{BandwidthMediumband, 12},
		{BandwidthWideband, 16},
	}
	for _, b := range bws {
		for _, ms := range []int{20, 10} {
			for _, cbr := range []bool{false, true} {
				for _, g := range []int{0, 2} {
					mode := "vbr"
					if cbr {
						mode = "cbr"
					}
					cases = append(cases, kase{
						name:      fmt.Sprintf("%s_%dms_%s_g%d", bwName(b.bw), ms, mode, g),
						bandwidth: b.bw,
						fsKHz:     b.fsKHz,
						frameMs:   ms,
						cbr:       cbr,
						bitrate:   18000,
						gen:       g,
					})
				}
			}
		}
	}

	for _, c := range cases {
		c := c
		t.Run(c.name, func(t *testing.T) {
			frameSamples := c.frameMs * c.fsKHz

			pcm := make([]float32, frameSamples)
			for i := range pcm {
				tt := float64(i) / float64(c.fsKHz*1000)
				var v float64
				switch c.gen {
				case 0:
					v = 0.5*math.Sin(2*math.Pi*150*tt) + 0.2*math.Sin(2*math.Pi*450*tt)
				default:
					v = 0.4 * math.Sin(2*math.Pi*2500*tt) * (0.5 + 0.5*math.Sin(2*math.Pi*40*tt))
				}
				pcm[i] = float32(v * 0.35)
			}

			enc := NewEncoder(c.bandwidth)
			enc.SetComplexity(2)
			enc.SetBitrate(c.bitrate)
			enc.SetVBR(!c.cbr)
			enc.SetVADState(200, 0, [4]int32{-1, -1, -1, -1})
			enc.EnableFixedSnapshotForTest()

			got := enc.EncodeFrame(pcm, nil, true)
			if len(got) == 0 {
				t.Fatalf("empty packet")
			}

			snap := enc.FixedPreEncodeForTest()
			if len(snap.XBuf) == 0 {
				t.Fatalf("no pre-encode snapshot captured")
			}

			oracleCase := buildPayloadCaseFromSnapshot(c.bandwidth, c.fsKHz, snap)
			want, err := probeLibopusSILKFixedEncodeFramePayload([]silkFixedEncodeFramePayloadCase{oracleCase})
			if err != nil {
				libopustest.HelperUnavailable(t, "silk fixed encode frame payload", err)
				return
			}
			w := want[0]
			if w.nBytesOut <= 0 {
				t.Fatalf("oracle produced no bytes")
			}

			// Run the validated payload driver on the public encoder's exact
			// inputs into a fresh range coder (no packet header) and assert the
			// frame payload is byte-for-byte identical to the libopus FIXED_POINT
			// silk_encode_frame_FIX reference.
			gotFrame := encodeFrameOnlyFromSnapshot(c.bandwidth, c.fsKHz, c.cbr, snap)
			if !bytes.Equal(gotFrame[:w.nBytesOut], w.payload) {
				t.Fatalf("frame payload vs libopus mismatch:\n got=%x\nwant=%x", gotFrame[:w.nBytesOut], w.payload)
			}

			// Assemble the public SILK packet (header + frame + patch) from the
			// validated driver replay and confirm it matches the encoder's own
			// output, proving the public encoder's buffer/state threading and
			// header assembly are correct end to end.
			rebuilt := rebuildPublicSILKPacket(c.bandwidth, c.fsKHz, c.cbr, snap)
			if !bytes.Equal(got, rebuilt) {
				t.Fatalf("public packet vs validated-driver rebuild mismatch:\n got=%x\nwant=%x", got, rebuilt)
			}
		})
	}
}

func buildPayloadCaseFromSnapshot(bw Bandwidth, fsKHz int, snap FixedPreEncodeSnapshot) silkFixedEncodeFramePayloadCase {
	subfrLength := subFrameLengthMs * fsKHz
	ltpMemLength := ltpMemLengthMs * fsKHz
	laPitch := laPitchMs * fsKHz
	laShape := laShapeMs * fsKHz
	pitchLPCWinLength := (ltpMemLengthMs + (laPitchMs << 1)) * fsKHz
	// The integer x_buf is always allocated for a 20 ms frame, but the libopus
	// per-frame oracle reads exactly ltp_mem_length + LA_SHAPE + frame_length
	// int16s. For 10 ms frames the snapshot buffer is longer; trim it to the
	// length the oracle consumes so a multi-case batch stays byte-aligned.
	xBufLen := ltpMemLength + laShape + snap.FrameLength
	xBuf := snap.XBuf
	if len(xBuf) > xBufLen {
		xBuf = xBuf[:xBufLen]
	}
	tc := silkFixedEncodeFrameCase{
		fsKHz:                   fsKHz,
		frameLength:             snap.FrameLength,
		subfrLength:             subfrLength,
		nbSubfr:                 snap.NbSubfr,
		ltpMemLength:            ltpMemLength,
		laPitch:                 laPitch,
		laShape:                 laShape,
		pitchLPCWinLength:       pitchLPCWinLength,
		pitchEstimationLPCOrder: snap.PitchEstLPCOrder,
		predictLPCOrder:         snap.PredictLPCOrder,
		shapingLPCOrder:         snap.ShapingLPCOrder,
		shapeWinLength:          snap.ShapeWinLength,
		complexity:              snap.Complexity,
		nStatesDelayedDecision:  snap.NStatesDelDec,
		warpingQ16:              snap.WarpingQ16,
		useCBR:                  snap.UseCBR,
		nlsfMSVQSurvivors:       snap.NlsfSurvivors,
		pitchEstThresQ16:        snap.PitchEstThrQ16,
		snrDBQ7:                 snap.SnrDBQ7,
		packetLossPerc:          0,
		nFramesPerPacket:        1,
		lbrrFlag:                0,
		condCoding:              snap.CondCoding,
		opusVADActivity:         1,
		frameCounter:            snap.FrameCounter,
		prevSignalType:          snap.PrevSignalType,
		prevLag:                 snap.PrevLag,
		firstFrameAfterReset:    boolToI32(snap.FirstFrameAfterReset),
		sumLogGainQ7:            snap.SumLogGainQ7,
		harmShapeGainSmthQ16:    snap.HarmShapeGainSmthQ16,
		tiltSmthQ16:             snap.TiltSmthQ16,
		lastGainIndex:           int32(snap.LastGainIndex),
		ltpCorrQ15:              snap.LtpCorrQ15,
		prevNLSFqQ15:            snap.PrevNLSFqQ15,
		vadInput:                append([]int16(nil), snap.InputBuf...),
		xBuf:                    append([]int16(nil), xBuf...),
	}
	return silkFixedEncodeFramePayloadCase{
		silkFixedEncodeFrameCase: tc,
		maxBits:                  int32(snap.MaxBits),
		bandwidth:                bw,
	}
}

// rebuildPublicSILKPacket replays the validated payload driver on the captured
// snapshot and assembles the SILK packet (VAD/FEC header + frame + 2-bit patch)
// exactly as the public encoder's standalone path does.
func rebuildPublicSILKPacket(bw Bandwidth, fsKHz int, cbr bool, snap FixedPreEncodeSnapshot) []byte {
	ps := newPayloadStateFromSnapshot(bw, fsKHz, cbr, snap)

	re := &rangecoding.Encoder{}
	buf := make([]byte, maxSilkPacketBytes)
	re.Init(buf)
	ps.rangeEncoder = re

	// VAD/FEC header reservation (single mono frame, nFramesPerPacket=1).
	iCDF := []uint16{uint16(256 - (256 >> ((1 + 1) * 1))), 0}
	re.EncodeICDF16(0, iCDF, 8)

	helper := &Encoder{}
	helper.silkEncodeFramePayloadFIX(ps)

	// Patch VAD + LBRR flags (vadFlag=1 in bit 1, lbrrFlag=0 in bit 0).
	re.PatchInitialBits(uint32(1<<1), 2)
	nBytesOut := (re.Tell() + 7) >> 3
	raw := re.Done()
	if nBytesOut > len(raw) {
		nBytesOut = len(raw)
	}
	return raw[:nBytesOut]
}

func newPayloadStateFromSnapshot(bw Bandwidth, fsKHz int, cbr bool, snap FixedPreEncodeSnapshot) *silkEncodeFramePayloadFIXState {
	oc := buildPayloadCaseFromSnapshot(bw, fsKHz, snap)
	tc := oc.silkFixedEncodeFrameCase
	ps := &silkEncodeFramePayloadFIXState{
		silkEncodeFrameFIXState: silkEncodeFrameFIXState{
			fsKHz:                       tc.fsKHz,
			frameLength:                 tc.frameLength,
			subfrLength:                 tc.subfrLength,
			nbSubfr:                     tc.nbSubfr,
			ltpMemLength:                tc.ltpMemLength,
			laPitch:                     tc.laPitch,
			laShape:                     tc.laShape,
			pitchLPCWinLength:           tc.pitchLPCWinLength,
			pitchEstimationLPCOrder:     tc.pitchEstimationLPCOrder,
			predictLPCOrder:             tc.predictLPCOrder,
			shapingLPCOrder:             tc.shapingLPCOrder,
			shapeWinLength:              tc.shapeWinLength,
			complexity:                  tc.complexity,
			nStatesDelayedDecision:      tc.nStatesDelayedDecision,
			warpingQ16:                  tc.warpingQ16,
			useCBR:                      tc.useCBR,
			nlsfMSVQSurvivors:           tc.nlsfMSVQSurvivors,
			pitchEstimationThresholdQ16: tc.pitchEstThresQ16,
			snrDBQ7:                     tc.snrDBQ7,
			packetLossPerc:              tc.packetLossPerc,
			nFramesPerPacket:            tc.nFramesPerPacket,
			lbrrFlag:                    tc.lbrrFlag,
			condCoding:                  tc.condCoding,
			opusVADActivity:             tc.opusVADActivity,
			frameCounter:                tc.frameCounter,
			prevSignalType:              tc.prevSignalType,
			prevLag:                     tc.prevLag,
			firstFrameAfterReset:        tc.firstFrameAfterReset != 0,
			sumLogGainQ7:                tc.sumLogGainQ7,
			harmShapeGainSmthQ16:        tc.harmShapeGainSmthQ16,
			tiltSmthQ16:                 tc.tiltSmthQ16,
			lastGainIndex:               int8(tc.lastGainIndex),
			ltpCorrQ15:                  tc.ltpCorrQ15,
			prevNLSFqQ15:                tc.prevNLSFqQ15,
			vadInput:                    tc.vadInput,
			xBuf:                        tc.xBuf,
		},
		lbrrEnabled: false,
		maxBits:     int(oc.maxBits),
		useCBR:      cbr,
		bandwidth:   bw,
	}
	silkVADInit(&ps.vad)
	ps.nsq.prevGainQ16 = 1 << 16
	ps.nsq.lagPrev = 100
	return ps
}

// encodeFrameOnlyFromSnapshot replays the validated payload driver on the
// captured snapshot into a fresh header-less range coder, returning the
// zero-padded frame buffer (matching the libopus oracle's read of nBytesOut
// bytes from its zero-initialized ec buffer).
func encodeFrameOnlyFromSnapshot(bw Bandwidth, fsKHz int, cbr bool, snap FixedPreEncodeSnapshot) []byte {
	ps := newPayloadStateFromSnapshot(bw, fsKHz, cbr, snap)
	buf := make([]byte, maxSilkPacketBytes)
	re := &rangecoding.Encoder{}
	re.Init(buf)
	ps.rangeEncoder = re
	helper := &Encoder{}
	res := helper.silkEncodeFramePayloadFIX(ps)
	re.Done()
	_ = res
	return buf
}

func bwName(b Bandwidth) string {
	switch b {
	case BandwidthNarrowband:
		return "nb"
	case BandwidthMediumband:
		return "mb"
	case BandwidthWideband:
		return "wb"
	}
	return "?"
}

func boolToI32(b bool) int32 {
	if b {
		return 1
	}
	return 0
}
