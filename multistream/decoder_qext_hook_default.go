//go:build !gopus_qext
// +build !gopus_qext

package multistream

func (d *streamState) setCELTQEXTPayload(_ []byte) {}
