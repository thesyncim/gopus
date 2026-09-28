//go:build gopus_osce && !gopus_fixed_point

package gopus

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/dnnblob"
	internalenc "github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/libopustest"
	"github.com/thesyncim/gopus/multistream"
	"github.com/thesyncim/gopus/types"
)

var osceLossSequenceHelper libopustest.HelperCache

func TestOSCEAutomaticLossRecoveryMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	helper, err := cachedLibopusOSCEHelperPath(&osceLossSequenceHelper,
		"libopus_osce_loss_sequence.c", "gopus_osce_loss_sequence", true)
	if err != nil {
		libopustest.HelperUnavailable(t, "OSCE loss sequence", err)
	}
	blob := append([]byte(nil), requireLibopusDecoderNeuralModelBlob(t)...)
	blob = append(blob, requireLibopusOSCELACEModelBlob(t)...)
	model, err := dnnblob.Clone(blob)
	if err != nil {
		t.Fatal(err)
	}
	rates := []int{8000, 12000, 16000, 24000, 48000}
	if extsupport.QEXT {
		rates = append(rates, 96000)
	}
	for _, mode := range []struct {
		name      string
		encode    internalenc.Mode
		decode    Mode
		bandwidth types.Bandwidth
	}{
		{"SILK", internalenc.ModeSILK, ModeSILK, types.BandwidthWideband},
		{"Hybrid", internalenc.ModeHybrid, ModeHybrid, types.BandwidthFullband},
		{"CELT", internalenc.ModeCELT, ModeCELT, types.BandwidthFullband},
	} {
		for _, channels := range []int{1, 2} {
			enc := internalenc.NewEncoder(48000, channels)
			enc.SetMode(mode.encode)
			enc.SetBandwidth(mode.bandwidth)
			enc.SetBitrate(40000 * channels)
			enc.SetForceChannels(channels)
			packets := make([][]byte, 6)
			pcm := make([]float32, 960*channels)
			for frame := range packets {
				for i := range 960 {
					for ch := range channels {
						phase := float64(frame*960+i) / 48000
						pcm[i*channels+ch] = float32(0.28*math.Sin(2*math.Pi*float64(197+66*ch)*phase) +
							0.1*math.Sin(2*math.Pi*389*phase+0.23))
					}
				}
				p, err := enc.Encode(pcm, 960)
				if err != nil {
					t.Fatal(err)
				}
				if len(p) == 0 || ParseTOC(p[0]).Mode != mode.decode {
					t.Fatalf("expected %s packet, got %x", mode.name, p)
				}
				packets[frame] = append([]byte(nil), p...)
			}
			for _, scenario := range []struct {
				name    string
				packets [][]byte
			}{
				{"received", packets},
				{"recovery", [][]byte{packets[0], packets[1], nil, packets[2], packets[3], nil, nil, packets[4], packets[5]}},
			} {
				sequence := scenario.packets
				for _, rate := range rates {
					for _, complexity := range []int{6, 7} {
						for _, ms := range []bool{false, true} {
							t.Run(fmt.Sprintf("%s/%s/ch%d/rate%d/complexity%d/ms%t", mode.name, scenario.name, channels, rate, complexity, ms), func(t *testing.T) {
								frameSize := rate / 50
								var input bytes.Buffer
								input.WriteString("GOLI")
								put := func(v int) { _ = binary.Write(&input, binary.LittleEndian, uint32(v)) }
								put(1)
								put(rate)
								put(channels)
								put(complexity)
								if ms {
									put(1)
								} else {
									put(0)
								}
								put(frameSize)
								put(len(sequence))
								for _, p := range sequence {
									put(len(p))
									input.Write(p)
								}
								wire, err := libopustest.RunHelper(helper, input.Bytes())
								if err != nil {
									t.Fatal(err)
								}
								r, version, err := libopustest.NewOracleReaderMagicVersion("OSCE loss", "GOLO", wire)
								if err != nil {
									t.Fatal(err)
								}
								features, arch, count := r.U32(), r.U32(), r.U32()
								wantFeatures := uint32(2)
								if extsupport.DRED {
									wantFeatures |= 1
								}
								if extsupport.QEXT {
									wantFeatures |= 4
								}
								if version != 1 || features != wantFeatures || count != uint32(len(sequence)) {
									t.Fatalf("oracle header version=%d features=%03b count=%d", version, features, count)
								}
								t.Logf("selected C features=%03b arch=%d", features, arch)
								var decode func([]byte, []float32) (int, error)
								var finalRange func() uint32
								if ms {
									d, err := multistream.NewDecoder(rate, channels, 1, channels-1, []byte{0, 1}[:channels])
									if err != nil {
										t.Fatal(err)
									}
									if err := d.SetComplexity(complexity); err != nil {
										t.Fatal(err)
									}
									d.SetDNNBlob(model)
									decode = func(p []byte, out []float32) (int, error) { return d.DecodeIntoFloat32(p, out, frameSize) }
									finalRange = d.FinalRange
								} else {
									d, err := NewDecoder(DefaultDecoderConfig(rate, channels))
									if err != nil {
										t.Fatal(err)
									}
									if err := d.SetComplexity(complexity); err != nil {
										t.Fatal(err)
									}
									if err := d.SetDNNBlob(blob); err != nil {
										t.Fatal(err)
									}
									decode, finalRange = d.Decode, d.FinalRange
								}
								out := make([]float32, frameSize*channels)
								for step, p := range sequence {
									wantN, wantRange := int(r.U32()), r.U32()
									n, err := decode(p, out)
									if err != nil || n != wantN || finalRange() != wantRange {
										t.Fatalf("step%d count/range=%d/%08x C=%d/%08x err=%v", step, n, finalRange(), wantN, wantRange, err)
									}
									for i, v := range out[:n*channels] {
										want := r.U32()
										if got := math.Float32bits(v); got != want {
											t.Fatalf("step%d sample%d Go=%08x C=%08x", step, i, got, want)
										}
									}
								}
								if err := r.ExpectConsumed(); err != nil {
									t.Fatal(err)
								}
								if allocs := testing.AllocsPerRun(5, func() {
									for _, p := range sequence {
										if _, err := decode(p, out); err != nil {
											panic(err)
										}
									}
								}); allocs != 0 {
									t.Fatalf("warm receive/loss/recovery allocations=%g", allocs)
								}
							})
						}
					}
				}
			}
		}
	}
}
