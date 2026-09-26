package multistream

import (
	"bytes"
	"errors"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

type encodeBudgetParityCase struct {
	name          string
	channels      int
	frameSize     int
	bitrate       int
	vbr           bool
	vbrConstraint bool
	maxPacket     int
	projection    bool
}

// TestMultistreamEncodeBudgetMatchesLibopus checks packet bytes and the encoder
// final range against the paired live C multistream/projection encoder. The
// cases exercise caller capacity, CVBR bursts, and CBR framing across stream
// counts and packet frame layouts.
func TestMultistreamEncodeBudgetMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)

	cases := []encodeBudgetParityCase{
		{name: "mono_cvbr_20ms_capacity4000", channels: 1, frameSize: 960, bitrate: 384000, vbr: true, vbrConstraint: true, maxPacket: 4000},
		{name: "mono_cvbr_20ms_capacity1000", channels: 1, frameSize: 960, bitrate: 384000, vbr: true, vbrConstraint: true, maxPacket: 1000},
		{name: "stereo_cvbr_20ms_capacity4000", channels: 2, frameSize: 960, bitrate: 384000, vbr: true, vbrConstraint: true, maxPacket: 4000},
		{name: "stereo_vbr_20ms_capacity1600", channels: 2, frameSize: 960, bitrate: 256000, vbr: true, vbrConstraint: false, maxPacket: 1600},
		{name: "surround51_cvbr_20ms_capacity4000", channels: 6, frameSize: 960, bitrate: 384000, vbr: true, vbrConstraint: true, maxPacket: 4000},
		{name: "surround51_cvbr_20ms_capacity1800", channels: 6, frameSize: 960, bitrate: 384000, vbr: true, vbrConstraint: true, maxPacket: 1800},
		{name: "surround51_cbr_40ms", channels: 6, frameSize: 1920, bitrate: 384000, vbr: false, maxPacket: 4000},
		{name: "surround71_cbr_60ms", channels: 8, frameSize: 2880, bitrate: 384000, vbr: false, maxPacket: 4000},
		{name: "surround51_cbr_100ms", channels: 6, frameSize: 4800, bitrate: 128000, vbr: false, maxPacket: 4000},
		{name: "surround51_cbr_120ms", channels: 6, frameSize: 5760, bitrate: 128000, vbr: false, maxPacket: 4000},
		{name: "projection_cvbr_20ms_capacity4000", channels: 4, frameSize: 960, bitrate: 384000, vbr: true, vbrConstraint: true, maxPacket: 4000, projection: true},
		{name: "projection_cbr_40ms", channels: 4, frameSize: 1920, bitrate: 384000, vbr: false, maxPacket: 4000, projection: true},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			const frameCount = 3
			pcm := generateSurroundSweep(tc.channels, tc.frameSize, frameCount)
			var refPackets [][]byte
			var refRanges []uint32
			var refStreams, refCoupled int
			var err error
			if tc.projection {
				pcm = generateAmbisonicsSweep(tc.channels, tc.frameSize, frameCount)
				ref, refErr := encodeLibopusProjection(48000, tc.channels, 2049, tc.bitrate, tc.vbr, tc.vbrConstraint,
					10, -1000, tc.frameSize, frameCount, tc.maxPacket, 0, pcm, nil)
				err = refErr
				if err == nil {
					refPackets, refRanges = ref.packets, ref.ranges
					refStreams, refCoupled = ref.streams, ref.coupledStreams
				}
			} else {
				ref, refErr := encodeLibopusSurround(48000, tc.channels, 1, 2049, tc.bitrate, tc.vbr, tc.vbrConstraint,
					10, -1000, tc.frameSize, frameCount, tc.maxPacket, pcm)
				err = refErr
				if err == nil {
					refPackets, refRanges = ref.packets, ref.ranges
					refStreams, refCoupled = ref.streams, ref.coupledStreams
				}
			}
			if err != nil {
				libopustest.HelperUnavailable(t, "multistream encode budget reference", err)
				return
			}
			if len(refPackets) != frameCount || len(refRanges) != frameCount {
				t.Fatalf("oracle records packets=%d ranges=%d want=%d", len(refPackets), len(refRanges), frameCount)
			}

			var enc *Encoder
			if tc.projection {
				enc, err = NewProjectionEncoder(48000, tc.channels)
			} else {
				enc, err = NewEncoderDefault(48000, tc.channels)
			}
			if err != nil {
				t.Fatalf("create encoder: %v", err)
			}
			if enc.Streams() != refStreams || enc.CoupledStreams() != refCoupled {
				t.Fatalf("stream layout got=(%d,%d) want=(%d,%d)", enc.Streams(), enc.CoupledStreams(), refStreams, refCoupled)
			}
			enc.SetBitrate(tc.bitrate)
			enc.SetVBR(tc.vbr)
			enc.SetVBRConstraint(tc.vbrConstraint)
			enc.SetComplexity(10)
			enc.SetBandwidthAuto()

			for frame := range frameCount {
				start := frame * tc.frameSize * tc.channels
				input := pcm[start : start+tc.frameSize*tc.channels]
				got, encodeErr := enc.EncodeFloat32WithAnalysisMaxBytes(input, tc.frameSize, input, tc.maxPacket)
				if encodeErr != nil {
					t.Fatalf("frame %d encode: %v", frame, encodeErr)
				}
				want := refPackets[frame]
				if !bytes.Equal(got, want) {
					first := firstByteMismatch(got, want)
					t.Errorf("frame %d bytes differ: len Go=%d C=%d firstByte=%d GoPrefix=%x CPrefix=%x GoTail=%x CTail=%x",
						frame, len(got), len(want), first, got[:min(len(got), 12)], want[:min(len(want), 12)],
						got[max(0, len(got)-8):], want[max(0, len(want)-8):])
					goStreams, goErr := parseMultistreamPacket(got, enc.Streams())
					cStreams, cErr := parseMultistreamPacket(want, enc.Streams())
					if goErr == nil && cErr == nil {
						for stream := range goStreams {
							if !tc.vbr && frame == 0 {
								t.Logf("stream %d raw=%d sd=%d C=%d", stream, len(enc.streamPacketsScratch[stream]), len(goStreams[stream]), len(cStreams[stream]))
							}
							if !bytes.Equal(goStreams[stream], cStreams[stream]) {
								child := enc.encoders[stream]
								t.Errorf("frame %d stream %d differs: Go=%d C=%d firstByte=%d bitrate=%d target=%d mode=%d childRange=%08x GoTail=%x CTail=%x", frame, stream,
									len(goStreams[stream]), len(cStreams[stream]), firstByteMismatch(goStreams[stream], cStreams[stream]),
									child.Bitrate(), (bitrateToBits(child.Bitrate(), 48000, tc.frameSize)+4)/8, child.GetBitrateMode(), child.FinalRange(),
									goStreams[stream][max(0, len(goStreams[stream])-16):], cStreams[stream][max(0, len(cStreams[stream])-16):])
							}
						}
					}
				}
				gotRange := enc.GetFinalRange()
				if gotRange != refRanges[frame] {
					t.Errorf("frame %d final range got=%08x C=%08x", frame, gotRange, refRanges[frame])
				}
			}
		})
	}
}

func TestMultistreamEncodeTooSmallPreservesState(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		channels  = 6
		frameSize = 960
	)
	pcm := generateSurroundSweep(channels, frameSize, 1)
	ref, err := encodeLibopusSurround(48000, channels, 1, 2049, 384000, true, true,
		10, -1000, frameSize, 1, 4000, pcm)
	if err != nil {
		libopustest.HelperUnavailable(t, "multistream undersized-budget reference", err)
		return
	}
	if len(ref.packets) != 1 || len(ref.ranges) != 1 {
		t.Fatalf("oracle records packets=%d ranges=%d want=1", len(ref.packets), len(ref.ranges))
	}
	enc, err := NewEncoderDefault(48000, channels)
	if err != nil {
		t.Fatal(err)
	}
	enc.SetBitrate(384000)
	enc.SetVBR(true)
	enc.SetVBRConstraint(true)
	for _, capacity := range []int{0, 1, 6} {
		packet, encodeErr := enc.EncodeFloat32WithAnalysisMaxBytes(pcm, frameSize, pcm, capacity)
		if !errors.Is(encodeErr, ErrBufferTooSmall) || packet != nil {
			t.Fatalf("capacity=%d packet=%x error=%v want buffer-too-small", capacity, packet, encodeErr)
		}
	}
	got, err := enc.EncodeFloat32WithAnalysisMaxBytes(pcm, frameSize, pcm, 4000)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(got, ref.packets[0]) || enc.GetFinalRange() != ref.ranges[0] {
		t.Fatalf("encode after rejected budgets: Go len=%d C len=%d firstByte=%d GoRange=%08x CRange=%08x",
			len(got), len(ref.packets[0]), firstByteMismatch(got, ref.packets[0]), enc.GetFinalRange(), ref.ranges[0])
	}
}

func TestMultistreamSelfDelimitedBudgetFramingWarmZeroAllocs(t *testing.T) {
	const (
		channels  = 6
		frameSize = 1920
	)
	enc, err := NewEncoderDefault(48000, channels)
	if err != nil {
		t.Fatal(err)
	}
	enc.SetBitrate(384000)
	enc.SetVBR(false)
	pcm := generateSurroundSweep(channels, frameSize, 1)
	if _, err := enc.EncodeFloat32WithAnalysisMaxBytes(pcm, frameSize, pcm, 4000); err != nil {
		t.Fatal(err)
	}
	raw := append([]byte(nil), enc.streamPacketsScratch[0]...)
	dst := make([]byte, len(raw)+2)
	var scratch packetScratch
	wantLen, err := makeSelfDelimitedPacketInto(&scratch, dst, raw)
	if err != nil {
		t.Fatal(err)
	}
	if wantLen >= len(raw)+2 {
		t.Fatalf("expected ordinary CBR child padding to shrink: raw=%d framed=%d", len(raw), wantLen)
	}
	allocs := testing.AllocsPerRun(100, func() {
		n, frameErr := makeSelfDelimitedPacketInto(&scratch, dst, raw)
		if frameErr != nil || n != wantLen {
			t.Fatalf("framing len=%d error=%v want=%d", n, frameErr, wantLen)
		}
	})
	if allocs != 0 {
		t.Fatalf("warm self-delimited framing allocs/run=%v want 0", allocs)
	}
}
