package cgo

import (
	"testing"

	"github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/rangecoding"
	"github.com/thesyncim/gopus/internal/silk"
)

// Temporary: compare postproc (sMid+resampler) for MB/WB against libopus.
func TestTmpSilkPostprocMBWB(t *testing.T) {
	vectors := []string{"testvector03", "testvector04"}

	for _, name := range vectors {
		t.Run(name, func(t *testing.T) {
			bitFile := "../../../internal/testvectors/testdata/opus_testvectors/" + name + ".bit"
			packets, err := loadPacketsSimple(bitFile, 3)
			if err != nil || len(packets) == 0 {
				t.Skip("Could not load packets")
			}

			goDec := silk.NewDecoder()
			var goResampler *silk.LibopusResampler
			var cPostproc *SilkPostprocState
			var sMidGo [2]float32
			var lastFsKHz int

			for pktIdx := 0; pktIdx < len(packets); pktIdx++ {
				pkt := packets[pktIdx]
				toc := gopus.ParseTOC(pkt[0])
				if toc.Mode != gopus.ModeSILK {
					continue
				}
				silkBW, ok := silk.BandwidthFromOpus(int(toc.Bandwidth))
				if !ok {
					continue
				}
				duration := silk.FrameDurationFromTOC(toc.FrameSize)
				framesPerPacket, nbSubfr := frameParamsForDuration(duration)
				if framesPerPacket == 0 {
					t.Fatalf("unexpected duration: %v", duration)
				}
				fsKHz := silk.GetBandwidthConfig(silkBW).SampleRate / 1000
				frameLength := nbSubfr * 5 * fsKHz

				var rd rangecoding.Decoder
				rd.Init(pkt[1:])
				goNative, err := goDec.DecodeFrame(&rd, silkBW, duration, true)
				if err != nil {
					t.Fatalf("gopus DecodeFrame failed: %v", err)
				}

				// (Re)init postproc for rate changes
				if goResampler == nil || fsKHz != lastFsKHz {
					goResampler = silk.NewLibopusResampler(fsKHz*1000, 48000)
					if cPostproc != nil {
						cPostproc.Free()
					}
					cPostproc = NewSilkPostprocState(fsKHz, 48000)
					if cPostproc == nil {
						t.Fatalf("failed to create libopus postproc state")
					}
					sMidGo = [2]float32{}
					lastFsKHz = fsKHz
				}

				for f := 0; f < framesPerPacket; f++ {
					start := f * frameLength
					end := start + frameLength
					if end > len(goNative) {
						t.Fatalf("frame slice out of range: %d..%d of %d", start, end, len(goNative))
					}
					frame := goNative[start:end]

					// Go-side postproc
					resIn := make([]float32, frameLength)
					resIn[0] = sMidGo[1]
					if frameLength > 1 {
						copy(resIn[1:], frame[:frameLength-1])
						sMidGo[0] = frame[frameLength-2]
						sMidGo[1] = frame[frameLength-1]
					} else {
						sMidGo[0] = sMidGo[1]
						sMidGo[1] = frame[0]
					}
					goOut := goResampler.Process(resIn)

					// Libopus postproc
					frameInt := floatToInt16Slice(frame)
					refOut, n := cPostproc.ProcessFrame(frameInt)
					if n <= 0 {
						t.Fatalf("libopus postproc failed")
					}
					if len(goOut) != len(refOut) {
						t.Fatalf("postproc length mismatch pkt=%d frame=%d go=%d ref=%d", pktIdx, f, len(goOut), len(refOut))
					}

					for i := 0; i < len(goOut); i++ {
						ref := float32(refOut[i]) / 32768.0
						diff := goOut[i] - ref
						if diff < -1e-6 || diff > 1e-6 {
							t.Fatalf("postproc mismatch pkt=%d frame=%d sample=%d go=%.8f ref=%.8f", pktIdx, f, i, goOut[i], ref)
						}
					}
				}
			}
			if cPostproc != nil {
				cPostproc.Free()
			}
		})
	}
}
