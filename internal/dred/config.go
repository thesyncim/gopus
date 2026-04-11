package dred

// Constants mirrored from libopus 1.6.1 dnn/dred_config.h.
const (
	ExtensionID             = 126
	ExperimentalVersion     = 12
	ExperimentalHeaderBytes = 2
	MinBytes                = 8
	FrameSize               = 160
	DFrameSize              = 2 * FrameSize
	MaxDataSize             = 1000
	MaxLatents              = 26
	NumRedundancyFrames     = 2 * MaxLatents
	MaxFrames               = 4 * MaxLatents
)

// ValidExperimentalPayload reports whether data matches the temporary libopus
// DRED extension framing and size bounds.
func ValidExperimentalPayload(data []byte) bool {
	if len(data) < ExperimentalHeaderBytes+MinBytes || len(data) > ExperimentalHeaderBytes+MaxDataSize {
		return false
	}
	return len(data) >= ExperimentalHeaderBytes &&
		data[0] == 'D' &&
		int(data[1]) == ExperimentalVersion
}
