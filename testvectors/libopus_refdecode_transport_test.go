package testvectors

import (
	"encoding/hex"
	"testing"

	"github.com/thesyncim/gopus/internal/libopustest"
)

func TestLibopusReferenceSingleDecodeBinaryTransport(t *testing.T) {
	t.Parallel()
	libopustest.RequireOracle(t)
	if _, err := getLibopusRefdecodeSinglePath(); err != nil {
		libopustest.HelperUnavailable(t, "reference decode", err)
	}

	packet, err := hex.DecodeString("f07e205545fdb24e3ed7bb68fd783712689ec4cd56eb3186ae9077b60aa0dfda515e3aa4db52bcac855cbcb57b8a61115f6c799313ad2fd8306bc44685533557c03ac9eceef1a589935c62d82d5fb4ea")
	if err != nil {
		t.Fatalf("decode packet hex: %v", err)
	}

	decoded, err := decodeWithLibopusReferencePacketsSingle(1, maxOpusPacketSamples48k, [][]byte{packet})
	if err != nil {
		t.Fatalf("decode packet with libopus helper: %v", err)
	}
	if len(decoded) != 480 {
		t.Fatalf("decoded sample count mismatch: got %d want 480", len(decoded))
	}
}
