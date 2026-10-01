package gopus_test

import (
	"bytes"
	"errors"
	"fmt"
	"testing"

	gopus "github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/libopustest"
)

const (
	encoder100msFloatAPI = iota
	encoder100msInt16API
	encoder100msInt24API
)

type encoder100msBudgetOperation struct {
	frameSize      int
	expertDuration gopus.ExpertFrameDuration
	budget         int
}

type encoder100msBudgetResult struct {
	status int
	rangeV uint32
	packet []byte
}

var encoder100msBudgetHelper libopustest.HelperCache

func encoder100msBudgetHelperPath() (string, error) {
	return encoder100msBudgetHelper.Path(func() (string, error) {
		return libopustest.BuildPublicAPIHelper(libopustest.CHelperConfig{
			Label:       "100 ms encode budget",
			OutputBase:  "gopus_libopus_encode_100ms_budget",
			SourceFile:  "libopus_encode_100ms_budget.c",
			RefIncludes: []string{"src", "celt", "silk"},
			CFlags:      []string{"-DHAVE_CONFIG_H"},
		})
	})
}

func runEncoder100msBudgetOracle(t *testing.T, sampleRate, channels, api, bitrate int, ops []encoder100msBudgetOperation) []encoder100msBudgetResult {
	t.Helper()
	path, err := encoder100msBudgetHelperPath()
	if err != nil {
		libopustest.HelperUnavailable(t, "100 ms encode budget", err)
		return nil
	}
	payload := libopustest.NewOraclePayload("G100", uint32(sampleRate), uint32(channels), uint32(bitrate), uint32(api), uint32(len(ops)))
	for _, op := range ops {
		payload.U32(uint32(op.frameSize))
		payload.U32(uint32(op.expertDuration))
		payload.U32(uint32(op.budget))
	}
	output, err := libopustest.RunHelper(path, payload.Bytes())
	if err != nil {
		libopustest.HelperUnavailable(t, "100 ms encode budget", err)
		return nil
	}
	reader, err := libopustest.NewOracleReader("100 ms encode budget", "G100", output)
	if err != nil {
		t.Fatal(err)
	}
	count := reader.Count(len(ops))
	results := make([]encoder100msBudgetResult, count)
	for i := range results {
		results[i].status = int(reader.I32())
		results[i].rangeV = reader.U32()
		packetLen := int(reader.U32())
		results[i].packet = append([]byte(nil), reader.Bytes(packetLen)...)
	}
	if err := reader.ExpectConsumed(); err != nil {
		t.Fatal(err)
	}
	return results
}

func compareEncoder100msBudgetSequence(t *testing.T, sampleRate, channels, api, bitrate int, ops []encoder100msBudgetOperation, configure func(*gopus.Encoder) error) []encoder100msBudgetResult {
	t.Helper()
	want := runEncoder100msBudgetOracle(t, sampleRate, channels, api, bitrate, ops)
	if len(want) != len(ops) {
		t.Fatalf("oracle results=%d want %d", len(want), len(ops))
	}
	enc, err := gopus.NewEncoder(gopus.EncoderConfig{
		SampleRate:  sampleRate,
		Channels:    channels,
		Application: gopus.ApplicationAudio,
	})
	if err != nil {
		t.Fatalf("create %d Hz encoder: %v", sampleRate, err)
	}
	if err := enc.SetBitrate(bitrate); err != nil {
		t.Fatal(err)
	}
	if configure != nil {
		if err := configure(enc); err != nil {
			t.Fatal(err)
		}
	}

	for i, op := range ops {
		if enc.FrameSize() != op.frameSize {
			if err := enc.SetFrameSize(op.frameSize); err != nil {
				t.Fatalf("operation %d SetFrameSize(%d): %v", i, op.frameSize, err)
			}
		}
		if err := enc.SetExpertFrameDuration(op.expertDuration); err != nil {
			t.Fatalf("operation %d SetExpertFrameDuration(%d): %v", i, op.expertDuration, err)
		}
		out := make([]byte, op.budget)
		var n int
		var encodeErr error
		samples := op.frameSize * channels
		switch api {
		case encoder100msFloatAPI:
			n, encodeErr = enc.Encode(make([]float32, samples), out)
		case encoder100msInt16API:
			n, encodeErr = enc.EncodeInt16(make([]int16, samples), out)
		case encoder100msInt24API:
			n, encodeErr = enc.EncodeInt24(make([]int32, samples), out)
		default:
			t.Fatalf("unknown input API %d", api)
		}

		gotPacket := []byte(nil)
		if want[i].status == -2 {
			if !errors.Is(encodeErr, gopus.ErrBufferTooSmall) || n != 0 {
				t.Fatalf("operation %d Go=(%d,%v), want OPUS_BUFFER_TOO_SMALL (-2)", i, n, encodeErr)
			}
		} else if want[i].status < 0 {
			t.Fatalf("operation %d C oracle returned unexpected status %d", i, want[i].status)
		} else {
			if encodeErr != nil || n != want[i].status {
				t.Fatalf("operation %d Go=(%d,%v), C=(%d,nil)", i, n, encodeErr, want[i].status)
			}
			gotPacket = out[:n]
		}
		if !bytes.Equal(gotPacket, want[i].packet) {
			t.Fatalf("operation %d packet Go=%x C=%x", i, gotPacket, want[i].packet)
		}
		if gotRange := enc.FinalRange(); gotRange != want[i].rangeV {
			t.Fatalf("operation %d final range Go=%08x C=%08x", i, gotRange, want[i].rangeV)
		}
	}
	return want
}

func TestEncoder100msOneByteBudgetMatchesLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		sampleRate = 48000
		bitrate    = 64000
	)
	for _, channels := range []int{1, 2} {
		for api := encoder100msFloatAPI; api <= encoder100msInt24API; api++ {
			name := fmt.Sprintf("%dch_api%d", channels, api)
			t.Run(name, func(t *testing.T) {
				ops := []encoder100msBudgetOperation{
					{frameSize: 4800, expertDuration: gopus.ExpertFrameDurationArg, budget: 4000},
					{frameSize: 4800, expertDuration: gopus.ExpertFrameDurationArg, budget: 1},
					{frameSize: 4800, expertDuration: gopus.ExpertFrameDurationArg, budget: 4000},
				}
				got := compareEncoder100msBudgetSequence(t, sampleRate, channels, api, bitrate, ops, nil)
				if got[0].rangeV == 0 {
					t.Fatal("priming packet has zero final range; rejection would not prove range clearing")
				}
				if got[1].status != -2 {
					t.Fatalf("100 ms one-byte status=%d, want OPUS_BUFFER_TOO_SMALL (-2)", got[1].status)
				}
			})
		}
	}
}

func TestEncoder100msTwoByteBudgetAndSelectedDurationMatchLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	const bitrate = 64000
	// Two bytes remain a valid low-space packet budget for a selected 100 ms
	// frame. A 120 ms caller frame selected down to 100 ms reaches the same guard.
	for _, api := range []int{encoder100msFloatAPI, encoder100msInt16API, encoder100msInt24API} {
		t.Run(fmt.Sprintf("budget2_api%d", api), func(t *testing.T) {
			ops := []encoder100msBudgetOperation{
				{frameSize: 4800, expertDuration: gopus.ExpertFrameDurationArg, budget: 2},
				{frameSize: 4800, expertDuration: gopus.ExpertFrameDurationArg, budget: 4000},
			}
			got := compareEncoder100msBudgetSequence(t, 48000, 1, api, bitrate, ops, nil)
			if len(got) == len(ops) && (got[0].status < 0 || got[0].status == 0) {
				t.Fatalf("100 ms two-byte status=%d, want a non-empty packet", got[0].status)
			}
		})
	}

	for _, tc := range []struct {
		name           string
		frameSize      int
		expertDuration gopus.ExpertFrameDuration
		wantReject     bool
	}{
		{name: "caller120_selected100", frameSize: 5760, expertDuration: gopus.ExpertFrameDuration100Ms, wantReject: true},
		{name: "caller100_selected20", frameSize: 4800, expertDuration: gopus.ExpertFrameDuration20Ms},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ops := []encoder100msBudgetOperation{{frameSize: tc.frameSize, expertDuration: tc.expertDuration, budget: 1}}
			got := compareEncoder100msBudgetSequence(t, 48000, 1, encoder100msFloatAPI, bitrate, ops, nil)
			if len(got) != 1 {
				return
			}
			if tc.wantReject && got[0].status != -2 {
				t.Fatalf("selected 100 ms one-byte status=%d, want -2", got[0].status)
			}
			if !tc.wantReject && (got[0].status < 0 || len(got[0].packet) == 0) {
				t.Fatalf("selected 20 ms one-byte status=%d packet=%x, want packet", got[0].status, got[0].packet)
			}
		})
	}
}
