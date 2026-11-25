//go:build gopus_fixed_point

package gopus

import (
	"encoding/binary"
	"fmt"
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/libopustest"
	"math"
	"testing"
)

func TestDecoderFixedPointActiveLossCycleZeroAlloc(t *testing.T) {
	for _, mode := range []EncoderMode{EncoderModeCELT, EncoderModeHybrid} {
		for _, channels := range []int{1, 2} {
			packet := encodeFixedSingleModePacket(t, channels, 960, mode, 0)
			for _, rate := range []int{8000, 12000, 16000, 24000, 48000} {
				for _, format := range []string{"float32", "int16", "int24"} {
					t.Run(fmt.Sprintf("mode%d_ch%d_%dHz_%s", mode, channels, rate, format), func(t *testing.T) {
						d, err := NewDecoder(DefaultDecoderConfig(rate, channels))
						if err != nil {
							t.Fatal(err)
						}
						size := rate / 50
						floats := make([]float32, size*channels)
						ints16 := make([]int16, size*channels)
						ints24 := make([]int32, size*channels)
						active := false
						activeLoss := false
						cycle := func() {
							active, activeLoss = false, false
							for step := 0; step < 17; step++ {
								data := packet
								if step >= 3 && step < 15 {
									data = nil
								}
								var n int
								switch format {
								case "float32":
									n, err = d.Decode(data, floats)
								case "int16":
									n, err = d.DecodeInt16(data, ints16)
								default:
									n, err = d.DecodeInt24(data, ints24)
								}
								if err != nil || n != size {
									t.Fatalf("step%d n=%d err=%v", step, n, err)
								}
								if step == 2 {
									for i := 0; i < size*channels; i++ {
										active = active || floats[i] != 0 || ints16[i] != 0 || ints24[i] != 0
									}
								}
								if step == 3 {
									for i := 0; i < size*channels; i++ {
										activeLoss = activeLoss || floats[i] != 0 || ints16[i] != 0 || ints24[i] != 0
									}
								}
							}
						}
						for range 3 {
							cycle()
						}
						if !active {
							t.Fatal("warm received signal is silent")
						}
						if allocs := testing.AllocsPerRun(10, cycle); allocs != 0 {
							t.Fatalf("active received/loss/recovery cycle allocations=%g want0", allocs)
						}
						if !active || !activeLoss {
							t.Fatal("measured cycle does not contain active received audio and concealment")
						}
					})
				}
			}
		}
	}
}

func encodeFixedCELTSequence(t *testing.T, channels, frameSize, frames int) [][]byte {
	t.Helper()
	const sampleRate = 48000
	enc, err := NewEncoder(EncoderConfig{
		SampleRate:  sampleRate,
		Channels:    channels,
		Application: ApplicationRestrictedCelt,
	})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if err := enc.SetFrameSize(frameSize); err != nil {
		t.Fatalf("SetFrameSize: %v", err)
	}
	if err := enc.SetBandwidth(BandwidthFullband); err != nil {
		t.Fatalf("SetBandwidth: %v", err)
	}
	if err := enc.SetBitrate(128000); err != nil {
		t.Fatalf("SetBitrate: %v", err)
	}
	if channels == 2 {
		if err := enc.SetForceChannels(2); err != nil {
			t.Fatalf("SetForceChannels: %v", err)
		}
	}

	var packets [][]byte
	phase := 0.0
	for f := 0; f < frames; f++ {
		pcm := make([]float32, frameSize*channels)
		for i := 0; i < frameSize; i++ {
			tm := (phase + float64(i)) / sampleRate
			pcm[i*channels] = 0.28 * float32(math.Sin(2*math.Pi*1200*tm))
			if channels == 2 {
				pcm[i*channels+1] = 0.19 * float32(math.Sin(2*math.Pi*1900*tm))
			}
		}
		phase += float64(frameSize)
		pkt, err := enc.EncodeFloat32(pcm)
		if err != nil {
			t.Fatalf("frame %d Encode: %v", f, err)
		}
		if toc := ParseTOC(pkt[0]); toc.Mode != ModeCELT {
			t.Fatalf("encoder produced mode %v, want CELT", toc.Mode)
		}
		packets = append(packets, append([]byte(nil), pkt...))
	}
	return packets
}

// TestDecoderFixedPointCELTPLCParity gates (A) and (B) at the public-API level:
// the public Decoder.DecodeInt16 / DecodeInt24 of a CELT-only stream containing
// LOST frames -- a single loss, a 3-frame burst, then the recovered good frame
// immediately after the loss -- is bit-exact with the libopus FIXED_POINT
// opus_decode / opus_decode24 reference.
//
// (A) the LOST frames route through the integer celt_decode_lost.
// (B) the first good frame after the burst enters celt_decode_with_ec with
// loss_duration != 0, exercising the coarse-energy prediction safety block.
//
// The tested integer outputs match the selected FIXED_POINT libopus reference;
// this assertion does not depend on a floating-point drift allowance.
func TestDecoderFixedPointCELTPLCParity(t *testing.T) {
	libopustest.RequireOracle(t)

	type tc struct {
		name      string
		channels  int
		frameSize int
		// lossAt[i] marks frame index i as a lost frame (PLC). Other frames are
		// the corresponding encoded packet.
		lossAt map[int]bool
		frames int
	}
	cases := []tc{
		{"mono_960_single", 1, 960, map[int]bool{2: true}, 6},
		{"mono_960_burst", 1, 960, map[int]bool{2: true, 3: true, 4: true}, 8},
		{"mono_480_burst", 1, 480, map[int]bool{2: true, 3: true, 4: true}, 8},
		{"mono_240_burst", 1, 240, map[int]bool{3: true, 4: true, 5: true}, 9},
		{"mono_120_burst", 1, 120, map[int]bool{3: true, 4: true, 5: true}, 9},
		{"stereo_960_single", 2, 960, map[int]bool{2: true}, 6},
		{"stereo_960_burst", 2, 960, map[int]bool{2: true, 3: true, 4: true}, 8},
		{"stereo_480_burst", 2, 480, map[int]bool{2: true, 3: true, 4: true}, 8},
	}

	const sampleRate = 48000
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			encoded := encodeFixedCELTSequence(t, c.channels, c.frameSize, c.frames)

			// Build the decode step sequence: nil for lost frames, the encoded
			// packet otherwise.
			steps := make([][]byte, c.frames)
			for i := 0; i < c.frames; i++ {
				if c.lossAt[i] {
					steps[i] = nil
				} else {
					steps[i] = encoded[i]
				}
			}

			refInt16, err := decodeWithLibopusFixedInt16(sampleRate, c.channels, c.frameSize, steps)
			if err != nil {
				libopustest.HelperUnavailable(t, "fixed reference decode int16 (PLC)", err)
				return
			}
			refInt24, err := decodeWithLibopusFixedInt24(sampleRate, c.channels, c.frameSize, steps)
			if err != nil {
				libopustest.HelperUnavailable(t, "fixed reference decode int24 (PLC)", err)
				return
			}

			dec16, err := NewDecoder(DefaultDecoderConfig(sampleRate, c.channels))
			if err != nil {
				t.Fatalf("NewDecoder int16: %v", err)
			}
			dec24, err := NewDecoder(DefaultDecoderConfig(sampleRate, c.channels))
			if err != nil {
				t.Fatalf("NewDecoder int24: %v", err)
			}

			var got16, got24 []int32
			for p, pkt := range steps {
				o16 := make([]int16, c.frameSize*c.channels)
				if _, err := dec16.DecodeInt16(pkt, o16); err != nil {
					t.Fatalf("step %d DecodeInt16: %v", p, err)
				}
				got16 = append(got16, int16ToInt32(o16)...)

				o24 := make([]int32, c.frameSize*c.channels)
				if _, err := dec24.DecodeInt24(pkt, o24); err != nil {
					t.Fatalf("step %d DecodeInt24: %v", p, err)
				}
				got24 = append(got24, o24...)
			}

			assertFixedExact(t, "int16", got16, int16ToInt32(refInt16))
			assertFixedExact(t, "int24", got24, refInt24)
		})
	}
}

func decodeWithLibopusFixedInt16Gain(sampleRate, channels, frameSize, gainQ8 int, packets [][]byte) ([]int16, error) {
	binPath, err := getFixedRefdecodeHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayloadVersion("GOSI", 5,
		libopusRefdecodeSingleFormatInt16, uint32(sampleRate), uint32(int32(gainQ8)),
		uint32(channels), uint32(frameSize), uint32(len(packets)))
	for _, pkt := range packets {
		payload.U32(0) // decode_fec
		payload.U32(uint32(len(pkt)))
		payload.Raw(pkt)
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "fixed reference decode int16 gain", "GOSO")
	if err != nil {
		return nil, err
	}
	nSamples := reader.Count(-1)
	reader.ExpectRemaining(nSamples * 2)
	decoded := make([]int16, nSamples)
	for i := range decoded {
		decoded[i] = reader.I16()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return decoded, nil
}

// decodeWithLibopusFixedInt24Gain drives FIXED_POINT opus_decode24() with a
// non-zero OPUS_SET_GAIN on the reference decoder.
func decodeWithLibopusFixedInt24Gain(sampleRate, channels, frameSize, gainQ8 int, packets [][]byte) ([]int32, error) {
	binPath, err := getFixedRefdecodeHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayloadVersion("GOSI", 5,
		libopusRefdecodeSingleFormatInt24, uint32(sampleRate), uint32(int32(gainQ8)),
		uint32(channels), uint32(frameSize), uint32(len(packets)))
	for _, pkt := range packets {
		payload.U32(0) // decode_fec
		payload.U32(uint32(len(pkt)))
		payload.Raw(pkt)
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "fixed reference decode int24 gain", "GOSO")
	if err != nil {
		return nil, err
	}
	nSamples := reader.Count(-1)
	reader.ExpectRemaining(nSamples * 4)
	decoded := make([]int32, nSamples)
	for i := range decoded {
		decoded[i] = reader.I32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return decoded, nil
}

// encodeFixedDecodeGainCELTSequence encodes a CELT-only multi-frame stream so the
// decode runs entirely through the integer CELT path; the decode-gain stage then
// applies to integer-exact accumulated opus_res samples.
func encodeFixedDecodeGainCELTSequence(t *testing.T, channels, frameSize, frames int) [][]byte {
	t.Helper()
	const sampleRate = 48000
	enc, err := NewEncoder(EncoderConfig{
		SampleRate:  sampleRate,
		Channels:    channels,
		Application: ApplicationAudio,
	})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if err := enc.SetFrameSize(frameSize); err != nil {
		t.Fatalf("SetFrameSize: %v", err)
	}
	if err := enc.SetBandwidth(BandwidthFullband); err != nil {
		t.Fatalf("SetBandwidth: %v", err)
	}
	if err := enc.SetBitrate(96000); err != nil {
		t.Fatalf("SetBitrate: %v", err)
	}
	if err := enc.SetMode(EncoderModeCELT); err != nil {
		t.Fatalf("SetMode: %v", err)
	}
	if channels == 2 {
		if err := enc.SetForceChannels(2); err != nil {
			t.Fatalf("SetForceChannels: %v", err)
		}
	}
	packets := make([][]byte, 0, frames)
	phase := 0.0
	for f := 0; f < frames; f++ {
		pcm := make([]float32, frameSize*channels)
		for i := 0; i < frameSize; i++ {
			tm := (phase + float64(i)) / sampleRate
			// Large-amplitude tone so the decode-gain SATURATE clamp is exercised
			// for the higher positive gains.
			pcm[i*channels] = 0.7 * float32(math.Sin(2*math.Pi*440*tm))
			if channels == 2 {
				pcm[i*channels+1] = 0.65 * float32(math.Sin(2*math.Pi*523*tm+0.2))
			}
		}
		phase += float64(frameSize)
		pkt, err := enc.EncodeFloat32(pcm)
		if err != nil {
			t.Fatalf("frame %d Encode: %v", f, err)
		}
		packets = append(packets, append([]byte(nil), pkt...))
	}
	return packets
}

// TestDecoderFixedPointDecodeGainParity gates that the public DecodeInt16 /
// DecodeInt24 with a non-zero OPUS_SET_GAIN is bit-exact with the libopus
// FIXED_POINT opus_decode / opus_decode24 reference, for the integer CELT path.
// The gain stage (celt_exp2(MULT16_16_P15(...)) -> MULT32_32_Q16 -> SATURATE) is
// applied to the integer-exact opus_res accumulation rather than dropping to the
// float conversion. The selected fixed reference must match on every architecture.
func TestDecoderFixedPointDecodeGainParity(t *testing.T) {
	libopustest.RequireOracle(t)

	const sampleRate = 48000
	gains := []int{256, -256, 1536, -2048, 6144, -6144}
	type tc struct {
		name      string
		channels  int
		frameSize int
		frames    int
	}
	cases := []tc{
		{"mono_960", 1, 960, 4},
		{"stereo_960", 2, 960, 4},
		{"mono_480", 1, 480, 6},
	}

	for _, c := range cases {
		for _, g := range gains {
			c, g := c, g
			t.Run(fmt.Sprintf("%s_gain%d", c.name, g), func(t *testing.T) {
				packets := encodeFixedDecodeGainCELTSequence(t, c.channels, c.frameSize, c.frames)
				if toc := ParseTOC(packets[0][0]); toc.Mode != ModeCELT {
					t.Fatalf("first packet mode %v, want CELT", toc.Mode)
				}

				refInt16, err := decodeWithLibopusFixedInt16Gain(sampleRate, c.channels, c.frameSize, g, packets)
				if err != nil {
					libopustest.HelperUnavailable(t, "fixed reference decode int16 gain", err)
					return
				}
				refInt24, err := decodeWithLibopusFixedInt24Gain(sampleRate, c.channels, c.frameSize, g, packets)
				if err != nil {
					libopustest.HelperUnavailable(t, "fixed reference decode int24 gain", err)
					return
				}

				dec16, _ := NewDecoder(DefaultDecoderConfig(sampleRate, c.channels))
				dec24, _ := NewDecoder(DefaultDecoderConfig(sampleRate, c.channels))
				if err := dec16.SetGain(g); err != nil {
					t.Fatalf("dec16 SetGain: %v", err)
				}
				if err := dec24.SetGain(g); err != nil {
					t.Fatalf("dec24 SetGain: %v", err)
				}

				var got16, got24 []int32
				for p, pkt := range packets {
					o16 := make([]int16, c.frameSize*c.channels)
					if _, err := dec16.DecodeInt16(pkt, o16); err != nil {
						t.Fatalf("packet %d DecodeInt16: %v", p, err)
					}
					got16 = append(got16, int16ToInt32(o16)...)
					o24 := make([]int32, c.frameSize*c.channels)
					if _, err := dec24.DecodeInt24(pkt, o24); err != nil {
						t.Fatalf("packet %d DecodeInt24: %v", p, err)
					}
					got24 = append(got24, o24...)
				}

				assertFixedExact(t, "int16", got16, int16ToInt32(refInt16))
				assertFixedExact(t, "int24", got24, refInt24)
			})
		}
	}
}

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

func TestFixedDecodeGainClampMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	packet := minimalHybridTestPacket20ms()
	for _, gain := range []int{-256, 0, 256, 8 * 256} {
		t.Run(fmt.Sprintf("gain_%d", gain), func(t *testing.T) {
			want, err := decodeWithLibopusReferenceAPIRateFloat32StepsGain(48000, 1, 960, gain,
				[]libopusAPIRateDecodeStep{{packet: packet}})
			if err != nil {
				libopustest.HelperUnavailable(t, "selected fixed gain", err)
			}
			d, err := NewDecoder(DefaultDecoderConfig(48000, 1))
			if err != nil {
				t.Fatal(err)
			}
			if err := d.SetGain(gain); err != nil {
				t.Fatal(err)
			}
			got := make([]float32, 960)
			n, err := d.Decode(packet, got)
			if err != nil {
				t.Fatal(err)
			}
			if n != len(want) {
				t.Fatalf("samples=%d want %d", n, len(want))
			}
			for i, sample := range got[:n] {
				if math.Float32bits(sample) != math.Float32bits(want[i]) {
					t.Fatalf("sample %d: Go=%08x C=%08x", i, math.Float32bits(sample), math.Float32bits(want[i]))
				}
			}
		})
	}
}

type fixedHybridToSILKStep struct {
	name   string
	packet []byte
	format uint32
}

func assertFixedHybridToSILKWarmZeroAllocs(t *testing.T, sampleRate, channels int, hybrid, silk []byte) {
	t.Helper()

	hybridSamples, err := packetSamplesAtRate(hybrid, sampleRate)
	if err != nil {
		t.Fatal(err)
	}
	silkSamples, err := packetSamplesAtRate(silk, sampleRate)
	if err != nil {
		t.Fatal(err)
	}
	dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatal(err)
	}
	out16 := make([]int16, hybridSamples*channels)
	out24 := make([]int32, silkSamples*channels)
	var hybridN, silkN int
	var decodeErr error
	decode := func() {
		dec.Reset()
		hybridN, decodeErr = dec.DecodeInt16(hybrid, out16)
		if decodeErr != nil {
			return
		}
		silkN, decodeErr = dec.DecodeInt24(silk, out24)
	}

	decode()
	if decodeErr != nil || hybridN != hybridSamples || silkN != silkSamples {
		t.Fatalf("warm Hybrid→SILK decode counts=(%d,%d) want=(%d,%d), err=%v", hybridN, silkN, hybridSamples, silkSamples, decodeErr)
	}
	allocs := testing.AllocsPerRun(20, decode)
	if decodeErr != nil || hybridN != hybridSamples || silkN != silkSamples {
		t.Fatalf("measured Hybrid→SILK decode counts=(%d,%d) want=(%d,%d), err=%v", hybridN, silkN, hybridSamples, silkSamples, decodeErr)
	}
	if allocs != 0 {
		t.Fatalf("warm Hybrid→SILK decode allocated %g times per sequence", allocs)
	}
}

// assertFixedHybridToSILKSequence checks a persistent Go decoder against the
// selected fixed-point C decoder while changing public PCM APIs between frames.
func assertFixedHybridToSILKSequence(t *testing.T, dec *Decoder, sampleRate, channels int, steps []fixedHybridToSILKStep) {
	t.Helper()

	cases := make([]libopustest.DecodeDiffCase, len(steps))
	frameSizes := make([]int, len(steps))
	previousFrameSize := sampleRate / 50
	for i, step := range steps {
		frameSize := previousFrameSize
		if len(step.packet) > 0 {
			var err error
			frameSize, err = packetSamplesAtRate(step.packet, sampleRate)
			if err != nil {
				t.Fatalf("step %d %s packetSamplesAtRate: %v", i, step.name, err)
			}
		}
		frameSizes[i] = frameSize
		previousFrameSize = frameSize
		cases[i] = libopustest.DecodeDiffCase{
			Packet:    step.packet,
			Format:    step.format,
			FrameSize: uint32(frameSize),
		}
	}

	want, err := libopustest.ProbeDecodeSequence(sampleRate, channels, cases)
	if err != nil {
		t.Fatal(err)
	}
	for i, step := range steps {
		frameSize := frameSizes[i]
		sampleCount := frameSize * channels
		var gotF32 []float32
		var gotI16 []int16
		var gotI24 []int32
		var n int
		switch step.format {
		case libopustest.DecodeDiffFormatFloat32:
			gotF32 = make([]float32, sampleCount)
			n, err = dec.Decode(step.packet, gotF32)
		case libopustest.DecodeDiffFormatInt16:
			gotI16 = make([]int16, sampleCount)
			n, err = dec.DecodeInt16(step.packet, gotI16)
		case libopustest.DecodeDiffFormatInt24:
			gotI24 = make([]int32, sampleCount)
			n, err = dec.DecodeInt24(step.packet, gotI24)
		default:
			t.Fatalf("step %d %s has unsupported PCM format %d", i, step.name, step.format)
		}
		if err != nil || n != int(want[i].Code) {
			t.Fatalf("step %d %s samples=%d err=%v C=%d", i, step.name, n, err, want[i].Code)
		}
		if got := dec.FinalRange(); got != want[i].FinalRange {
			t.Fatalf("step %d %s final range=%08x C=%08x", i, step.name, got, want[i].FinalRange)
		}

		bytesPerSample := 4
		if step.format == libopustest.DecodeDiffFormatInt16 {
			bytesPerSample = 2
		}
		if len(want[i].PCM) != sampleCount*bytesPerSample {
			t.Fatalf("step %d %s C PCM bytes=%d want=%d", i, step.name, len(want[i].PCM), sampleCount*bytesPerSample)
		}
		for j := 0; j < sampleCount; j++ {
			var got, expected int64
			switch step.format {
			case libopustest.DecodeDiffFormatFloat32:
				got = int64(math.Float32bits(gotF32[j]))
				expected = int64(binary.LittleEndian.Uint32(want[i].PCM[j*4:]))
			case libopustest.DecodeDiffFormatInt16:
				got = int64(gotI16[j])
				expected = int64(int16(binary.LittleEndian.Uint16(want[i].PCM[j*2:])))
			case libopustest.DecodeDiffFormatInt24:
				got = int64(gotI24[j])
				expected = int64(int32(binary.LittleEndian.Uint32(want[i].PCM[j*4:])))
			}
			if got != expected {
				t.Fatalf("step %d %s sample %d Go=%d C=%d", i, step.name, j, got, expected)
			}
		}
	}
}

// TestDecoderFixedPointHybridToSILKFadeParity covers the 2.5 ms CELT silence
// accumulation that opus_decode_frame applies after SILK when the previous mode
// is Hybrid. The test uses a 60 ms SILK packet so the captured fixed-point output
// starts after the Hybrid frame in the same persistent state and exercises the
// packet-tail offset used when adding the CELT overlap.
func TestDecoderFixedPointHybridToSILKFadeParity(t *testing.T) {
	libopustest.RequireOracle(t)

	hybrid := encodeAPIRateHybridPacketFrameSize(t, 1, 1920)
	silk := encodeAPIRateSILKPacketFrameSize(t, 2, 2880)
	celtShort := encodeAPIRateCELTPacketFrameSize(t, 1, 120)
	if got := ParseTOC(hybrid[0]).Mode; got != ModeHybrid {
		t.Fatalf("Hybrid witness TOC mode=%v", got)
	}
	if got := ParseTOC(silk[0]).Mode; got != ModeSILK {
		t.Fatalf("SILK witness TOC mode=%v", got)
	}

	sampleRates := []int{8000, 12000, 16000, 24000, 48000}
	if extsupport.QEXT {
		sampleRates = append(sampleRates, 96000)
	}
	for _, sampleRate := range sampleRates {
		for _, channels := range []int{1, 2} {
			t.Run(fmt.Sprintf("%dHz/ch%d", sampleRate, channels), func(t *testing.T) {
				dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
				if err != nil {
					t.Fatal(err)
				}
				assertFixedHybridToSILKSequence(t, dec, sampleRate, channels, []fixedHybridToSILKStep{
					{name: "hybrid-40ms-int16", packet: hybrid, format: libopustest.DecodeDiffFormatInt16},
					{name: "silk-60ms-int24", packet: silk, format: libopustest.DecodeDiffFormatInt24},
					{name: "silk-60ms-float-followup", packet: silk, format: libopustest.DecodeDiffFormatFloat32},
					{name: "silk-plc-int16", format: libopustest.DecodeDiffFormatInt16},
					{name: "celt-2p5ms-int24-recovery", packet: celtShort, format: libopustest.DecodeDiffFormatInt24},
				})

				// A fresh SILK decode is the control: the one-unit mismatch requires
				// the preceding Hybrid state and its CELT overlap fade.
				fresh, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
				if err != nil {
					t.Fatal(err)
				}
				assertFixedHybridToSILKSequence(t, fresh, sampleRate, channels, []fixedHybridToSILKStep{
					{name: "fresh-silk-60ms-int24", packet: silk, format: libopustest.DecodeDiffFormatInt24},
				})

				// Reset must clear the prior Hybrid/CELT history before comparing
				// the same sequence suffix with a newly initialized C decoder.
				dec.Reset()
				assertFixedHybridToSILKSequence(t, dec, sampleRate, channels, []fixedHybridToSILKStep{
					{name: "reset-silk-60ms-int24", packet: silk, format: libopustest.DecodeDiffFormatInt24},
					{name: "reset-hybrid-40ms-int16", packet: hybrid, format: libopustest.DecodeDiffFormatInt16},
					{name: "reset-hybrid-plc-float", format: libopustest.DecodeDiffFormatFloat32},
					{name: "reset-celt-2p5ms-int24", packet: celtShort, format: libopustest.DecodeDiffFormatInt24},
				})

				assertFixedHybridToSILKWarmZeroAllocs(t, sampleRate, channels, hybrid, silk)
			})
		}
	}
}

func encodeFixedModeSwitchSequence(t *testing.T, channels, frameSize int, modes []EncoderMode) [][]byte {
	t.Helper()
	const sampleRate = 48000
	enc, err := NewEncoder(EncoderConfig{
		SampleRate:  sampleRate,
		Channels:    channels,
		Application: ApplicationAudio,
	})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if err := enc.SetFrameSize(frameSize); err != nil {
		t.Fatalf("SetFrameSize: %v", err)
	}
	if err := enc.SetBandwidth(BandwidthFullband); err != nil {
		t.Fatalf("SetBandwidth: %v", err)
	}
	if err := enc.SetBitrate(96000); err != nil {
		t.Fatalf("SetBitrate: %v", err)
	}
	if err := enc.SetInBandFEC(InBandFECDisabled); err != nil {
		t.Fatalf("SetInBandFEC: %v", err)
	}
	if channels == 2 {
		if err := enc.SetForceChannels(2); err != nil {
			t.Fatalf("SetForceChannels: %v", err)
		}
	}

	packets := make([][]byte, 0, len(modes))
	phase := 0.0
	for f, m := range modes {
		if err := enc.SetMode(m); err != nil {
			t.Fatalf("frame %d SetMode: %v", f, err)
		}
		pcm := make([]float32, frameSize*channels)
		for i := 0; i < frameSize; i++ {
			tm := (phase + float64(i)) / sampleRate
			pcm[i*channels] = 0.24*float32(math.Sin(2*math.Pi*220*tm)) +
				0.12*float32(math.Sin(2*math.Pi*1300*tm+0.17))
			if channels == 2 {
				pcm[i*channels+1] = 0.21*float32(math.Sin(2*math.Pi*330*tm+0.09)) +
					0.10*float32(math.Sin(2*math.Pi*1700*tm+0.31))
			}
		}
		phase += float64(frameSize)
		pkt, err := enc.EncodeFloat32(pcm)
		if err != nil {
			t.Fatalf("frame %d Encode: %v", f, err)
		}
		packets = append(packets, append([]byte(nil), pkt...))
	}
	return packets
}

// encodeFixedSingleModePacket encodes one independent frame in the given forced
// mode from a fresh encoder, so a concatenated CELT-then-Hybrid sequence produces
// a Hybrid frame whose previous frame was CELT but which carries no redundancy
// (a fresh encoder has no prior frame to prefill from), exercising the pure
// integer transition smooth_fade crossfade.
func encodeFixedSingleModePacket(t *testing.T, channels, frameSize int, mode EncoderMode, phase float64) []byte {
	t.Helper()
	const sampleRate = 48000
	enc, err := NewEncoder(EncoderConfig{
		SampleRate:  sampleRate,
		Channels:    channels,
		Application: ApplicationAudio,
	})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if err := enc.SetFrameSize(frameSize); err != nil {
		t.Fatalf("SetFrameSize: %v", err)
	}
	if err := enc.SetBandwidth(BandwidthFullband); err != nil {
		t.Fatalf("SetBandwidth: %v", err)
	}
	if err := enc.SetBitrate(96000); err != nil {
		t.Fatalf("SetBitrate: %v", err)
	}
	if err := enc.SetInBandFEC(InBandFECDisabled); err != nil {
		t.Fatalf("SetInBandFEC: %v", err)
	}
	if channels == 2 {
		if err := enc.SetForceChannels(2); err != nil {
			t.Fatalf("SetForceChannels: %v", err)
		}
	}
	if err := enc.SetMode(mode); err != nil {
		t.Fatalf("SetMode: %v", err)
	}
	pcm := make([]float32, frameSize*channels)
	for i := 0; i < frameSize; i++ {
		tm := (phase + float64(i)) / sampleRate
		pcm[i*channels] = 0.24*float32(math.Sin(2*math.Pi*220*tm)) +
			0.12*float32(math.Sin(2*math.Pi*1300*tm+0.17))
		if channels == 2 {
			pcm[i*channels+1] = 0.21*float32(math.Sin(2*math.Pi*330*tm+0.09)) +
				0.10*float32(math.Sin(2*math.Pi*1700*tm+0.31))
		}
	}
	pkt, err := enc.EncodeFloat32(pcm)
	if err != nil {
		t.Fatalf("Encode: %v", err)
	}
	return append([]byte(nil), pkt...)
}

// TestDecoderFixedPointHybridTransitionParity gates that the public DecodeInt16 /
// DecodeInt24 of a CELT-then-Hybrid transition (no redundancy) is bit-exact with
// the libopus FIXED_POINT opus_decode / opus_decode24 reference. The Hybrid frame
// after a CELT frame triggers the 5 ms CELT PLC pcm_transition decode and the
// integer opus_res smooth_fade crossfade (opus_decode_frame transition path).
// The tested integer outputs match the selected FIXED_POINT reference build.
func TestDecoderFixedPointHybridTransitionParity(t *testing.T) {
	libopustest.RequireOracle(t)

	type tc struct {
		name      string
		channels  int
		frameSize int
	}
	cases := []tc{
		{"mono_960", 1, 960},
		{"stereo_960", 2, 960},
		{"mono_480", 1, 480},
	}

	const sampleRate = 48000
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			phase := 0.0
			celtPkt := encodeFixedSingleModePacket(t, c.channels, c.frameSize, EncoderModeCELT, phase)
			phase += float64(c.frameSize)
			hyb1 := encodeFixedSingleModePacket(t, c.channels, c.frameSize, EncoderModeHybrid, phase)
			phase += float64(c.frameSize)
			hyb2 := encodeFixedSingleModePacket(t, c.channels, c.frameSize, EncoderModeHybrid, phase)
			packets := [][]byte{celtPkt, hyb1, hyb2}

			if toc := ParseTOC(celtPkt[0]); toc.Mode != ModeCELT {
				t.Fatalf("first packet mode %v, want CELT", toc.Mode)
			}
			if toc := ParseTOC(hyb1[0]); toc.Mode != ModeHybrid {
				t.Fatalf("second packet mode %v, want Hybrid", toc.Mode)
			}

			refInt16, err := decodeWithLibopusFixedInt16(sampleRate, c.channels, c.frameSize, packets)
			if err != nil {
				libopustest.HelperUnavailable(t, "fixed reference decode int16", err)
				return
			}
			refInt24, err := decodeWithLibopusFixedInt24(sampleRate, c.channels, c.frameSize, packets)
			if err != nil {
				libopustest.HelperUnavailable(t, "fixed reference decode int24", err)
				return
			}

			dec16, _ := NewDecoder(DefaultDecoderConfig(sampleRate, c.channels))
			dec24, _ := NewDecoder(DefaultDecoderConfig(sampleRate, c.channels))
			var got16, got24 []int32
			for p, pkt := range packets {
				o16 := make([]int16, c.frameSize*c.channels)
				if _, err := dec16.DecodeInt16(pkt, o16); err != nil {
					t.Fatalf("packet %d DecodeInt16: %v", p, err)
				}
				got16 = append(got16, int16ToInt32(o16)...)
				o24 := make([]int32, c.frameSize*c.channels)
				if _, err := dec24.DecodeInt24(pkt, o24); err != nil {
					t.Fatalf("packet %d DecodeInt24: %v", p, err)
				}
				got24 = append(got24, o24...)
			}

			if dec16.fixedTransitionApplied == 0 {
				t.Fatalf("stream did not exercise the integer transition crossfade")
			}
			t.Logf("integer transition applied=%d, redundancy applied=%d",
				dec16.fixedTransitionApplied, dec16.fixedRedundancyApplied)

			assertFixedExact(t, "int16", got16, int16ToInt32(refInt16))
			assertFixedExact(t, "int24", got24, refInt24)
		})
	}
}

// TestDecoderFixedPointHybridRedundancyTransitionParity gates that the public
// DecodeInt16 / DecodeInt24 of a mode-switching stream (Hybrid <-> CELT) remains
// bit-exact with the libopus FIXED_POINT opus_decode / opus_decode24 reference
// across the frames that carry CELT redundancy and the CELT->SILK/Hybrid
// transition smooth_fade. The integer redundancy frame is decoded in the opus_res
// domain on the same integer CELT decoder as the main hybrid highband, and the
// crossfade uses the integer smooth_fade (FIXED_POINT ENABLE_RES24), mirroring
// opus_decode_frame. The tested integer outputs match the selected FIXED_POINT
// reference build.
func TestDecoderFixedPointHybridRedundancyTransitionParity(t *testing.T) {
	libopustest.RequireOracle(t)

	type tc struct {
		name      string
		channels  int
		frameSize int
		modes     []EncoderMode
	}
	cases := []tc{
		{
			name:      "mono_hybrid_celt_hybrid",
			channels:  1,
			frameSize: 960,
			modes:     []EncoderMode{EncoderModeHybrid, EncoderModeHybrid, EncoderModeCELT, EncoderModeHybrid, EncoderModeHybrid},
		},
		{
			name:      "stereo_hybrid_celt_hybrid",
			channels:  2,
			frameSize: 960,
			modes:     []EncoderMode{EncoderModeHybrid, EncoderModeHybrid, EncoderModeCELT, EncoderModeHybrid, EncoderModeHybrid},
		},
		{
			name:      "mono_celt_hybrid_celt",
			channels:  1,
			frameSize: 960,
			modes:     []EncoderMode{EncoderModeHybrid, EncoderModeCELT, EncoderModeHybrid, EncoderModeCELT, EncoderModeHybrid, EncoderModeHybrid},
		},
		{
			name:      "mono_480_alternating",
			channels:  1,
			frameSize: 480,
			modes:     []EncoderMode{EncoderModeHybrid, EncoderModeHybrid, EncoderModeCELT, EncoderModeCELT, EncoderModeHybrid, EncoderModeHybrid},
		},
	}

	const sampleRate = 48000
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			packets := encodeFixedModeSwitchSequence(t, c.channels, c.frameSize, c.modes)

			refInt16, err := decodeWithLibopusFixedInt16(sampleRate, c.channels, c.frameSize, packets)
			if err != nil {
				libopustest.HelperUnavailable(t, "fixed reference decode int16", err)
				return
			}
			refInt24, err := decodeWithLibopusFixedInt24(sampleRate, c.channels, c.frameSize, packets)
			if err != nil {
				libopustest.HelperUnavailable(t, "fixed reference decode int24", err)
				return
			}

			dec16, _ := NewDecoder(DefaultDecoderConfig(sampleRate, c.channels))
			dec24, _ := NewDecoder(DefaultDecoderConfig(sampleRate, c.channels))
			var got16, got24 []int32
			for p, pkt := range packets {
				o16 := make([]int16, c.frameSize*c.channels)
				if _, err := dec16.DecodeInt16(pkt, o16); err != nil {
					t.Fatalf("packet %d DecodeInt16: %v", p, err)
				}
				got16 = append(got16, int16ToInt32(o16)...)
				o24 := make([]int32, c.frameSize*c.channels)
				if _, err := dec24.DecodeInt24(pkt, o24); err != nil {
					t.Fatalf("packet %d DecodeInt24: %v", p, err)
				}
				got24 = append(got24, o24...)
			}

			// Confirm the stream actually exercises the integer redundancy /
			// transition smooth_fade path so the gate is meaningful.
			if dec16.fixedRedundancyApplied == 0 && dec16.fixedTransitionApplied == 0 {
				t.Fatalf("stream did not exercise the integer redundancy/transition path "+
					"(redundancy=%d transition=%d)", dec16.fixedRedundancyApplied, dec16.fixedTransitionApplied)
			}
			t.Logf("integer redundancy applied=%d, transition applied=%d",
				dec16.fixedRedundancyApplied, dec16.fixedTransitionApplied)

			assertFixedExact(t, "int16", got16, int16ToInt32(refInt16))
			assertFixedExact(t, "int24", got24, refInt24)
		})
	}
}

// TestDecoderFixedPointRedundancyCodedOutputChannelParity keeps the CELT
// redundancy decode's packet-coded channel count separate from the public
// decoder output channel count. libopus passes stream_channels as C while
// retaining the decoder's channel count as CC during synthesis.
func TestDecoderFixedPointRedundancyCodedOutputChannelParity(t *testing.T) {
	libopustest.RequireOracle(t)

	const sampleRate = 48000
	const frameSize = 960
	modes := []EncoderMode{
		EncoderModeHybrid, EncoderModeHybrid, EncoderModeCELT,
		EncoderModeHybrid, EncoderModeHybrid,
	}

	for _, tc := range []struct {
		name           string
		codedChannels  int
		outputChannels int
	}{
		{name: "stereo_packet_mono_output", codedChannels: 2, outputChannels: 1},
		{name: "mono_packet_stereo_output", codedChannels: 1, outputChannels: 2},
	} {
		t.Run(tc.name, func(t *testing.T) {
			packets := encodeFixedModeSwitchSequence(t, tc.codedChannels, frameSize, modes)

			refInt16, err := decodeWithLibopusFixedInt16(sampleRate, tc.outputChannels, frameSize, packets)
			if err != nil {
				libopustest.HelperUnavailable(t, "fixed reference decode int16", err)
				return
			}
			refInt24, err := decodeWithLibopusFixedInt24(sampleRate, tc.outputChannels, frameSize, packets)
			if err != nil {
				libopustest.HelperUnavailable(t, "fixed reference decode int24", err)
				return
			}

			dec16, err := NewDecoder(DefaultDecoderConfig(sampleRate, tc.outputChannels))
			if err != nil {
				t.Fatal(err)
			}
			dec24, err := NewDecoder(DefaultDecoderConfig(sampleRate, tc.outputChannels))
			if err != nil {
				t.Fatal(err)
			}
			for p, packet := range packets {
				out16 := make([]int16, frameSize*tc.outputChannels)
				if _, err := dec16.DecodeInt16(packet, out16); err != nil {
					t.Fatalf("packet %d DecodeInt16: %v", p, err)
				}
				frame16 := int16ToInt32(out16)
				out24 := make([]int32, frameSize*tc.outputChannels)
				if _, err := dec24.DecodeInt24(packet, out24); err != nil {
					t.Fatalf("packet %d DecodeInt24: %v", p, err)
				}
				start := p * frameSize * tc.outputChannels
				assertFixedExact(t, fmt.Sprintf("int16 packet %d", p), frame16, int16ToInt32(refInt16[start:start+len(frame16)]))
				assertFixedExact(t, fmt.Sprintf("int24 packet %d", p), out24, refInt24[start:start+len(out24)])
			}

			if dec16.fixedRedundancyApplied == 0 {
				t.Fatalf("stream did not exercise integer redundancy decode")
			}
		})
	}
}

func decodeWithLibopusFixedPhaseControl(sampleFormat uint32, sampleRate, channels, frameSize int, disabled bool, packets [][]byte) (*libopustest.OracleReader, error) {
	binPath, err := getFixedRefdecodeHelperPath()
	if err != nil {
		return nil, err
	}
	phase := uint32(0)
	if disabled {
		phase = 1
	}
	payload := libopustest.NewOraclePayloadVersion("GOSI", 9,
		sampleFormat, uint32(sampleRate), 0, phase,
		uint32(channels), uint32(frameSize), uint32(len(packets)))
	for _, packet := range packets {
		payload.U32(0) // decode_fec
		payload.U32(uint32(frameSize))
		payload.U32(uint32(len(packet)))
		payload.Raw(packet)
	}
	return libopustest.RunOracle(binPath, payload.Bytes(), "fixed reference phase-control decode", "GOSO")
}

func TestFixedPointPhaseInversionControlMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const sampleRate, channels, frameSize = 48000, 2, 960
	packets := encodeFixedModeSwitchSequence(t, channels, frameSize,
		[]EncoderMode{EncoderModeCELT, EncoderModeCELT, EncoderModeCELT})
	if len(packets) < 2 {
		t.Fatalf("encoder produced %d packets", len(packets))
	}
	for i, packet := range packets {
		if len(packet) == 0 || packet[0]&4 == 0 {
			t.Fatalf("packet %d is not stereo-coded: %x", i, packet[:min(len(packet), 1)])
		}
	}

	for _, phaseDisabled := range []bool{true, false} {
		name := "phase_enabled"
		if phaseDisabled {
			name = "phase_disabled"
		}
		t.Run(name, func(t *testing.T) {
			for _, sampleFormat := range []uint32{libopusRefdecodeSingleFormatInt16, libopusRefdecodeSingleFormatInt24} {
				refReader, err := decodeWithLibopusFixedPhaseControl(sampleFormat, sampleRate, channels, frameSize, phaseDisabled, packets)
				if err != nil {
					libopustest.HelperUnavailable(t, "fixed reference phase-control decode", err)
					return
				}
				count := refReader.Count(-1)
				if count != len(packets)*frameSize*channels {
					t.Fatalf("reference samples=%d want %d", count, len(packets)*frameSize*channels)
				}
				want16 := make([]int16, count)
				want24 := make([]int32, count)
				if sampleFormat == libopusRefdecodeSingleFormatInt16 {
					refReader.ExpectRemaining(count * 2)
					for i := range want16 {
						want16[i] = refReader.I16()
					}
				} else {
					refReader.ExpectRemaining(count * 4)
					for i := range want24 {
						want24[i] = refReader.I32()
					}
				}
				if err := refReader.ExpectConsumed(); err != nil {
					t.Fatal(err)
				}

				dec, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
				if err != nil {
					t.Fatal(err)
				}
				if dec.PhaseInversionDisabled() {
					t.Fatal("stereo decoder default disables phase inversion")
				}
				if phaseDisabled {
					// Set before the fixed CELT sidecar exists; its lazy creation
					// must inherit the public decoder control.
					dec.SetPhaseInversionDisabled(true)
				}
				got16 := make([]int16, 0, count)
				got24 := make([]int32, 0, count)
				out16 := make([]int16, frameSize*channels)
				out24 := make([]int32, frameSize*channels)
				for p, packet := range packets {
					if sampleFormat == libopusRefdecodeSingleFormatInt16 {
						if n, err := dec.DecodeInt16(packet, out16); err != nil || n != frameSize {
							t.Fatalf("packet %d DecodeInt16 samples=%d err=%v", p, n, err)
						}
						got16 = append(got16, out16...)
					} else {
						if n, err := dec.DecodeInt24(packet, out24); err != nil || n != frameSize {
							t.Fatalf("packet %d DecodeInt24 samples=%d err=%v", p, n, err)
						}
						got24 = append(got24, out24...)
					}
				}
				if sampleFormat == libopusRefdecodeSingleFormatInt16 {
					assertFixedExact(t, "phase-control int16", int16ToInt32(got16), int16ToInt32(want16))
				} else {
					assertFixedExact(t, "phase-control int24", got24, want24)
				}

				// A live control update reaches an already initialized Q15 CELT
				// decoder. Reset clears codec history but preserves the control,
				// as OPUS_RESET_STATE does for disable_inv.
				dec.SetPhaseInversionDisabled(!phaseDisabled)
				dec.Reset()
				if got := dec.PhaseInversionDisabled(); got == phaseDisabled {
					t.Fatalf("phase-inversion setting after Reset=%t, want %t", got, !phaseDisabled)
				}
				refReader, err = decodeWithLibopusFixedPhaseControl(sampleFormat, sampleRate, channels, frameSize, !phaseDisabled, packets)
				if err != nil {
					libopustest.HelperUnavailable(t, "fixed reference phase-control reset decode", err)
					return
				}
				count = refReader.Count(-1)
				if sampleFormat == libopusRefdecodeSingleFormatInt16 {
					refReader.ExpectRemaining(count * 2)
					for i := range want16 {
						want16[i] = refReader.I16()
					}
				} else {
					refReader.ExpectRemaining(count * 4)
					for i := range want24 {
						want24[i] = refReader.I32()
					}
				}
				if err := refReader.ExpectConsumed(); err != nil {
					t.Fatal(err)
				}
				if sampleFormat == libopusRefdecodeSingleFormatInt16 {
					got16 = got16[:0]
					for p, packet := range packets {
						if n, err := dec.DecodeInt16(packet, out16); err != nil || n != frameSize {
							t.Fatalf("post-reset packet %d DecodeInt16 samples=%d err=%v", p, n, err)
						}
						got16 = append(got16, out16...)
					}
					assertFixedExact(t, "post-reset phase-control int16", int16ToInt32(got16), int16ToInt32(want16))
				} else {
					got24 = got24[:0]
					for p, packet := range packets {
						if n, err := dec.DecodeInt24(packet, out24); err != nil || n != frameSize {
							t.Fatalf("post-reset packet %d DecodeInt24 samples=%d err=%v", p, n, err)
						}
						got24 = append(got24, out24...)
					}
					assertFixedExact(t, "post-reset phase-control int24", got24, want24)
				}

				// Measure the warmed fixed-point CELT path with caller-owned
				// output storage; helper setup and reset are outside the run.
				allocs := testing.AllocsPerRun(20, func() {
					if sampleFormat == libopusRefdecodeSingleFormatInt16 {
						if _, err := dec.DecodeInt16(packets[len(packets)-1], out16); err != nil {
							panic(err)
						}
					} else if _, err := dec.DecodeInt24(packets[len(packets)-1], out24); err != nil {
						panic(err)
					}
				})
				if allocs != 0 {
					t.Fatalf("warmed phase-control decode allocated %g times/run", allocs)
				}
			}
		})
	}
}

var fixedRefdecodeHelper libopustest.HelperCache

// getFixedRefdecodeHelperPath builds the libopus_refdecode_single.c full-pipeline
// opus_decode / opus_decode24 helper against the public fixed-point reference
// selected for this Go build, including ENABLE_QEXT when that tag is active.
func getFixedRefdecodeHelperPath() (string, error) {
	return fixedRefdecodeHelper.Path(func() (string, error) {
		return libopustest.BuildPublicAPIHelper(libopustest.CHelperConfig{
			Label:       "fixed-point reference decode",
			OutputBase:  "gopus_libopus_refdecode_fixed",
			SourceFile:  "libopus_refdecode_single.c",
			CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
			RefIncludes: []string{"celt", "silk"},
			DeadStrip:   true,
		})
	})
}

// decodeWithLibopusFixedInt16 drives FIXED_POINT opus_decode() for a sequence of
// packets through the refdecode_single helper.
func decodeWithLibopusFixedInt16(sampleRate, channels, frameSize int, packets [][]byte) ([]int16, error) {
	binPath, err := getFixedRefdecodeHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayloadVersion("GOSI", 5,
		libopusRefdecodeSingleFormatInt16, uint32(sampleRate), 0,
		uint32(channels), uint32(frameSize), uint32(len(packets)))
	for _, pkt := range packets {
		payload.U32(0) // decode_fec
		payload.U32(uint32(len(pkt)))
		payload.Raw(pkt)
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "fixed reference decode int16", "GOSO")
	if err != nil {
		return nil, err
	}
	nSamples := reader.Count(-1)
	reader.ExpectRemaining(nSamples * 2)
	decoded := make([]int16, nSamples)
	for i := range decoded {
		decoded[i] = reader.I16()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return decoded, nil
}

// decodeWithLibopusFixedInt24 drives FIXED_POINT opus_decode24() for a sequence
// of packets through the refdecode_single helper.
func decodeWithLibopusFixedInt24(sampleRate, channels, frameSize int, packets [][]byte) ([]int32, error) {
	binPath, err := getFixedRefdecodeHelperPath()
	if err != nil {
		return nil, err
	}
	payload := libopustest.NewOraclePayloadVersion("GOSI", 5,
		libopusRefdecodeSingleFormatInt24, uint32(sampleRate), 0,
		uint32(channels), uint32(frameSize), uint32(len(packets)))
	for _, pkt := range packets {
		payload.U32(0) // decode_fec
		payload.U32(uint32(len(pkt)))
		payload.Raw(pkt)
	}
	reader, err := libopustest.RunOracle(binPath, payload.Bytes(), "fixed reference decode int24", "GOSO")
	if err != nil {
		return nil, err
	}
	nSamples := reader.Count(-1)
	reader.ExpectRemaining(nSamples * 4)
	decoded := make([]int32, nSamples)
	for i := range decoded {
		decoded[i] = reader.I32()
	}
	if err := reader.ExpectConsumed(); err != nil {
		return nil, err
	}
	return decoded, nil
}

// encodeFixedSILKSequence encodes a multi-frame SILK-only stream at the given
// bandwidth so the decode exercises cross-frame SILK + resampler state.
func encodeFixedSILKSequence(t *testing.T, channels, frameSize, frames int, bw Bandwidth) [][]byte {
	t.Helper()
	const sampleRate = 48000
	enc, err := NewEncoder(EncoderConfig{
		SampleRate:  sampleRate,
		Channels:    channels,
		Application: ApplicationRestrictedSilk,
	})
	if err != nil {
		t.Fatalf("NewEncoder: %v", err)
	}
	if err := enc.SetFrameSize(frameSize); err != nil {
		t.Fatalf("SetFrameSize: %v", err)
	}
	if err := enc.SetBandwidth(bw); err != nil {
		t.Fatalf("SetBandwidth: %v", err)
	}
	if err := enc.SetBitrate(24000); err != nil {
		t.Fatalf("SetBitrate: %v", err)
	}
	if err := enc.SetInBandFEC(InBandFECDisabled); err != nil {
		t.Fatalf("SetInBandFEC: %v", err)
	}
	if channels == 2 {
		if err := enc.SetForceChannels(2); err != nil {
			t.Fatalf("SetForceChannels: %v", err)
		}
	}

	var packets [][]byte
	phase := 0.0
	for f := 0; f < frames; f++ {
		pcm := make([]float32, frameSize*channels)
		for i := 0; i < frameSize; i++ {
			tm := (phase + float64(i)) / sampleRate
			pcm[i*channels] = 0.22 * float32(math.Sin(2*math.Pi*440*tm))
			if channels == 2 {
				pcm[i*channels+1] = 0.18 * float32(math.Sin(2*math.Pi*660*tm))
			}
		}
		phase += float64(frameSize)
		pkt, err := enc.EncodeFloat32(pcm)
		if err != nil {
			t.Fatalf("frame %d Encode: %v", f, err)
		}
		packets = append(packets, append([]byte(nil), pkt...))
	}
	return packets
}

// TestDecoderFixedPointSILKParity gates that the public DecodeInt16 / DecodeInt24
// of SILK-only packets is bit-exact with the libopus FIXED_POINT opus_decode /
// opus_decode24 reference. gopus' silk.Decoder is inherently integer (int16
// native samples + the int16 silk_resampler), and the int16/int24 output of a
// SILK-only frame round-trips through float32 without loss, so the existing
// public path is checked against the selected FIXED_POINT reference.
func TestDecoderFixedPointSILKParity(t *testing.T) {
	libopustest.RequireOracle(t)

	type tc struct {
		name      string
		channels  int
		frameSize int
		bw        Bandwidth
	}
	cases := []tc{
		{"mono_wb_960", 1, 960, BandwidthWideband},
		{"stereo_wb_960", 2, 960, BandwidthWideband},
		{"mono_wb_480", 1, 480, BandwidthWideband},
		{"mono_mb_960", 1, 960, BandwidthMediumband},
		{"mono_nb_960", 1, 960, BandwidthNarrowband},
	}

	const sampleRate = 48000
	const frames = 5
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			packets := encodeFixedSILKSequence(t, c.channels, c.frameSize, frames, c.bw)
			for _, pkt := range packets {
				if toc := ParseTOC(pkt[0]); toc.Mode != ModeSILK {
					t.Fatalf("encoder produced mode %v, want SILK", toc.Mode)
				}
			}

			refInt16, err := decodeWithLibopusFixedInt16(sampleRate, c.channels, c.frameSize, packets)
			if err != nil {
				libopustest.HelperUnavailable(t, "fixed reference decode int16", err)
				return
			}
			refInt24, err := decodeWithLibopusFixedInt24(sampleRate, c.channels, c.frameSize, packets)
			if err != nil {
				libopustest.HelperUnavailable(t, "fixed reference decode int24", err)
				return
			}

			dec16, err := NewDecoder(DefaultDecoderConfig(sampleRate, c.channels))
			if err != nil {
				t.Fatalf("NewDecoder int16: %v", err)
			}
			dec24, err := NewDecoder(DefaultDecoderConfig(sampleRate, c.channels))
			if err != nil {
				t.Fatalf("NewDecoder int24: %v", err)
			}

			var got16 []int32
			var got24 []int32
			for p, pkt := range packets {
				o16 := make([]int16, c.frameSize*c.channels)
				if _, err := dec16.DecodeInt16(pkt, o16); err != nil {
					t.Fatalf("packet %d DecodeInt16: %v", p, err)
				}
				got16 = append(got16, int16ToInt32(o16)...)
				o24 := make([]int32, c.frameSize*c.channels)
				if _, err := dec24.DecodeInt24(pkt, o24); err != nil {
					t.Fatalf("packet %d DecodeInt24: %v", p, err)
				}
				got24 = append(got24, o24...)
			}

			assertFixedExact(t, "int16", got16, int16ToInt32(refInt16))
			assertFixedExact(t, "int24", got24, refInt24)
		})
	}
}

// TestDecoderFixedPointHybridParity gates that the public DecodeInt16 /
// DecodeInt24 of Hybrid packets is bit-exact with the libopus FIXED_POINT
// opus_decode / opus_decode24 reference. The integer SILK lowband
// (INT16TORES of the resampled int16 SILK output) is combined with the integer
// FIXED_POINT CELT highband (bands 17-21) via internal/fixedpoint.CELTDecoder
// driven from the shared range decoder with celt_accum, mirroring
// opus_decode_frame (start_band=17, celt_accum=1). Multi-frame packets exercise
// the cross-frame integer CELT state (decode_mem, energy histories, post-filter,
// preemph). Bit-exact on every architecture: the integer decode has no
// fused-multiply-add, so there is no per-arch float drift.
func TestDecoderFixedPointHybridParity(t *testing.T) {
	libopustest.RequireOracle(t)

	type tc struct {
		name      string
		channels  int
		frameSize int
		frames    int
	}
	cases := []tc{
		{"mono_960", 1, 960, 1},
		{"stereo_960", 2, 960, 1},
		{"mono_480", 1, 480, 1},
		{"mono_960_multi", 1, 960, 4},
		{"stereo_960_multi", 2, 960, 4},
		{"mono_480_multi", 1, 480, 6},
	}

	const sampleRate = 48000
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			packets := make([][]byte, 0, c.frames)
			for f := 0; f < c.frames; f++ {
				pkt := encodeAPIRateHybridPacketFrameSize(t, c.channels, c.frameSize)
				if toc := ParseTOC(pkt[0]); toc.Mode != ModeHybrid {
					t.Fatalf("encoder produced mode %v, want Hybrid", toc.Mode)
				}
				packets = append(packets, pkt)
			}

			refInt16, err := decodeWithLibopusFixedInt16(sampleRate, c.channels, c.frameSize, packets)
			if err != nil {
				libopustest.HelperUnavailable(t, "fixed reference decode int16", err)
				return
			}
			refInt24, err := decodeWithLibopusFixedInt24(sampleRate, c.channels, c.frameSize, packets)
			if err != nil {
				libopustest.HelperUnavailable(t, "fixed reference decode int24", err)
				return
			}

			dec16, _ := NewDecoder(DefaultDecoderConfig(sampleRate, c.channels))
			dec24, _ := NewDecoder(DefaultDecoderConfig(sampleRate, c.channels))
			var got16, got24 []int32
			for p, pkt := range packets {
				o16 := make([]int16, c.frameSize*c.channels)
				if _, err := dec16.DecodeInt16(pkt, o16); err != nil {
					t.Fatalf("packet %d DecodeInt16: %v", p, err)
				}
				got16 = append(got16, int16ToInt32(o16)...)
				o24 := make([]int32, c.frameSize*c.channels)
				if _, err := dec24.DecodeInt24(pkt, o24); err != nil {
					t.Fatalf("packet %d DecodeInt24: %v", p, err)
				}
				got24 = append(got24, o24...)
			}

			assertFixedExact(t, "int16", got16, int16ToInt32(refInt16))
			assertFixedExact(t, "int24", got24, refInt24)
		})
	}
}

func int16ToInt32(in []int16) []int32 {
	out := make([]int32, len(in))
	for i, v := range in {
		out[i] = int32(v)
	}
	return out
}

func divergence(got, want []int32) (diffs int, maxAbs int64, firstIdx int) {
	firstIdx = -1
	for i := range got {
		d := int64(got[i]) - int64(want[i])
		if d < 0 {
			d = -d
		}
		if d != 0 {
			diffs++
			if firstIdx < 0 {
				firstIdx = i
			}
		}
		if d > maxAbs {
			maxAbs = d
		}
	}
	return
}

// assertFixedExact requires bit-exact equality on every architecture. The
// gopus_fixed_point integer CELT/SILK/Hybrid decode is pure integer arithmetic
// with no fused-multiply-add, so it is deterministic and bit-identical to the
// FIXED_POINT reference on amd64 and darwin/arm64 alike — there is no per-arch
// float drift in this path.
func assertFixedExact(t *testing.T, label string, got, want []int32) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("%s: length got=%d want=%d", label, len(got), len(want))
	}
	diffs, maxAbs, firstIdx := divergence(got, want)
	if diffs == 0 {
		t.Logf("%s: bit-exact (%d samples)", label, len(got))
		return
	}
	t.Errorf("%s: %d/%d samples differ, maxAbs=%d, first at %d: gopus=%d libopus=%d",
		label, diffs, len(got), maxAbs, firstIdx, got[firstIdx], want[firstIdx])
}

func decodeFixedStereoEdge(t *testing.T, sampleRate, channels, frameSize int, steps [][]byte) *Decoder {
	t.Helper()

	refInt16, err := decodeWithLibopusFixedInt16(sampleRate, channels, frameSize, steps)
	if err != nil {
		libopustest.HelperUnavailable(t, "fixed reference decode int16", err)
		return nil
	}
	refInt24, err := decodeWithLibopusFixedInt24(sampleRate, channels, frameSize, steps)
	if err != nil {
		libopustest.HelperUnavailable(t, "fixed reference decode int24", err)
		return nil
	}

	dec16, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatalf("NewDecoder int16: %v", err)
	}
	dec24, err := NewDecoder(DefaultDecoderConfig(sampleRate, channels))
	if err != nil {
		t.Fatalf("NewDecoder int24: %v", err)
	}

	var got16, got24 []int32
	for p, pkt := range steps {
		o16 := make([]int16, frameSize*channels)
		if _, err := dec16.DecodeInt16(pkt, o16); err != nil {
			t.Fatalf("step %d DecodeInt16: %v", p, err)
		}
		got16 = append(got16, int16ToInt32(o16)...)
		o24 := make([]int32, frameSize*channels)
		if _, err := dec24.DecodeInt24(pkt, o24); err != nil {
			t.Fatalf("step %d DecodeInt24: %v", p, err)
		}
		got24 = append(got24, o24...)
	}

	assertFixedExact(t, "int16", got16, int16ToInt32(refInt16))
	assertFixedExact(t, "int24", got24, refInt24)
	return dec16
}

// TestDecoderFixedPointStereoCELTRecoveryParity hardens the STEREO CELT
// packet-loss recovery edge: a multi-frame loss burst followed by SEVERAL good
// received frames so the integer celt_decode_with_ec coarse-energy prediction
// safety block (loss_duration != 0) and the post-filter / decode_mem recovery
// are exercised under the integer path across the full recovery tail (not just
// the first frame after the loss). Bit-exact int16 + int24 vs the FIXED_POINT
// opus_decode / opus_decode24 oracle.
func TestDecoderFixedPointStereoCELTRecoveryParity(t *testing.T) {
	libopustest.RequireOracle(t)

	type tc struct {
		name      string
		frameSize int
		frames    int
		lossAt    map[int]bool
	}
	cases := []tc{
		{"stereo_960_burst_recover", 960, 10, map[int]bool{2: true, 3: true, 4: true}},
		{"stereo_480_burst_recover", 480, 12, map[int]bool{3: true, 4: true, 5: true}},
		{"stereo_240_burst_recover", 240, 12, map[int]bool{4: true, 5: true}},
		{"stereo_960_single_recover", 960, 8, map[int]bool{3: true}},
	}

	const sampleRate = 48000
	const channels = 2
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			encoded := encodeFixedCELTSequence(t, channels, c.frameSize, c.frames)
			steps := make([][]byte, c.frames)
			for i := 0; i < c.frames; i++ {
				if c.lossAt[i] {
					steps[i] = nil
				} else {
					steps[i] = encoded[i]
				}
			}
			decodeFixedStereoEdge(t, sampleRate, channels, c.frameSize, steps)
		})
	}
}

// TestDecoderFixedPointStereoCELTTransitionParity gates the STEREO CELT<->SILK
// (here CELT->Hybrid) transition smooth_fade under the integer path: a CELT
// frame followed by Hybrid frames triggers the integer 5 ms CELT PLC
// pcm_transition decode and the integer opus_res smooth_fade crossfade. The
// stereo transition crossfade interleaves both channels, which the mono gate
// does not cover.
func TestDecoderFixedPointStereoCELTTransitionParity(t *testing.T) {
	libopustest.RequireOracle(t)

	const sampleRate = 48000
	const channels = 2
	for _, frameSize := range []int{960, 480} {
		t.Run("stereo_"+itoaSmall(frameSize), func(t *testing.T) {
			phase := 0.0
			celtPkt := encodeFixedSingleModePacket(t, channels, frameSize, EncoderModeCELT, phase)
			phase += float64(frameSize)
			hyb1 := encodeFixedSingleModePacket(t, channels, frameSize, EncoderModeHybrid, phase)
			phase += float64(frameSize)
			hyb2 := encodeFixedSingleModePacket(t, channels, frameSize, EncoderModeHybrid, phase)
			if toc := ParseTOC(celtPkt[0]); toc.Mode != ModeCELT {
				t.Fatalf("first packet mode %v, want CELT", toc.Mode)
			}
			if toc := ParseTOC(hyb1[0]); toc.Mode != ModeHybrid {
				t.Fatalf("second packet mode %v, want Hybrid", toc.Mode)
			}
			steps := [][]byte{celtPkt, hyb1, hyb2}
			dec := decodeFixedStereoEdge(t, sampleRate, channels, frameSize, steps)
			if dec == nil {
				return
			}
			if dec.fixedTransitionApplied == 0 {
				t.Fatalf("stereo stream did not exercise the integer transition crossfade")
			}
			t.Logf("integer transition applied=%d, redundancy applied=%d",
				dec.fixedTransitionApplied, dec.fixedRedundancyApplied)
		})
	}
}

// TestDecoderFixedPointStereoRedundancyBothDirectionsParity gates STEREO Hybrid
// CELT redundancy in BOTH directions on the integer path:
//   - SILK->CELT redundancy: a Hybrid (SILK lowband) frame followed by a CELT
//     frame carries a redundant CELT frame whose tail is smooth_faded into the
//     SILK-derived output (fixedApplyRedundancySilkToCelt).
//   - CELT->SILK redundancy: a CELT frame followed by a Hybrid frame prefills
//     the Hybrid frame's head from the redundant CELT decode
//     (fixedApplyRedundancyCeltToSilk).
//
// Mode-switch streams (Hybrid<->CELT) on a single encoder produce both. The
// stereo crossfade interleaves both channels. Bit-exact int16 + int24 vs the
// FIXED_POINT oracle.
func TestDecoderFixedPointStereoRedundancyBothDirectionsParity(t *testing.T) {
	libopustest.RequireOracle(t)

	type tc struct {
		name      string
		frameSize int
		modes     []EncoderMode
	}
	cases := []tc{
		{
			name:      "stereo_hybrid_celt_hybrid_960",
			frameSize: 960,
			modes:     []EncoderMode{EncoderModeHybrid, EncoderModeHybrid, EncoderModeCELT, EncoderModeHybrid, EncoderModeHybrid},
		},
		{
			name:      "stereo_alternating_960",
			frameSize: 960,
			modes:     []EncoderMode{EncoderModeHybrid, EncoderModeCELT, EncoderModeHybrid, EncoderModeCELT, EncoderModeHybrid, EncoderModeHybrid},
		},
		{
			name:      "stereo_alternating_480",
			frameSize: 480,
			modes:     []EncoderMode{EncoderModeHybrid, EncoderModeHybrid, EncoderModeCELT, EncoderModeCELT, EncoderModeHybrid, EncoderModeHybrid},
		},
	}

	const sampleRate = 48000
	const channels = 2
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			packets := encodeFixedModeSwitchSequence(t, channels, c.frameSize, c.modes)
			dec := decodeFixedStereoEdge(t, sampleRate, channels, c.frameSize, packets)
			if dec == nil {
				return
			}
			if dec.fixedRedundancyApplied == 0 && dec.fixedTransitionApplied == 0 {
				t.Fatalf("stereo stream did not exercise the integer redundancy/transition path "+
					"(redundancy=%d transition=%d)", dec.fixedRedundancyApplied, dec.fixedTransitionApplied)
			}
			t.Logf("integer redundancy applied=%d, transition applied=%d",
				dec.fixedRedundancyApplied, dec.fixedTransitionApplied)
		})
	}
}

// TestDecoderFixedPointStereoLongPLCChurnParity gates a LONG (100+ frame) STEREO
// CELT stream with periodic packet loss so the integer decoder's cross-frame
// state (decode_mem, per-band energy histories, post-filter taps/gains,
// preemph memory, loss_duration / fade decay) is churned over many frames. Each
// periodic loss + recovery re-enters the coarse-energy prediction safety block
// from a different accumulated state, and any per-frame state divergence between
// the integer path and the FIXED_POINT oracle compounds and surfaces. Bit-exact
// int16 + int24 over the whole sequence.
func TestDecoderFixedPointStereoLongPLCChurnParity(t *testing.T) {
	libopustest.RequireOracle(t)

	type tc struct {
		name      string
		frameSize int
		frames    int
		// lossPeriod marks every lossPeriod-th frame (after warmup) as lost.
		lossPeriod int
		warmup     int
	}
	cases := []tc{
		{"stereo_960_120f_p13", 960, 120, 13, 4},
		{"stereo_480_160f_p11", 480, 160, 11, 4},
	}

	const sampleRate = 48000
	const channels = 2
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			encoded := encodeFixedCELTSequence(t, channels, c.frameSize, c.frames)
			steps := make([][]byte, c.frames)
			losses := 0
			for i := 0; i < c.frames; i++ {
				if i >= c.warmup && (i-c.warmup)%c.lossPeriod == 0 {
					steps[i] = nil
					losses++
				} else {
					steps[i] = encoded[i]
				}
			}
			if losses == 0 {
				t.Fatalf("no losses scheduled")
			}
			t.Logf("%d frames, %d periodic losses", c.frames, losses)
			decodeFixedStereoEdge(t, sampleRate, channels, c.frameSize, steps)
		})
	}
}

// TestDecoderFixedPointSubRateParity gates the integer int16 / int24 decode path
// at the sub-48k API output rates (24/16/12/8 kHz) for CELT, SILK and Hybrid
// streams. The packets are 48 kHz-domain frames; the public DecodeInt16 /
// DecodeInt24 decode them at the configured API rate (the integer CELT decoder
// runs at the core rate and decimates by 48000/Fs). Where the integer path
// declines a sub-48k case (it does for CELT-only loss frames, which decline so
// the float conversion conceals), the output still matches the FIXED_POINT
// oracle because the float fallback is itself FIXED_POINT-derived. Every step
// here is a RECEIVED frame, so the received CELT / Hybrid integer decimation and
// the inherently-integer SILK resampler are all exercised. Bit-exact int16 +
// int24 vs the FIXED_POINT opus_decode / opus_decode24 oracle.
func TestDecoderFixedPointSubRateParity(t *testing.T) {
	libopustest.RequireOracle(t)

	const coreFrame = 960 // 20 ms at 48 kHz
	const frames = 5

	subRates := []int{24000, 16000, 12000, 8000}

	for _, channels := range []int{1, 2} {
		// CELT-only.
		t.Run("celt_ch"+itoaSmall(channels), func(t *testing.T) {
			packets := encodeFixedCELTSequence(t, channels, coreFrame, frames)
			for _, sr := range subRates {
				frameSize, err := packetSamplesAtRate(packets[0], sr)
				if err != nil {
					t.Fatalf("packetSamplesAtRate: %v", err)
				}
				t.Run("fs"+itoaSmall(sr), func(t *testing.T) {
					decodeFixedStereoEdge(t, sr, channels, frameSize, packets)
				})
			}
		})

		// SILK-only (wideband). The SILK decoder is inherently integer and its
		// int16 resampler reproduces the FIXED_POINT lowband at the API rate.
		t.Run("silk_ch"+itoaSmall(channels), func(t *testing.T) {
			packets := encodeFixedSILKSequence(t, channels, coreFrame, frames, BandwidthWideband)
			for _, pkt := range packets {
				if toc := ParseTOC(pkt[0]); toc.Mode != ModeSILK {
					t.Fatalf("encoder produced mode %v, want SILK", toc.Mode)
				}
			}
			for _, sr := range subRates {
				frameSize, err := packetSamplesAtRate(packets[0], sr)
				if err != nil {
					t.Fatalf("packetSamplesAtRate: %v", err)
				}
				t.Run("fs"+itoaSmall(sr), func(t *testing.T) {
					decodeFixedStereoEdge(t, sr, channels, frameSize, packets)
				})
			}
		})

		// Hybrid. The integer SILK lowband + integer CELT highband combine and
		// then decimate to the API rate.
		t.Run("hybrid_ch"+itoaSmall(channels), func(t *testing.T) {
			packets := make([][]byte, 0, frames)
			for f := 0; f < frames; f++ {
				pkt := encodeAPIRateHybridPacketFrameSizeVariant(t, channels, coreFrame, f)
				if toc := ParseTOC(pkt[0]); toc.Mode != ModeHybrid {
					t.Fatalf("encoder produced mode %v, want Hybrid", toc.Mode)
				}
				packets = append(packets, append([]byte(nil), pkt...))
			}
			for _, sr := range subRates {
				frameSize, err := packetSamplesAtRate(packets[0], sr)
				if err != nil {
					t.Fatalf("packetSamplesAtRate: %v", err)
				}
				t.Run("fs"+itoaSmall(sr), func(t *testing.T) {
					decodeFixedStereoEdge(t, sr, channels, frameSize, packets)
				})
			}
		})
	}
}

func TestDecoderFixedPointHybridTransitionWarmZeroAlloc(t *testing.T) {
	for _, channels := range []int{1, 2} {
		celt := encodeFixedSingleModePacket(t, channels, 960, EncoderModeCELT, 0)
		hybrid := encodeFixedSingleModePacket(t, channels, 960, EncoderModeHybrid, 960)
		for _, rate := range []int{8000, 48000} {
			t.Run(fmt.Sprintf("ch%d_%dHz", channels, rate), func(t *testing.T) {
				dec, err := NewDecoder(DefaultDecoderConfig(rate, channels))
				if err != nil {
					t.Fatal(err)
				}
				frameSize := rate / 50
				out := make([]int16, frameSize*channels)
				for i := 0; i < 3; i++ {
					if _, err := dec.DecodeInt16(celt, out); err != nil {
						t.Fatal(err)
					}
					if _, err := dec.DecodeInt16(hybrid, out); err != nil {
						t.Fatal(err)
					}
				}
				var celtN, hybridN int
				allocs := testing.AllocsPerRun(100, func() {
					celtN, err = dec.DecodeInt16(celt, out)
					if err != nil {
						return
					}
					hybridN, err = dec.DecodeInt16(hybrid, out)
				})
				if err != nil || celtN != frameSize || hybridN != frameSize {
					t.Fatalf("transition samples CELT=%d Hybrid=%d want %d: %v", celtN, hybridN, frameSize, err)
				}
				if allocs != 0 {
					t.Fatalf("warm CELT/Hybrid transition allocs=%g want 0", allocs)
				}
				active := false
				for _, sample := range out {
					active = active || sample != 0
				}
				if !active {
					t.Fatal("warm Hybrid output is silent")
				}
			})
		}
	}
}
