//go:build gopus_qext

package gopus_test

import (
	"fmt"
	"testing"

	gopus "github.com/thesyncim/gopus"
	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestEncoder100msBudgetMatchesLibopusAt96kQEXT(t *testing.T) {
	libopustest.RequireOracle(t)
	const (
		sampleRate = 96000
		bitrate    = 64000
	)
	configure := func(enc *gopus.Encoder) error { return enc.SetQEXT(true) }
	for _, channels := range []int{1, 2} {
		for api := encoder100msFloatAPI; api <= encoder100msInt24API; api++ {
			t.Run(fmt.Sprintf("%dch_api%d", channels, api), func(t *testing.T) {
				ops := []encoder100msBudgetOperation{
					{frameSize: 9600, expertDuration: gopus.ExpertFrameDurationArg, budget: 4000},
					{frameSize: 9600, expertDuration: gopus.ExpertFrameDurationArg, budget: 1},
					{frameSize: 9600, expertDuration: gopus.ExpertFrameDurationArg, budget: 4000},
				}
				got := compareEncoder100msBudgetSequence(t, sampleRate, channels, api, bitrate, ops, configure)
				if got[0].rangeV == 0 {
					t.Fatal("priming packet has zero final range; rejection would not prove range clearing")
				}
				if got[1].status != -2 {
					t.Fatalf("96 kHz 100 ms one-byte status=%d, want OPUS_BUFFER_TOO_SMALL (-2)", got[1].status)
				}
			})
		}
	}

	for api := encoder100msFloatAPI; api <= encoder100msInt24API; api++ {
		t.Run(fmt.Sprintf("budget2_api%d", api), func(t *testing.T) {
			ops := []encoder100msBudgetOperation{
				{frameSize: 9600, expertDuration: gopus.ExpertFrameDurationArg, budget: 2},
				{frameSize: 9600, expertDuration: gopus.ExpertFrameDurationArg, budget: 4000},
			}
			got := compareEncoder100msBudgetSequence(t, sampleRate, 1, api, bitrate, ops, configure)
			if len(got) == len(ops) && (got[0].status < 0 || got[0].status == 0) {
				t.Fatalf("96 kHz 100 ms two-byte status=%d, want a non-empty packet", got[0].status)
			}
		})
	}

	for _, tc := range []struct {
		name           string
		frameSize      int
		expertDuration gopus.ExpertFrameDuration
		wantReject     bool
	}{
		{name: "caller120_selected100", frameSize: 11520, expertDuration: gopus.ExpertFrameDuration100Ms, wantReject: true},
		{name: "caller100_selected20", frameSize: 9600, expertDuration: gopus.ExpertFrameDuration20Ms},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ops := []encoder100msBudgetOperation{{frameSize: tc.frameSize, expertDuration: tc.expertDuration, budget: 1}}
			got := compareEncoder100msBudgetSequence(t, sampleRate, 1, encoder100msFloatAPI, bitrate, ops, configure)
			if len(got) != 1 {
				return
			}
			if tc.wantReject && got[0].status != -2 {
				t.Fatalf("96 kHz selected 100 ms one-byte status=%d, want -2", got[0].status)
			}
			if !tc.wantReject && (got[0].status < 0 || len(got[0].packet) == 0) {
				t.Fatalf("96 kHz selected 20 ms one-byte status=%d packet=%x, want packet", got[0].status, got[0].packet)
			}
		})
	}
}
