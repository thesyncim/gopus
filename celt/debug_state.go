package celt

// bandDebugState keeps per-encoder/per-decoder debug counters.
// It avoids package-level mutable state that can leak across instances.
type bandDebugState struct {
	pvqDumpCounter  int
	pvqDumpFrame    int
	qDbgDecodeFrame int
	pvqCallSeq      int
}
