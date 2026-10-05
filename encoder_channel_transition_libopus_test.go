package gopus

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

const reqSetForceMode = 11002

func TestEncoderForcedChannelTransitionLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	tests := []struct {
		name            string
		mode            EncoderMode
		oracleMode      int32
		packetMode      Mode
		bandwidth       Bandwidth
		oracleBandwidth int32
		frameSize       int
		stereoToMonoLag bool
	}{
		{"silk_20ms", EncoderModeSILK, libopustest.OpusForceModeSILKOnly, ModeSILK, BandwidthWideband, libopustest.OpusBandwidthWideband, 960, true},
		{"hybrid_20ms", EncoderModeHybrid, libopustest.OpusForceModeHybrid, ModeHybrid, BandwidthFullband, libopustest.OpusBandwidthFullband, 960, true},
		{"celt_20ms", EncoderModeCELT, libopustest.OpusForceModeCELTOnly, ModeCELT, BandwidthFullband, libopustest.OpusBandwidthFullband, 960, false},
		{"silk_40ms", EncoderModeSILK, libopustest.OpusForceModeSILKOnly, ModeSILK, BandwidthWideband, libopustest.OpusBandwidthWideband, 1920, true},
		{"hybrid_40ms", EncoderModeHybrid, libopustest.OpusForceModeHybrid, ModeHybrid, BandwidthFullband, libopustest.OpusBandwidthFullband, 1920, true},
		{"celt_40ms", EncoderModeCELT, libopustest.OpusForceModeCELTOnly, ModeCELT, BandwidthFullband, libopustest.OpusBandwidthFullband, 1920, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ops := encoderChannelTransitionOps(tt.oracleMode, tt.oracleBandwidth)
			oracle, err := libopustest.ProbeCTLSequence(libopustest.CTLSequenceParams{
				SampleRate: 48000, Channels: 2, Application: cAppAudio,
				FrameSize: tt.frameSize, CapturePackets: true, Ops: ops,
			})
			if err != nil {
				libopustest.HelperUnavailable(t, "encoder channel transition", err)
				return
			}

			enc := mustNewTestEncoder(t, 48000, 2, ApplicationAudio)
			if tt.frameSize != enc.FrameSize() {
				if err := enc.SetFrameSize(tt.frameSize); err != nil {
					t.Fatalf("SetFrameSize(%d): %v", tt.frameSize, err)
				}
			}
			pcm := generateSineWaveFloat32(48000, 440, tt.frameSize, 2)
			packetBuf := make([]byte, 4000)
			packetIndex := 0
			lbrrSeen := false
			wantStereo := [4]bool{true, tt.stereoToMonoLag, false, true}

			if len(oracle) != len(ops) {
				t.Fatalf("oracle returned %d results for %d operations", len(oracle), len(ops))
			}
			for i, op := range ops {
				got := libopustest.CTLResult{}
				switch op.Op {
				case libopustest.CTLOpSet:
					if op.Request == reqSetForceMode {
						if err := enc.SetMode(tt.mode); err != nil {
							t.Fatalf("SET force mode: %v", err)
						}
						got.Ret = cOpusOK
					} else {
						got.Ret = applyEncoderSet(enc, op.Request, op.Arg)
					}
				case libopustest.CTLOpProcess:
					n, err := enc.Encode(pcm, packetBuf)
					if err != nil {
						t.Fatalf("PROCESS %d: %v", packetIndex, err)
					}
					got.Ret = int32(n)
					got.Packet = append([]byte(nil), packetBuf[:n]...)
				case libopustest.CTLOpGet:
					got.Value, got.HaveValue = applyEncoderGet(enc, op.Request)
					got.Ret = cOpusOK
				default:
					t.Fatalf("unexpected operation %d at index %d", op.Op, i)
				}

				ref := oracle[i]
				if got.Ret != ref.Ret || got.Value != ref.Value || got.HaveValue != ref.HaveValue {
					t.Fatalf("operation %d (%+v): Go ret/value/presence=%d/%08x/%t, libopus=%d/%08x/%t",
						i, op, got.Ret, uint32(got.Value), got.HaveValue, ref.Ret, uint32(ref.Value), ref.HaveValue)
				}
				if op.Op != libopustest.CTLOpProcess {
					continue
				}

				if !bytes.Equal(got.Packet, ref.Packet) {
					first := firstByteDifference(got.Packet, ref.Packet)
					t.Fatalf("packet %d differs: Go length=%d TOC=%s, libopus length=%d TOC=%s, first byte difference=%d",
						packetIndex, len(got.Packet), packetTOCLabel(got.Packet), len(ref.Packet), packetTOCLabel(ref.Packet), first)
				}
				if len(ref.Packet) == 0 {
					t.Fatalf("libopus packet %d is empty", packetIndex)
				}
				toc := ParseTOC(ref.Packet[0])
				if toc.Mode != tt.packetMode || toc.Bandwidth != tt.bandwidth {
					t.Fatalf("packet %d TOC mode/bandwidth=%v/%v, want %v/%v", packetIndex, toc.Mode, toc.Bandwidth, tt.packetMode, tt.bandwidth)
				}
				if toc.Stereo != wantStereo[packetIndex] {
					t.Fatalf("packet %d TOC stereo=%t, want %t", packetIndex, toc.Stereo, wantStereo[packetIndex])
				}
				lbrrSeen = lbrrSeen || PacketHasLBRR(ref.Packet)
				packetIndex++
			}
			if packetIndex != len(wantStereo) {
				t.Fatalf("encoded %d packets, want %d", packetIndex, len(wantStereo))
			}
			if tt.mode != EncoderModeCELT && !lbrrSeen {
				t.Fatal("libopus packets did not carry expected SILK LBRR with FEC enabled")
			}
			if tt.name == "hybrid_20ms" {
				assertWarmChannelTransitionAllocations(t, enc, pcm, packetBuf)
			}
		})
	}
}

func encoderChannelTransitionOps(forceMode, bandwidth int32) []libopustest.CTLOp {
	set := func(request, arg int32) libopustest.CTLOp {
		return libopustest.CTLOp{Op: libopustest.CTLOpSet, Request: request, Arg: arg}
	}
	processAndRange := func(ops *[]libopustest.CTLOp) {
		*ops = append(*ops,
			libopustest.CTLOp{Op: libopustest.CTLOpProcess},
			libopustest.CTLOp{Op: libopustest.CTLOpGet, Request: reqGetFinalRange},
		)
	}
	ops := []libopustest.CTLOp{
		set(reqSetBitrate, 64000),
		set(reqSetComplexity, 9),
		set(reqSetVBR, 1),
		set(reqSetVBRConstraint, 0),
		set(reqSetSignal, cSignalVoice),
		set(reqSetLSBDepth, 24),
		set(reqSetInbandFEC, 1),
		set(reqSetPacketLossPerc, 30),
		set(reqSetForceMode, forceMode),
		set(reqSetBandwidth, bandwidth),
		set(reqSetForceChannels, 2),
	}
	processAndRange(&ops)
	ops = append(ops, set(reqSetForceChannels, 1))
	processAndRange(&ops)
	processAndRange(&ops)
	ops = append(ops, set(reqSetForceChannels, 2))
	processAndRange(&ops)
	return ops
}

func firstByteDifference(a, b []byte) int {
	for i := range min(len(a), len(b)) {
		if a[i] != b[i] {
			return i
		}
	}
	return min(len(a), len(b))
}

func packetTOCLabel(packet []byte) string {
	if len(packet) == 0 {
		return "<empty>"
	}
	return fmt.Sprintf("%02x", packet[0])
}

func assertWarmChannelTransitionAllocations(t *testing.T, enc *Encoder, pcm []float32, packet []byte) {
	t.Helper()
	encodeTransition := func() {
		enc.Reset()
		if err := enc.SetMode(EncoderModeHybrid); err != nil {
			t.Fatalf("SetMode(Hybrid): %v", err)
		}
		if err := enc.SetBandwidth(BandwidthFullband); err != nil {
			t.Fatalf("SetBandwidth(Fullband): %v", err)
		}
		if err := enc.SetBitrate(64000); err != nil {
			t.Fatalf("SetBitrate: %v", err)
		}
		if err := enc.SetComplexity(9); err != nil {
			t.Fatalf("SetComplexity: %v", err)
		}
		enc.SetVBR(true)
		enc.SetVBRConstraint(false)
		if err := enc.SetSignal(SignalVoice); err != nil {
			t.Fatalf("SetSignal: %v", err)
		}
		if err := enc.SetLSBDepth(24); err != nil {
			t.Fatalf("SetLSBDepth: %v", err)
		}
		if err := enc.SetInBandFEC(1); err != nil {
			t.Fatalf("SetInBandFEC: %v", err)
		}
		if err := enc.SetPacketLoss(30); err != nil {
			t.Fatalf("SetPacketLoss: %v", err)
		}
		if err := enc.SetForceChannels(2); err != nil {
			t.Fatalf("SetForceChannels(2): %v", err)
		}
		if _, err := enc.Encode(pcm, packet); err != nil {
			t.Fatalf("encode stereo warm-up: %v", err)
		}
		if err := enc.SetForceChannels(1); err != nil {
			t.Fatalf("SetForceChannels(1): %v", err)
		}
		if _, err := enc.Encode(pcm, packet); err != nil {
			t.Fatalf("encode mono-transition warm-up: %v", err)
		}
		if _, err := enc.Encode(pcm, packet); err != nil {
			t.Fatalf("encode mono warm-up: %v", err)
		}
		if err := enc.SetForceChannels(2); err != nil {
			t.Fatalf("SetForceChannels(2): %v", err)
		}
		if _, err := enc.Encode(pcm, packet); err != nil {
			t.Fatalf("encode stereo-transition warm-up: %v", err)
		}
	}
	encodeTransition()
	if got := testing.AllocsPerRun(100, encodeTransition); got != 0 {
		t.Fatalf("warmed forced-channel transition allocated %.2f times per four-frame sequence", got)
	}
}
