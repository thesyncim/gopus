//go:build gopus_qext

package gopus

import (
	"bytes"
	"fmt"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

const (
	rootHD96kInputMagic  = "G96M"
	rootHD96kOutputMagic = "G96O"
)

var rootHD96kModeHelper libopustest.HelperCache

type rootHD96kRecord struct {
	packet  []byte
	range32 uint32
}

func buildRootHD96kModeHelper() (string, error) {
	return libopustest.BuildPublicAPIHelper(libopustest.CHelperConfig{
		Label:       "native 96 kHz encoder mode sequence",
		OutputBase:  "gopus_libopus_encoder_96k_mode_sequence",
		SourceFile:  "libopus_encoder_96k_mode_sequence.c",
		CFlags:      []string{"-DHAVE_CONFIG_H", "-O3", "-DNDEBUG"},
		RefIncludes: []string{"src", "celt", "silk", "silk/float"},
		DeadStrip:   true,
	})
}

func encodeRootHD96kWithLibopus(t *testing.T, qext bool, budgets []int, frames [][]float32) []rootHD96kRecord {
	t.Helper()
	if len(budgets) != len(frames) {
		t.Fatalf("oracle input has %d budgets and %d frames", len(budgets), len(frames))
	}
	if len(frames) == 0 {
		t.Fatal("oracle input has no frames")
	}
	frameSize := len(frames[0])
	qextFlag := uint32(0)
	if qext {
		qextFlag = 1
	}
	payload := libopustest.NewOraclePayloadVersion(rootHD96kInputMagic, 1,
		uint32(frameSize), uint32(len(frames)), qextFlag, 10, 24)
	for i, frame := range frames {
		if len(frame) != frameSize {
			t.Fatalf("frame %d has %d samples, want %d", i, len(frame), frameSize)
		}
		payload.U32(uint32(budgets[i]))
		payload.U32(256000)
		payload.Float32s(frame...)
	}
	bin, err := rootHD96kModeHelper.Path(buildRootHD96kModeHelper)
	if err != nil {
		libopustest.HelperUnavailable(t, "native 96 kHz mode sequence", err)
		return nil
	}
	reader, err := libopustest.RunOracleVersion(bin, payload.Bytes(), "native 96 kHz mode sequence", rootHD96kOutputMagic, 1)
	if err != nil {
		t.Fatalf("selected libopus encoder: %v", err)
	}
	count := reader.Count(len(frames))
	if count != len(frames) {
		t.Fatalf("selected C returned %d frames, want %d: %v", count, len(frames), reader.Err())
	}
	records := make([]rootHD96kRecord, count)
	for i := range records {
		n := int(reader.U32())
		records[i].range32 = reader.U32()
		if n < 1 || n > budgets[i] {
			t.Fatalf("selected C packet %d has length %d for budget %d", i, n, budgets[i])
		}
		records[i].packet = append([]byte(nil), reader.Bytes(n)...)
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
	return records
}

func TestRootNative96kModeBudgetSequenceMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const packetCapacity = 3825
	for _, frameSize := range []int{1920, 3840} {
		for _, qext := range []bool{false, true} {
			for _, prime := range []bool{false, true} {
				for _, lowBudget := range []int{16, 32} {
					name := fmt.Sprintf("frame%d/qext%t/primed%t/budget%d", frameSize, qext, prime, lowBudget)
					t.Run(name, func(t *testing.T) {
						budgets := []int{lowBudget, packetCapacity}
						if prime {
							budgets = append([]int{packetCapacity}, budgets...)
						}
						frames := makeRootHD96kPCM(frameSize, len(budgets))
						want := encodeRootHD96kWithLibopus(t, qext, budgets, frames)
						lowBudgetFrame := 0
						if prime {
							lowBudgetFrame = 1
						}
						if frameSize == 1920 && want[lowBudgetFrame].packet[0] != 0x48 {
							t.Fatalf("selected C low-budget frame TOC=%02x, want native 96 kHz SILK WB TOC 48", want[lowBudgetFrame].packet[0])
						}
						enc, err := NewEncoder(EncoderConfig{SampleRate: 96000, Channels: 1, Application: ApplicationAudio})
						if err != nil {
							t.Fatal(err)
						}
						if err := enc.SetBitrate(256000); err != nil {
							t.Fatal(err)
						}
						if err := enc.SetComplexity(10); err != nil {
							t.Fatal(err)
						}
						enc.SetVBR(true)
						enc.SetVBRConstraint(true)
						if err := enc.SetQEXT(qext); err != nil {
							t.Fatal(err)
						}
						if enc.FrameSize() != frameSize {
							if err := enc.SetFrameSize(frameSize); err != nil {
								t.Fatalf("SetFrameSize(%d): %v", frameSize, err)
							}
						}
						out := make([]byte, packetCapacity)
						for step, budget := range budgets {
							if err := enc.SetBitrate(256000); err != nil {
								t.Fatal(err)
							}
							n, err := enc.Encode(frames[step], out[:budget])
							if err != nil {
								t.Fatalf("step%d encode: %v", step, err)
							}
							if !bytes.Equal(out[:n], want[step].packet) {
								t.Fatalf("step%d budget%d packet Go=%x C=%x", step, budget, out[:n], want[step].packet)
							}
							if got := enc.FinalRange(); got != want[step].range32 {
								t.Fatalf("step%d range Go=%08x C=%08x", step, got, want[step].range32)
							}
						}
						encodeWarm := func() {
							for step, budget := range budgets {
								if _, err := enc.Encode(frames[step], out[:budget]); err != nil {
									panic(err)
								}
							}
						}
						if allocs := testing.AllocsPerRun(10, encodeWarm); allocs != 0 {
							t.Fatalf("warm allocations=%g", allocs)
						}
					})
				}
			}
		}
	}
}

func makeRootHD96kPCM(frameSize, frames int) [][]float32 {
	pcm := make([][]float32, frames)
	for frame := range frames {
		pcm[frame] = make([]float32, frameSize)
		for i := range frameSize {
			n := frame*frameSize + i
			x := 0.36*math.Sin(2*math.Pi*5300*float64(n)/96000) +
				0.16*math.Sin(2*math.Pi*11900*float64(n)/96000) +
				0.21*math.Sin(2*math.Pi*30000*float64(n)/96000)
			pcm[frame][i] = float32(int16(math.Round(x*32767))) / 32768
		}
	}
	return pcm
}
