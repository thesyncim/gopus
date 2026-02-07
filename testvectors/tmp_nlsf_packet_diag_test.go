//go:build cgo_libopus

package testvectors

import (
	"testing"

	"github.com/thesyncim/gopus/encoder"
	"github.com/thesyncim/gopus/rangecoding"
	"github.com/thesyncim/gopus/silk"
	"github.com/thesyncim/gopus/types"
)

type tmpNLSFSnap struct {
	ltpRes      []float32
	nbSubfr     int
	subfrLen    int
	lpcOrder    int
	useInterp   bool
	firstFrame  bool
	prevNLSF    []int16
	minInvGain  float32
}

func TestTmpNLSFPacketDiag(t *testing.T) {
	const (
		sampleRate = 48000
		channels   = 1
		frameSize  = 960
		bitrate    = 32000
	)

	numFrames := sampleRate / frameSize
	totalSamples := numFrames * frameSize * channels
	original := generateEncoderTestSignal(totalSamples, channels)

	goEnc := encoder.NewEncoder(sampleRate, channels)
	goEnc.SetMode(encoder.ModeSILK)
	goEnc.SetBandwidth(types.BandwidthWideband)
	goEnc.SetBitrate(bitrate)

	nlsfTrace := &silk.NLSFTrace{CaptureLTPRes: true}
	goEnc.SetSilkTrace(&silk.EncoderTrace{NLSF: nlsfTrace})

	gopusPackets := make([][]byte, 0, numFrames)
	goInterp := make([]int, 0, numFrames)
	goInterpRes := make([][4]float32, 0, numFrames)
	goInterpBase := make([]float32, 0, numFrames)
	goInterpBreak := make([]int, 0, numFrames)
	goNLSFSnaps := make([]tmpNLSFSnap, 0, numFrames)

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
		if nlsfTrace != nil {
			goInterp = append(goInterp, nlsfTrace.InterpIdx)
			goInterpRes = append(goInterpRes, nlsfTrace.InterpResNrgQ2)
			goInterpBase = append(goInterpBase, nlsfTrace.InterpBaseResNrg)
			goInterpBreak = append(goInterpBreak, nlsfTrace.InterpBreakAt)
			ltp := append([]float32(nil), nlsfTrace.LTPRes[:nlsfTrace.LTPResLen]...)
			prev := append([]int16(nil), nlsfTrace.PrevNLSFQ15...)
			goNLSFSnaps = append(goNLSFSnaps, tmpNLSFSnap{
				ltpRes:     ltp,
				nbSubfr:    nlsfTrace.NbSubfr,
				subfrLen:   nlsfTrace.SubfrLen,
				lpcOrder:   nlsfTrace.LPCOrder,
				useInterp:  nlsfTrace.UseInterp,
				firstFrame: nlsfTrace.FirstFrameAfterReset,
				prevNLSF:   prev,
				minInvGain: float32(nlsfTrace.MinInvGain),
			})
		} else {
			goInterp = append(goInterp, -1)
			goInterpRes = append(goInterpRes, [4]float32{})
			goInterpBase = append(goInterpBase, 0)
			goInterpBreak = append(goInterpBreak, -1)
			goNLSFSnaps = append(goNLSFSnaps, tmpNLSFSnap{})
		}
	}

	cgoPackets := encodeWithLibopusFloat(original, sampleRate, channels, bitrate, frameSize, 2052)
	if len(cgoPackets) == 0 {
		t.Skip("CGO libopus encoder not available; build with -tags cgo_libopus")
	}
	libPackets := make([][]byte, len(cgoPackets))
	for i, p := range cgoPackets {
		libPackets[i] = p.data
	}

	goDec := silk.NewDecoder()
	libDec := silk.NewDecoder()

	compareCount := len(gopusPackets)
	if len(libPackets) < compareCount {
		compareCount = len(libPackets)
	}
	if len(goInterp) < compareCount {
		compareCount = len(goInterp)
	}
	for i := 0; i < compareCount; i++ {
		goPayload := gopusPackets[i]
		libPayload := libPackets[i]
		if len(goPayload) < 1 || len(libPayload) < 1 {
			t.Fatalf("frame %d: empty payload", i)
		}
		goPayload = goPayload[1:]
		libPayload = libPayload[1:]
		var rdGo, rdLib rangecoding.Decoder
		rdGo.Init(goPayload)
		rdLib.Init(libPayload)
		if _, err := goDec.DecodeFrame(&rdGo, silk.BandwidthWideband, silk.Frame20ms, true); err != nil {
			t.Fatalf("go decode frame %d: %v", i, err)
		}
		if _, err := libDec.DecodeFrame(&rdLib, silk.BandwidthWideband, silk.Frame20ms, true); err != nil {
			t.Fatalf("lib decode frame %d: %v", i, err)
		}
		goParams := goDec.GetLastFrameParams()
		libParams := libDec.GetLastFrameParams()
		if goInterp[i] != goParams.NLSFInterpCoefQ2 || goParams.NLSFInterpCoefQ2 != libParams.NLSFInterpCoefQ2 {
			t.Logf("frame %d: trace=%d goPacket=%d libPacket=%d", i, goInterp[i], goParams.NLSFInterpCoefQ2, libParams.NLSFInterpCoefQ2)
			e := goInterpRes[i]
			t.Logf("  go interp energies: base=%.9f k3=%.9f k2=%.9f k1=%.9f k0=%.9f break=%d",
				goInterpBase[i], e[3], e[2], e[1], e[0], goInterpBreak[i])
			s := goNLSFSnaps[i]
			if len(s.ltpRes) > 0 {
				libDbg := silk.TmpLibopusFindLPCInterpDebug(s.ltpRes, s.nbSubfr, s.subfrLen, s.lpcOrder, s.useInterp, s.firstFrame, s.prevNLSF, s.minInvGain)
				t.Logf("  lib interp energies: res=%.9f last=%.9f k3=%.9f k2=%.9f k1=%.9f k0=%.9f libInterp=%d",
					libDbg.ResNrg, libDbg.ResNrgLast,
					libDbg.ResNrgInterp[3], libDbg.ResNrgInterp[2], libDbg.ResNrgInterp[1], libDbg.ResNrgInterp[0],
					libDbg.InterpQ2)
			}
		}
	}
}
