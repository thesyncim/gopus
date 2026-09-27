//go:build gopus_dred

package silk

// deepPLCSkipsRecoveryRamp matches the ENABLE_DEEP_PLC condition in
// silk/PLC.c:silk_PLC_glue_frames. At 16 kHz, deep PLC handles the recovery
// transition, so the ordinary SILK gain ramp is not applied.
func deepPLCSkipsRecoveryRamp(fsKHz int32) bool {
	return fsKHz == 16
}
