//go:build gopus_fixed_point

package silk

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/internal/opusmath"
)

var fixedSILKAPIHelper libopustest.HelperCache

// assertFixedSILKAPI compares the complete packets and coder state produced by
// PacketEncoder and the linked silk_Encode API. Both consume the same signed16
// input lattice, controls, packet history and reset points.
func assertFixedSILKAPI(t *testing.T, p *testPacketEncoder, frames [][]float32, resetAt int) {
	t.Helper()
	helper, err := fixedSILKAPIHelper.CHelperPath(libopustest.CHelperConfig{
		Label: "fixed SILK API", OutputBase: "gopus_silk_fixed_api", SourceFile: "libopus_silk_fixed_api_info.c",
		FixedRef: true, CFlags: []string{"-DHAVE_CONFIG_H", "-O3"}, RefIncludes: []string{"celt", "silk", "silk/fixed"},
		Libs: []string{libopustest.FixedRefPath(".libs", "libopus.a"), "-lm"},
	})
	if err != nil {
		t.Fatal(err)
	}
	c := p.ctl
	original := newTestPacketEncoderAt(int(c.APISampleRate), p.enc.state[0].bandwidth, int(c.NChannelsAPI))
	original.ctl = c
	channels := int(c.NChannelsAPI)
	frameSamples := len(frames[0]) / channels
	ms := 1000 * frameSamples / int(c.APISampleRate)
	payload := libopustest.NewOraclePayload("GSAI", uint32(c.APISampleRate), uint32(channels), uint32(ms), uint32(c.BitRate), uint32(boolToI32(c.UseCBR)), uint32(c.MaxBits), uint32(c.Complexity), uint32(len(frames)))
	inputs := make([][]float32, len(frames))
	for f, frame := range frames {
		if len(frame) != frameSamples*channels {
			t.Fatal("inconsistent frame length")
		}
		payload.U32(uint32(boolToI32(f == resetAt)))
		inputs[f] = make([]float32, len(frame))
		for i, sample := range frame {
			q := opusmath.Float32ToInt16(sample)
			payload.I16(q)
			inputs[f][i] = float32(q) * (1.0 / 32768.0)
		}
	}
	reader, err := libopustest.RunOracle(helper, payload.Bytes(), "fixed SILK API", "GSAO")
	if err != nil {
		t.Fatal(err)
	}
	if n := reader.Count(len(frames)); n != len(frames) {
		t.Fatalf("C packet count=%d want%d", n, len(frames))
	}
	t.Logf("selected C arch=%d", reader.U32())
	for f, frame := range inputs {
		n := reader.U32()
		rng, tell := reader.U32(), reader.U32()
		if n == 0 || n > maxSilkPacketBytes {
			t.Fatalf("C packet%d size=%d", f, n)
		}
		want := reader.Bytes(int(n))
		if f == resetAt {
			p.enc.Init()
			original.enc.Init()
		}
		got := p.encodeInto(t, frame, 1)
		unquantized := original.encodeInto(t, frames[f], 1)
		if len(got) == 0 || !bytes.Equal(got, unquantized) || p.re.Range() != original.re.Range() {
			t.Fatalf("packet%d signed16 input transport changes the original Go encoding", f)
		}
		if !bytes.Equal(got, want) || p.re.Range() != rng || uint32(p.re.Tell()) != tell {
			t.Fatalf("packet%d bytes=%d/%d range=%08x/%08x tell=%d/%d\nGo %x\nC  %x", f, len(got), len(want), p.re.Range(), rng, p.re.Tell(), tell, got, want)
		}
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
}

func TestPublicSILKFixedStatefulComplexityParity(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, complexity := range []int32{0, 1, 2, 3, 5, 10} {
		for _, channels := range []int{1, 2} {
			t.Run(fmt.Sprintf("cx%d_ch%d", complexity, channels), func(t *testing.T) {
				p := newTestPacketEncoder(BandwidthWideband, channels)
				p.ctl.Complexity = complexity
				p.ctl.BitRate = int32(18000 * channels)
				p.ctl.MaxBits = p.ctl.BitRate / 50
				pcm := speechLikeSignal(16000, 320*6, channels)
				frames := make([][]float32, 6)
				for f := range frames {
					frames[f] = pcm[f*320*channels : (f+1)*320*channels]
				}
				assertFixedSILKAPI(t, p, frames, 3)
			})
		}
	}
}
