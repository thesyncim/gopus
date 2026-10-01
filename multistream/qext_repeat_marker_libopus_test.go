//go:build gopus_qext

package multistream

import (
	"bytes"
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/encoder"
	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestQEXTAfterEmptyRepeatMatchesSelectedLibopus(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, channels := range []int{1, 2} {
		for _, mode := range []encoder.Mode{encoder.ModeCELT, encoder.ModeHybrid} {
			base := encodeModeSwitchSingleStreamPackets(t, channels, 960, []encoder.Mode{mode})[0]
			if base[0]&3 != 0 {
				t.Fatal("expected one elementary frame")
			}
			for _, repeat := range []bool{false, true} {
				// Empty Repeat-These-Extensions with L=1 has no entries to yield.
				// src/extensions.c continues to the following ID124 payload.
				padding := []byte{}
				if repeat {
					padding = append(padding, 0x05)
				}
				padding = append(padding, 0xf8)
				padding = append(padding, bytes.Repeat([]byte{0xff}, 32)...)
				packet := append([]byte{base[0] | 3, 0x41, byte(len(padding))}, base[1:]...)
				packet = append(packet, padding...)
				t.Run(fmt.Sprintf("mode%d/ch%d/repeat%t", mode, channels, repeat), func(t *testing.T) {
					assertMultistreamSequenceFormatsMatchSelectedLibopus(t, 48000, channels, [][]byte{packet, nil, base, packet})
				})
			}
		}
	}
}
