package encoder

// maybeLogHybridStageDebug is wired by temporary local probes in some worktrees.
// Keep a tracked no-op fallback so normal builds/tests do not depend on probe files.
func maybeLogHybridStageDebug(_ int, _ string, _ int, _ int, _ int, _ int, _ int, _ int, _ bool, _ bool, _ int, _ string) {
}
