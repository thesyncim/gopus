//go:build gopus_silk_trace

package silk

const encodeFrameTraceEnabled = true

var encodeFrameTraceHook func(*Encoder, encodeFrameTrace)

func recordEncodeFrameTrace(e *Encoder, trace encodeFrameTrace) {
	if encodeFrameTraceHook != nil {
		encodeFrameTraceHook(e, trace)
	}
}

func withEncodeFrameTraceHook(hook func(*Encoder, encodeFrameTrace), fn func()) {
	prev := encodeFrameTraceHook
	encodeFrameTraceHook = hook
	defer func() {
		encodeFrameTraceHook = prev
	}()
	fn()
}
