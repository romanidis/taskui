package cmd

import (
	"os"
	"os/exec"
	"path/filepath"
	"syscall"
	"testing"
	"time"

	"github.com/romanidis/taskui/internal/run"
)

// A plain `--run` killed by its caller has to take its task down with it: the child is a
// session leader of its own, so nothing else will.
func TestATerminatedHeadlessRunStopsItsTask(t *testing.T) {
	if _, err := exec.LookPath("task"); err != nil {
		t.Skip("go-task is not installed")
	}
	dir := t.TempDir()
	taskfile := "version: \"3\"\ntasks:\n  slow:\n    cmds: ['echo started', 'sleep 30']\n"
	if err := os.WriteFile(filepath.Join(dir, "Taskfile.yml"), []byte(taskfile), 0o600); err != nil {
		t.Fatal(err)
	}
	r := run.Start(dir, run.Invocation{Task: "slow"})
	stop := stopOnSignal(r)
	defer stop()

	deadline := time.Now().Add(15 * time.Second)
	for time.Now().Before(deadline) {
		r.Poll()
		if t, ok := r.Tasks["slow"]; ok && len(t.Lines) > 0 {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	if err := syscall.Kill(os.Getpid(), syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	deadline = time.Now().Add(10 * time.Second)
	for !r.Finished() && time.Now().Before(deadline) {
		r.Poll()
		time.Sleep(20 * time.Millisecond)
	}
	if !r.Finished() || !r.Cancelled() {
		t.Errorf("finished = %v, cancelled = %v: SIGTERM should have stopped the task", r.Finished(), r.Cancelled())
	}
}
