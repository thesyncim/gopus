package testvectors

// libopusPacket holds a single encoded Opus packet and its range coder state.
// Used by both CGO-based and opus_demo-based comparison tests.
type libopusPacket struct {
	data       []byte
	finalRange uint32
}
