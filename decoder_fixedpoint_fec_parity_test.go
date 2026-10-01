//go:build gopus_fixed_point

package gopus

import (
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

// TestDecoderFixedPointFECMatchesLibopus compares the public float FEC API to
// FIXED_POINT opus_decode_float. Both decode a real SILK or Hybrid LBRR packet,
// or a CELT packet whose FEC request enters the PLC path. The full sequence
// includes normal recovery, another loss, and the next received packet.
func TestDecoderFixedPointFECMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	helper, err := getFixedRefdecodeHelperPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "fixed reference FEC decode", err)
	}
	type fecStep struct {
		packet []byte
		fec    bool
	}
	for _, mode := range []EncoderMode{EncoderModeSILK, EncoderModeHybrid, EncoderModeCELT} {
		for _, channels := range []int{1, 2} {
			var seed, recovery []byte
			switch mode {
			case EncoderModeSILK:
				seed, recovery = encodeAPIRateFECSequence(t, mode, ModeSILK, BandwidthWideband, 24000, channels, 960)
			case EncoderModeHybrid:
				seed, recovery = encodeAPIRateFECSequence(t, mode, ModeHybrid, BandwidthFullband, 48000, channels, 960)
			default:
				seed = encodeFixedSingleModePacket(t, channels, 960, mode, 0)
				recovery = seed
			}
			if mode != EncoderModeCELT && !PacketHasLBRR(recovery) {
				t.Fatalf("mode %v ch %d recovery packet has no LBRR", mode, channels)
			}
			steps := []fecStep{
				{packet: seed},
				{packet: recovery, fec: true},
				{packet: recovery},
				{fec: true},
				{packet: recovery},
			}
			for _, rate := range []int{8000, 12000, 16000, 24000, 48000} {
				for _, gain := range []int{0, -768, 768} {
					t.Run(fmt.Sprintf("mode%d_ch%d_rate%d_gain%d", mode, channels, rate, gain), func(t *testing.T) {
						frameSize := rate / 50
						stepPCM := frameSize * channels
						payload := libopustest.NewOraclePayloadVersion("GOSI", 8,
							libopusRefdecodeSingleFormatFloat32, uint32(rate), uint32(int32(gain)),
							uint32(channels), uint32(frameSize), uint32(len(steps)))
						for _, step := range steps {
							flag := uint32(0)
							if step.fec {
								flag = 1
							}
							payload.U32(flag)
							payload.U32(uint32(frameSize))
							payload.U32(uint32(len(step.packet)))
							payload.Raw(step.packet)
						}
						reader, err := libopustest.RunOracleVersion(helper, payload.Bytes(), "fixed reference FEC decode", "GOSO", 3)
						if err != nil {
							t.Fatal(err)
						}
						if got := reader.Count(stepPCM * len(steps)); got != stepPCM*len(steps) {
							t.Fatalf("reference PCM count=%d want=%d", got, stepPCM*len(steps))
						}
						wantPCM := make([]uint32, stepPCM*len(steps))
						for i := range wantPCM {
							wantPCM[i] = reader.U32()
						}
						if got := reader.Count(len(steps)); got != len(steps) {
							t.Fatalf("reference record count=%d want=%d", got, len(steps))
						}
						wantRange := make([]uint32, len(steps))
						for i := range steps {
							status, samples, finalRange, offset := reader.U32(), reader.U32(), reader.U32(), reader.U32()
							if status != 0 || samples != uint32(frameSize) || offset != uint32(i*stepPCM) {
								t.Fatalf("reference step %d record=(%d,%d,%08x,%d)", i, status, samples, finalRange, offset)
							}
							wantRange[i] = finalRange
						}
						if err := reader.ExpectConsumed(); err != nil {
							t.Fatal(err)
						}

						dec, err := NewDecoder(DefaultDecoderConfig(rate, channels))
						if err != nil {
							t.Fatal(err)
						}
						if err := dec.SetGain(gain); err != nil {
							t.Fatal(err)
						}
						frame := make([]float32, stepPCM)
						for pass := range 2 {
							if pass != 0 {
								dec.Reset()
							}
							for i, step := range steps {
								var n int
								if step.fec {
									n, err = dec.DecodeWithFEC(step.packet, frame, true)
								} else {
									n, err = dec.Decode(step.packet, frame)
								}
								if err != nil || n != frameSize {
									t.Fatalf("pass %d step %d decode=(%d,%v), want %d", pass, i, n, err, frameSize)
								}
								if got := dec.FinalRange(); got != wantRange[i] {
									t.Fatalf("pass %d step %d range=%08x want=%08x", pass, i, got, wantRange[i])
								}
								for j, sample := range frame {
									if got := math.Float32bits(sample); got != wantPCM[i*stepPCM+j] {
										t.Fatalf("pass %d step %d sample %d=%08x want=%08x", pass, i, j, got, wantPCM[i*stepPCM+j])
									}
								}
							}
						}
					})
				}
			}
		}
	}
}

func TestDecoderFixedPointFECWarmZeroAllocs(t *testing.T) {
	for _, tc := range []struct {
		name      string
		mode      EncoderMode
		wantMode  Mode
		bandwidth Bandwidth
		bitrate   int
	}{
		{"silk", EncoderModeSILK, ModeSILK, BandwidthWideband, 24000},
		{"hybrid", EncoderModeHybrid, ModeHybrid, BandwidthFullband, 48000},
	} {
		for _, channels := range []int{1, 2} {
			t.Run(fmt.Sprintf("%s_ch%d", tc.name, channels), func(t *testing.T) {
				seed, recovery := encodeAPIRateFECSequence(t, tc.mode, tc.wantMode, tc.bandwidth, tc.bitrate, channels, 960)
				if !PacketHasLBRR(recovery) {
					t.Fatal("warm FEC packet has no LBRR")
				}
				dec, err := NewDecoder(DefaultDecoderConfig(48000, channels))
				if err != nil {
					t.Fatal(err)
				}
				frame := make([]float32, 960*channels)
				if _, err := dec.Decode(seed, frame); err != nil {
					t.Fatal(err)
				}
				for range 3 {
					if _, err := dec.DecodeWithFEC(recovery, frame, true); err != nil {
						t.Fatal(err)
					}
				}
				allocs := testing.AllocsPerRun(20, func() {
					n, err := dec.DecodeWithFEC(recovery, frame, true)
					if err != nil || n != 960 {
						t.Fatalf("warm FEC decode=(%d,%v)", n, err)
					}
				})
				if allocs != 0 {
					t.Fatalf("warm FEC allocations=%g, want zero", allocs)
				}
				hasSignal := false
				for _, sample := range frame {
					if sample != 0 && !math.IsNaN(float64(sample)) {
						hasSignal = true
						break
					}
				}
				if !hasSignal {
					t.Fatal("warm FEC produced no active PCM")
				}
			})
		}
	}
}

// TestDecoderFixedPointFECMultiFrameMatchesLibopus exercises SILK's two- and
// three-frame LBRR cadence, including Hybrid integer CELT highband accumulation
// on the first 20 ms of the recovered frame.
func TestDecoderFixedPointFECMultiFrameMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	helper, err := getFixedRefdecodeHelperPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "fixed reference multi-frame FEC decode", err)
	}
	for _, tc := range []struct {
		name      string
		mode      EncoderMode
		wantMode  Mode
		bandwidth Bandwidth
		bitrate   int
		channels  int
		rate      int
		frameSize int
	}{
		{"silk_40ms_mono", EncoderModeSILK, ModeSILK, BandwidthWideband, 24000, 1, 16000, 1920},
		{"silk_60ms_stereo", EncoderModeSILK, ModeSILK, BandwidthWideband, 24000, 2, 48000, 2880},
		{"hybrid_40ms_mono", EncoderModeHybrid, ModeHybrid, BandwidthFullband, 48000, 1, 48000, 1920},
		{"hybrid_60ms_stereo", EncoderModeHybrid, ModeHybrid, BandwidthFullband, 48000, 2, 48000, 2880},
	} {
		t.Run(tc.name, func(t *testing.T) {
			seed, recovery := encodeAPIRateFECSequence(t, tc.mode, tc.wantMode, tc.bandwidth, tc.bitrate, tc.channels, tc.frameSize)
			if !PacketHasLBRR(recovery) {
				t.Fatal("recovery packet has no LBRR")
			}
			frameSize := tc.frameSize * tc.rate / 48000
			pcmPerStep := frameSize * tc.channels
			packets := [][]byte{seed, recovery, recovery}
			payload := libopustest.NewOraclePayloadVersion("GOSI", 8,
				libopusRefdecodeSingleFormatFloat32, uint32(tc.rate), uint32(768),
				uint32(tc.channels), uint32(frameSize), uint32(len(packets)))
			for i, packet := range packets {
				fec := uint32(0)
				if i == 1 {
					fec = 1
				}
				payload.U32(fec)
				payload.U32(uint32(frameSize))
				payload.U32(uint32(len(packet)))
				payload.Raw(packet)
			}
			reader, err := libopustest.RunOracleVersion(helper, payload.Bytes(), "fixed reference multi-frame FEC decode", "GOSO", 3)
			if err != nil {
				t.Fatal(err)
			}
			if got := reader.Count(len(packets) * pcmPerStep); got != len(packets)*pcmPerStep {
				t.Fatalf("reference PCM count=%d want=%d", got, len(packets)*pcmPerStep)
			}
			wantPCM := make([]uint32, len(packets)*pcmPerStep)
			for i := range wantPCM {
				wantPCM[i] = reader.U32()
			}
			if got := reader.Count(len(packets)); got != len(packets) {
				t.Fatalf("reference record count=%d want=%d", got, len(packets))
			}
			wantRange := make([]uint32, len(packets))
			for i := range packets {
				status, samples, finalRange, offset := reader.U32(), reader.U32(), reader.U32(), reader.U32()
				if status != 0 || samples != uint32(frameSize) || offset != uint32(i*pcmPerStep) {
					t.Fatalf("reference step %d record=(%d,%d,%08x,%d)", i, status, samples, finalRange, offset)
				}
				wantRange[i] = finalRange
			}
			if err := reader.ExpectConsumed(); err != nil {
				t.Fatal(err)
			}
			dec, err := NewDecoder(DefaultDecoderConfig(tc.rate, tc.channels))
			if err != nil {
				t.Fatal(err)
			}
			if err := dec.SetGain(768); err != nil {
				t.Fatal(err)
			}
			pcm := make([]float32, pcmPerStep)
			for i, packet := range packets {
				var n int
				if i == 1 {
					n, err = dec.DecodeWithFEC(packet, pcm, true)
				} else {
					n, err = dec.Decode(packet, pcm)
				}
				if err != nil || n != frameSize {
					t.Fatalf("step %d decode=(%d,%v), want=%d", i, n, err, frameSize)
				}
				if got := dec.FinalRange(); got != wantRange[i] {
					t.Fatalf("step %d range=%08x want=%08x", i, got, wantRange[i])
				}
				for j, sample := range pcm {
					if got := math.Float32bits(sample); got != wantPCM[i*pcmPerStep+j] {
						t.Fatalf("step %d sample %d=%08x want=%08x", i, j, got, wantPCM[i*pcmPerStep+j])
					}
				}
			}
		})
	}
}

// TestDecoderFixedPointFECNoLBRRCELTToSILKMatchesLibopus covers the 5 ms
// integer transition after a long no-LBRR request. The middle call conceals
// 40 or 60 ms, and the following SILK packet crossfades CELT PLC onto SILK.
func TestDecoderFixedPointFECNoLBRRCELTToSILKMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	helper, err := getFixedRefdecodeHelperPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "fixed reference no-LBRR FEC transition", err)
	}
	for _, channels := range []int{1, 2} {
		seed := encodeAPIRateCELTPacket(t, channels)
		recovery := encodeAPIRateSILKPacket(t, channels)
		if PacketHasLBRR(recovery) {
			t.Fatalf("channels=%d recovery packet unexpectedly carries LBRR", channels)
		}
		for _, rate := range []int{12000, 48000} {
			for _, duration := range []int{40, 60} {
				for _, gain := range []int{0, 768} {
					t.Run(fmt.Sprintf("ch%d_rate%d_ms%d_gain%d", channels, rate, duration, gain), func(t *testing.T) {
						requested := rate * duration / 1000
						packetSamples := rate / 50
						wantSamples := []int{packetSamples, requested, packetSamples}
						packets := [][]byte{seed, recovery, recovery}
						payload := libopustest.NewOraclePayloadVersion("GOSI", 8,
							libopusRefdecodeSingleFormatFloat32, uint32(rate), uint32(int32(gain)),
							uint32(channels), uint32(requested), uint32(len(packets)))
						for i, packet := range packets {
							fec := uint32(0)
							if i == 1 {
								fec = 1
							}
							payload.U32(fec)
							payload.U32(uint32(requested))
							payload.U32(uint32(len(packet)))
							payload.Raw(packet)
						}
						pcmCount := (2*packetSamples + requested) * channels
						reader, err := libopustest.RunOracleVersion(helper, payload.Bytes(), "fixed reference no-LBRR FEC transition", "GOSO", 3)
						if err != nil {
							t.Fatal(err)
						}
						if got := reader.Count(pcmCount); got != pcmCount {
							t.Fatalf("reference PCM count=%d want=%d", got, pcmCount)
						}
						wantPCM := make([]uint32, pcmCount)
						for i := range wantPCM {
							wantPCM[i] = reader.U32()
						}
						if got := reader.Count(len(packets)); got != len(packets) {
							t.Fatalf("reference record count=%d want=%d", got, len(packets))
						}
						wantRange := make([]uint32, len(packets))
						offset := 0
						for i := range packets {
							status, samples, finalRange, pcmOffset := reader.U32(), reader.U32(), reader.U32(), reader.U32()
							if status != 0 || samples != uint32(wantSamples[i]) || pcmOffset != uint32(offset) {
								t.Fatalf("reference step %d record=(%d,%d,%08x,%d), want samples=%d offset=%d", i, status, samples, finalRange, pcmOffset, wantSamples[i], offset)
							}
							wantRange[i] = finalRange
							offset += wantSamples[i] * channels
						}
						if err := reader.ExpectConsumed(); err != nil {
							t.Fatal(err)
						}
						dec, err := NewDecoder(DefaultDecoderConfig(rate, channels))
						if err != nil {
							t.Fatal(err)
						}
						if err := dec.SetGain(gain); err != nil {
							t.Fatal(err)
						}
						frame := make([]float32, requested*channels)
						for pass := range 2 {
							if pass != 0 {
								dec.Reset()
							}
							offset = 0
							for i, packet := range packets {
								var n int
								if i == 1 {
									n, err = dec.DecodeWithFEC(packet, frame, true)
								} else {
									n, err = dec.Decode(packet, frame)
								}
								if err != nil || n != wantSamples[i] {
									t.Fatalf("pass %d step %d decode=(%d,%v), want=%d", pass, i, n, err, wantSamples[i])
								}
								if got := dec.FinalRange(); got != wantRange[i] {
									t.Fatalf("pass %d step %d range=%08x want=%08x", pass, i, got, wantRange[i])
								}
								for j, sample := range frame[:n*channels] {
									if got := math.Float32bits(sample); got != wantPCM[offset+j] {
										t.Fatalf("pass %d step %d sample %d=%08x want=%08x", pass, i, j, got, wantPCM[offset+j])
									}
								}
								offset += n * channels
							}
						}
					})
				}
			}
		}
	}
}

func TestDecoderFixedPointFECTransitionWarmZeroAllocs(t *testing.T) {
	const channels = 2
	seed := encodeAPIRateCELTPacket(t, channels)
	recovery := encodeAPIRateSILKPacket(t, channels)
	if PacketHasLBRR(recovery) {
		t.Fatal("transition packet unexpectedly carries LBRR")
	}
	dec, err := NewDecoder(DefaultDecoderConfig(48000, channels))
	if err != nil {
		t.Fatal(err)
	}
	frame := make([]float32, 1920*channels)
	decodeCycle := func() {
		dec.Reset()
		if n, err := dec.Decode(seed, frame); err != nil || n != 960 {
			t.Fatalf("seed decode=(%d,%v)", n, err)
		}
		if n, err := dec.DecodeWithFEC(recovery, frame, true); err != nil || n != 1920 {
			t.Fatalf("no-LBRR decode=(%d,%v)", n, err)
		}
		if n, err := dec.Decode(recovery, frame); err != nil || n != 960 {
			t.Fatalf("transition decode=(%d,%v)", n, err)
		}
	}
	for range 3 {
		decodeCycle()
	}
	if allocs := testing.AllocsPerRun(20, decodeCycle); allocs != 0 {
		t.Fatalf("warm FEC transition allocations=%g, want zero", allocs)
	}
	hasSignal := false
	for _, sample := range frame[:960*channels] {
		if sample != 0 && !math.IsNaN(float64(sample)) {
			hasSignal = true
			break
		}
	}
	if !hasSignal {
		t.Fatal("warm transition produced no active PCM")
	}
}
