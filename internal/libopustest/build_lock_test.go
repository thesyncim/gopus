//go:build darwin || dragonfly || freebsd || illumos || linux || netbsd || openbsd || windows

package libopustest

import (
	"bufio"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestOracleBuildLockProcess(t *testing.T) {
	if path := os.Getenv("GOPUS_TEST_ORACLE_LOCK_PATH"); path != "" {
		fmt.Println("waiting")
		lock, err := lockOracleBuild(path)
		if err != nil {
			t.Fatal(err)
		}
		fmt.Println("locked")
		_, _ = io.Copy(io.Discard, os.Stdin)
		if err := lock.Close(); err != nil {
			t.Fatal(err)
		}
		return
	}

	for _, killOwner := range []bool{false, true} {
		t.Run(fmt.Sprintf("kill_owner_%t", killOwner), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 15*time.Second)
			defer cancel()
			path := filepath.Join(t.TempDir(), "shared.lock")
			start := func() (*exec.Cmd, io.WriteCloser, <-chan string) {
				t.Helper()
				cmd := exec.CommandContext(ctx, os.Args[0], "-test.run=^TestOracleBuildLockProcess$")
				cmd.Env = append(os.Environ(), "GOPUS_TEST_ORACLE_LOCK_PATH="+path)
				cmd.Stderr = os.Stderr
				out, err := cmd.StdoutPipe()
				if err != nil {
					t.Fatal(err)
				}
				in, err := cmd.StdinPipe()
				if err != nil {
					t.Fatal(err)
				}
				if err := cmd.Start(); err != nil {
					t.Fatal(err)
				}
				lines := make(chan string, 16)
				go func() {
					defer close(lines)
					scanner := bufio.NewScanner(out)
					for scanner.Scan() {
						lines <- scanner.Text()
					}
				}()
				return cmd, in, lines
			}
			wantLine := func(lines <-chan string, want string) {
				t.Helper()
				select {
				case got := <-lines:
					if got != want {
						t.Fatalf("child output %q, want %q", got, want)
					}
				case <-ctx.Done():
					t.Fatal(ctx.Err())
				}
			}
			owner, ownerIn, ownerLines := start()
			wantLine(ownerLines, "waiting")
			wantLine(ownerLines, "locked")
			contender, contenderIn, contenderLines := start()
			wantLine(contenderLines, "waiting")
			select {
			case got := <-contenderLines:
				t.Fatalf("contender crossed held lock: %q", got)
			case <-time.After(150 * time.Millisecond):
			}
			if killOwner {
				if err := owner.Process.Kill(); err != nil {
					t.Fatal(err)
				}
			} else if err := ownerIn.Close(); err != nil {
				t.Fatal(err)
			}
			if err := owner.Wait(); err != nil && !killOwner {
				t.Fatal(err)
			}
			_ = ownerIn.Close()
			wantLine(contenderLines, "locked")
			_ = contenderIn.Close()
			if err := contender.Wait(); err != nil {
				t.Fatal(err)
			}
			if _, err := os.Stat(path); err != nil {
				t.Fatalf("lock inode must persist: %v", err)
			}
		})
	}
}
