package testvectors

// libopusPacket holds a single encoded Opus packet and its range coder state.
// Used by fixture-backed libopus comparison tests.
type libopusPacket struct {
	data       []byte
	finalRange uint32
}
