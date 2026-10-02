package gopus

import (
	"bytes"
	"fmt"
	"sync"
	"testing"

	"github.com/thesyncim/gopus/internal/extsupport"
	"github.com/thesyncim/gopus/internal/libopustest"
)

const (
	encodeAPIErrorFloat32 = iota
	encodeAPIErrorInt16
	encodeAPIErrorInt24
)

type encodeAPIErrorRecord struct {
	ret        int32
	finalRange uint32
	packet     []byte
}

var encodeAPIErrorOracle struct {
	sync.Once
	path string
	err  error
}

func encoderAPIErrorOraclePath() (string, error) {
	encodeAPIErrorOracle.Do(func() {
		encodeAPIErrorOracle.path, encodeAPIErrorOracle.err = libopustest.BuildPublicAPIHelper(libopustest.CHelperConfig{
			Label:       "public encode API error state",
			OutputBase:  "gopus_libopus_encode_api_error_state",
			SourceFile:  "libopus_encode_api_error_state.c",
			CFlags:      []string{"-DHAVE_CONFIG_H", "-O2", "-DNDEBUG"},
			RefIncludes: []string{"celt", "silk", "src"},
			DeadStrip:   true,
		})
	})
	return encodeAPIErrorOracle.path, encodeAPIErrorOracle.err
}

func TestPublicEncodeAPIErrorFinalRangeAndRecoveryMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	helper, err := encoderAPIErrorOraclePath()
	if err != nil {
		libopustest.HelperUnavailable(t, "public encode API error state", err)
		return
	}
	rates := []int{48000}
	if extsupport.QEXT {
		rates = append(rates, 96000)
	}
	apis := []struct {
		name string
		kind int
	}{
		{name: "float32", kind: encodeAPIErrorFloat32},
		{name: "int16", kind: encodeAPIErrorInt16},
		{name: "int24", kind: encodeAPIErrorInt24},
	}
	for _, rate := range rates {
		for _, api := range apis {
			t.Run(fmt.Sprintf("%dHz/%s", rate, api.name), func(t *testing.T) {
				enc, err := NewEncoder(EncoderConfig{
					SampleRate:  rate,
					Channels:    1,
					Application: ApplicationAudio,
				})
				if err != nil {
					t.Fatalf("NewEncoder: %v", err)
				}
				if err := enc.SetBitrate(64000); err != nil {
					t.Fatalf("SetBitrate: %v", err)
				}
				frameSize := enc.FrameSize()
				pcmFloat, pcmInt16, pcmInt24 := encodeAPIErrorPCM(frameSize)
				want, err := probeEncodeAPIErrorState(helper, rate, 1, api.kind, frameSize)
				if err != nil {
					t.Fatalf("libopus oracle: %v", err)
				}
				if len(want) != 7 {
					t.Fatalf("oracle returned %d records, want 7", len(want))
				}
				if want[0].ret <= 0 || want[0].finalRange == 0 ||
					want[1].ret >= 0 || want[2].ret <= 0 || want[2].finalRange == 0 ||
					want[3].ret >= 0 || want[4].ret <= 0 || want[4].finalRange == 0 ||
					want[5].ret >= 0 || want[6].ret <= 0 || want[6].finalRange == 0 {
					t.Fatalf("oracle did not exercise encode/error/recovery: ret/ranges=%+v", want)
				}

				steps := []struct {
					name     string
					duration ExpertFrameDuration
					budget   int
					wantErr  error
				}{
					{name: "prime", duration: ExpertFrameDurationArg, budget: 4000},
					{name: "invalid expert frame", duration: ExpertFrameDuration120Ms, budget: 4000, wantErr: ErrInvalidFrameSize},
					{name: "recovery after invalid expert frame", duration: ExpertFrameDurationArg, budget: 4000},
					{name: "zero output budget", duration: ExpertFrameDurationArg, budget: 0, wantErr: ErrBufferTooSmall},
					{name: "recovery after zero budget", duration: ExpertFrameDurationArg, budget: 4000},
					{name: "invalid frame and zero budget", duration: ExpertFrameDuration120Ms, budget: 0, wantErr: ErrInvalidFrameSize},
					{name: "recovery after both errors", duration: ExpertFrameDurationArg, budget: 4000},
				}
				for i, step := range steps {
					if err := enc.SetExpertFrameDuration(step.duration); err != nil {
						t.Fatalf("%s: SetExpertFrameDuration: %v", step.name, err)
					}
					buffer := make([]byte, step.budget)
					n, err := encodeAPIErrorCall(enc, api.kind, pcmFloat, pcmInt16, pcmInt24, buffer)
					if err != step.wantErr {
						t.Fatalf("%s: error=%v want %v", step.name, err, step.wantErr)
					}
					if step.wantErr != nil && n != 0 {
						t.Fatalf("%s: wrote %d bytes on error, want 0", step.name, n)
					}
					ret := int32(n)
					if err != nil {
						ret = -1
					}
					var packet []byte
					if n > 0 {
						packet = buffer[:n]
					}
					got := encodeAPIErrorRecord{ret: ret, finalRange: enc.FinalRange(), packet: packet}
					if got.ret != want[i].ret || got.finalRange != want[i].finalRange || !bytes.Equal(got.packet, want[i].packet) {
						t.Fatalf("%s: Go ret=%d range=%08x packet=%x; libopus ret=%d range=%08x packet=%x", step.name, got.ret, got.finalRange, got.packet, want[i].ret, want[i].finalRange, want[i].packet)
					}
				}
				priorRange := enc.FinalRange()
				_, shortErr := encodeAPIErrorCall(enc, api.kind, pcmFloat[:len(pcmFloat)-1], pcmInt16[:len(pcmInt16)-1], pcmInt24[:len(pcmInt24)-1], make([]byte, 4000))
				if shortErr != ErrInvalidFrameSize {
					t.Fatalf("short PCM slice error=%v, want %v", shortErr, ErrInvalidFrameSize)
				}
				if got := enc.FinalRange(); got != priorRange {
					t.Fatalf("short PCM slice changed final range: got=%08x want=%08x", got, priorRange)
				}
			})
		}
	}
}

func encodeAPIErrorPCM(frameSize int) ([]float32, []int16, []int32) {
	pcmFloat := make([]float32, frameSize)
	pcmInt16 := make([]int16, frameSize)
	pcmInt24 := make([]int32, frameSize)
	for i := range frameSize {
		value := int16((i*7919+1237)%20001 - 10000)
		pcmInt16[i] = value
		pcmFloat[i] = float32(value) / 32768.0
		pcmInt24[i] = int32(value) * 256
	}
	return pcmFloat, pcmInt16, pcmInt24
}

func encodeAPIErrorCall(enc *Encoder, api int, pcmFloat []float32, pcmInt16 []int16, pcmInt24 []int32, output []byte) (int, error) {
	switch api {
	case encodeAPIErrorFloat32:
		return enc.Encode(pcmFloat, output)
	case encodeAPIErrorInt16:
		return enc.EncodeInt16(pcmInt16, output)
	case encodeAPIErrorInt24:
		return enc.EncodeInt24(pcmInt24, output)
	default:
		return 0, ErrInvalidArgument
	}
}

func probeEncodeAPIErrorState(helper string, sampleRate, channels, api, frameSize int) ([]encodeAPIErrorRecord, error) {
	payload := libopustest.NewOraclePayloadVersion("GEAI", 1,
		uint32(sampleRate), uint32(channels), uint32(libopustest.OpusApplicationAudio), uint32(api), uint32(frameSize))
	data, err := libopustest.RunOracle(helper, payload.Bytes(), "public encode API error state", "GEAO")
	if err != nil {
		return nil, err
	}
	count := data.Count(7)
	records := make([]encodeAPIErrorRecord, count)
	for i := range records {
		records[i].ret = data.I32()
		records[i].finalRange = data.U32()
		records[i].packet = append([]byte(nil), data.Bytes(int(data.U32()))...)
	}
	if err := data.ExpectConsumed(); err != nil {
		return nil, err
	}
	return records, nil
}
