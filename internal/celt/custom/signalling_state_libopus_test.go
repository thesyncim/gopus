//go:build gopus_custom_modes

package custom_test

import (
	"bytes"
	"encoding/binary"
	"math"
	"testing"

	"github.com/thesyncim/gopus/internal/celt/custom"
	"github.com/thesyncim/gopus/internal/libopustest"
)

type customSignallingStateResult struct {
	status     int
	finalRange uint32
	pcm        []float32
}

var customSignallingStateHelper libopustest.HelperCache

func runCustomSignallingStateOracle(t *testing.T, input []float32) ([]byte, [32]customSignallingStateResult) {
	t.Helper()
	path, err := customSignallingStateHelper.Path(func() (string, error) {
		return libopustest.BuildPublicAPIHelper(libopustest.CHelperConfig{
			Label:       "custom signalling error state",
			OutputBase:  "gopus_libopus_custom_signalling_state",
			SourceFile:  "libopus_custom_signalling_state.c",
			CFlags:      []string{"-DHAVE_CONFIG_H", "-O2"},
			RefIncludes: []string{"celt", "silk", "src", "include"},
			Libs:        []string{"-lm"},
			DeadStrip:   true,
		})
	})
	if err != nil {
		libopustest.HelperUnavailable(t, "custom signalling error state", err)
		return nil, [32]customSignallingStateResult{}
	}
	var request bytes.Buffer
	request.WriteString("GCSL")
	for _, value := range []int{48000, 960, 200, len(input)} {
		writeU32(&request, uint32(value))
	}
	for _, sample := range input {
		writef32(&request, sample)
	}
	response, err := libopustest.RunHelper(path, request.Bytes())
	if err != nil {
		t.Fatalf("custom signalling error-state oracle: %v", err)
	}
	r := bytes.NewReader(response)
	var packetSize int32
	if err := binary.Read(r, binary.LittleEndian, &packetSize); err != nil || packetSize < 2 || packetSize > 200 {
		t.Fatalf("custom signalling packet size=%d err=%v", packetSize, err)
	}
	packet := make([]byte, int(packetSize))
	if _, err := r.Read(packet); err != nil {
		t.Fatalf("custom signalling packet: %v", err)
	}
	var results [32]customSignallingStateResult
	for i := range results {
		result := &results[i]
		var status int32
		for _, value := range []any{&status, &result.finalRange} {
			if err := binary.Read(r, binary.LittleEndian, value); err != nil {
				t.Fatalf("custom signalling result %d header: %v", i, err)
			}
		}
		result.status = int(status)
		if status > 960 || status < -7 {
			t.Fatalf("custom signalling result %d has status %d", i, status)
		}
		if status > 0 {
			result.pcm = make([]float32, int(status))
			for j := range result.pcm {
				var bits uint32
				if err := binary.Read(r, binary.LittleEndian, &bits); err != nil {
					t.Fatalf("custom signalling result %d PCM[%d]: %v", i, j, err)
				}
				result.pcm[j] = math.Float32frombits(bits)
			}
		}
	}
	if r.Len() != 0 {
		t.Fatalf("custom signalling error-state oracle has %d trailing bytes", r.Len())
	}
	return packet, results
}

func assertCustomSignallingStateResult(t *testing.T, label string, got []float32, gotRange uint32, want customSignallingStateResult) {
	t.Helper()
	if want.status <= 0 {
		t.Fatalf("%s C oracle status %d is not a decoded frame", label, want.status)
	}
	if len(got) != len(want.pcm) || gotRange != want.finalRange {
		t.Fatalf("%s samples/range=%d/%08x want=%d/%08x", label, len(got), gotRange, len(want.pcm), want.finalRange)
	}
	assertCustomDecodeExact(t, label, got, want.pcm)
}

func TestCustomSignallingEndBandCommitsBeforePacketErrors(t *testing.T) {
	input := make([]float32, 960)
	for i := range input {
		input[i] = float32(.21*math.Sin(2*math.Pi*531*float64(i)/48000) + .07*math.Sin(2*math.Pi*1433*float64(i)/48000))
	}
	packet, oracle := runCustomSignallingStateOracle(t, input)
	mode, err := custom.NewMode(48000, 960)
	if err != nil {
		t.Fatal(err)
	}

	for scenarioIndex, scenario := range []struct {
		name      string
		badPacket func([]byte) []byte
		capacity  int
		wantErr   error
		cStatus   int
	}{
		{
			name: "valid TOC with short output capacity",
			badPacket: func(packet []byte) []byte {
				packet[0] = 0xd8 // commits end band 19 before returning OPUS_BUFFER_TOO_SMALL
				return packet
			},
			capacity: 959, wantErr: custom.ErrInvalidFrameSize, cStatus: -2,
		},
		{
			name:      "valid TOC with malformed code-3 framing",
			badPacket: func([]byte) []byte { return []byte{0xdb} },
			capacity:  960, wantErr: custom.ErrInvalidPacket, cStatus: -4,
		},
		{
			name: "valid TOC with zero output capacity",
			badPacket: func(packet []byte) []byte {
				packet[0] = 0xd8
				return packet
			},
			capacity: 0, wantErr: custom.ErrInvalidFrameSize, cStatus: -2,
		},
		{
			name:      "invalid TOC with zero output capacity",
			badPacket: func(packet []byte) []byte { packet[0] = 0x7f; return packet },
			capacity:  0, wantErr: custom.ErrInvalidPacket, cStatus: -4,
		},
	} {
		oracleBase := scenarioIndex * 8
		t.Run(scenario.name, func(t *testing.T) {
			dec, err := custom.NewDecoder(mode, 1)
			if err != nil {
				t.Fatal(err)
			}
			got, err := dec.DecodeFloat(packet, 960)
			if err != nil {
				t.Fatalf("seed DecodeFloat: %v", err)
			}
			assertCustomSignallingStateResult(t, "seed", got, dec.FinalRange(), oracle[oracleBase])

			badPacket := scenario.badPacket(append([]byte(nil), packet...))
			if got := oracle[oracleBase+1].status; got != scenario.cStatus {
				t.Fatalf("C rejected-packet status=%d want=%d", got, scenario.cStatus)
			}
			if _, err := dec.DecodeFloat(badPacket, scenario.capacity); err != scenario.wantErr {
				t.Fatalf("bad packet error=%v want %v", err, scenario.wantErr)
			}
			if got, want := dec.FinalRange(), oracle[oracleBase+1].finalRange; got != want {
				t.Fatalf("range after rejected packet=%08x want=%08x", got, want)
			}
			for loss := 0; loss < 6; loss++ {
				got, err = dec.DecodeFloat(nil, 960)
				if err != nil {
					t.Fatalf("loss %d DecodeFloat: %v", loss, err)
				}
				assertCustomSignallingStateResult(t, "loss", got, dec.FinalRange(), oracle[oracleBase+2+loss])
			}
		})
	}
}
