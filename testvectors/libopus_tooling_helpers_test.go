package testvectors

import (
	"os"
	"os/exec"
	"path/filepath"
	"sync"
)

var (
	fixtureOpusDemoEnsureOnce sync.Once
	fixtureOpusDemoPath       string
	fixtureOpusDemoFound      bool
)

func fixtureOpusDemoCandidates() []string {
	return []string{
		filepath.Join("tmp_check", "opus-1.6.1", "opus_demo"),
		filepath.Join("..", "tmp_check", "opus-1.6.1", "opus_demo"),
		filepath.Join("..", "..", "tmp_check", "opus-1.6.1", "opus_demo"),
	}
}

func fixtureEnsureScriptCandidates() []string {
	return []string{
		filepath.Join("tools", "ensure_libopus.sh"),
		filepath.Join("..", "tools", "ensure_libopus.sh"),
		filepath.Join("..", "..", "tools", "ensure_libopus.sh"),
	}
}

func findFixtureOpusDemo() (string, bool) {
	for _, path := range fixtureOpusDemoCandidates() {
		if st, err := os.Stat(path); err == nil && (st.Mode()&0o111) != 0 {
			return path, true
		}
	}
	return "", false
}

func runFixtureEnsureScript() {
	for _, script := range fixtureEnsureScriptCandidates() {
		if st, err := os.Stat(script); err != nil || st.IsDir() {
			continue
		}
		cmd := exec.Command("sh", script)
		_, _ = cmd.CombinedOutput()
		return
	}
}

func getFixtureOpusDemoPathAuto() (string, bool) {
	fixtureOpusDemoEnsureOnce.Do(func() {
		if p, ok := findFixtureOpusDemo(); ok {
			fixtureOpusDemoPath, fixtureOpusDemoFound = p, true
			return
		}
		runFixtureEnsureScript()
		if p, ok := findFixtureOpusDemo(); ok {
			fixtureOpusDemoPath, fixtureOpusDemoFound = p, true
		}
	})
	return fixtureOpusDemoPath, fixtureOpusDemoFound
}
