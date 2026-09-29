//go:build gopus_qext

package gopus

import (
	"fmt"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestDecodeMalformedFramingPrecedesSmallOutput96k(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, channels := range []int{1, 2} {
		for _, format := range []uint32{
			libopustest.DecodeDiffFormatFloat32,
			libopustest.DecodeDiffFormatInt16,
			libopustest.DecodeDiffFormatInt24,
		} {
			formatName := [...]string{"float32", "int16", "int24"}[format]
			t.Run(fmt.Sprintf("%dch/%s", channels, formatName), func(t *testing.T) {
				packet := encodeAPIRateCELTPacketFrameSize(t, channels, 960)
				assertDecodeMalformedFramingPrecedesSmallOutput(t, 96000, channels, format, packet, 1920)
			})
		}
	}
}

func TestDecodeEmptyAndPartialChannelBuffersPrecedePacketParsing96k(t *testing.T) {
	libopustest.RequireOracle(t)
	for _, channels := range []int{1, 2} {
		for _, format := range []uint32{
			libopustest.DecodeDiffFormatFloat32,
			libopustest.DecodeDiffFormatInt16,
			libopustest.DecodeDiffFormatInt24,
		} {
			formatName := [...]string{"float32", "int16", "int24"}[format]
			t.Run(fmt.Sprintf("%dch/%s", channels, formatName), func(t *testing.T) {
				packet := encodeAPIRateCELTPacketFrameSize(t, channels, 960)
				assertDecodeEmptyAndPartialChannelBuffers(
					t, 96000, channels, format, packet, 1920)
			})
		}
	}
}
