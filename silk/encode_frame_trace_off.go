//go:build !gopus_silk_trace

package silk

const encodeFrameTraceEnabled = false

func recordEncodeFrameTrace(_ *Encoder, _ encodeFrameTrace) {}

func withEncodeFrameTraceHook(_ func(*Encoder, encodeFrameTrace), fn func()) {
	fn()
}
