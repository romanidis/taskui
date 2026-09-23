package cmd

import (
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func projectWith(t *testing.T, taskfile string) string {
	t.Helper()
	if _, err := exec.LookPath("task"); err != nil {
		t.Skip("go-task is not installed")
	}
	t.Setenv("XDG_STATE_HOME", t.TempDir())
	inTempConfig(t)
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "Taskfile.yml"), []byte(taskfile), 0o600); err != nil {
		t.Fatal(err)
	}
	return dir
}

// A name go-task does not know has no graph to print, and saying so is not a success. It
// used to print the name alone, as though it were a task that ran nothing else.
func TestGraphOfATaskThatDoesNotExistIsAnError(t *testing.T) {
	dir := projectWith(t, "version: \"3\"\ntasks:\n  build:\n    aliases: [b]\n    cmds: ['echo hi']\n")
	t.Cleanup(func() { opts.graph = "" })

	_, err := execute(t, "--graph", "nosuch", dir)
	if err == nil || !strings.Contains(err.Error(), "no task called nosuch") {
		t.Errorf("err = %v", err)
	}
	for _, name := range []string{"build", "b"} {
		if out, err := execute(t, "--graph", name, dir); err != nil || !strings.Contains(out, name) {
			t.Errorf("--graph %s: %v %q", name, err, out)
		}
	}
}

// `--run` exits with the task's status, and a screenshot of the run does not change that.
func TestRunWithAScreenshotStillExitsWithTheTask(t *testing.T) {
	dir := projectWith(t, "version: \"3\"\ntasks:\n  boom:\n    cmds: ['exit 3']\n")
	t.Cleanup(func() { opts.runTask, opts.screenshot = "", "" })

	out, err := execute(t, "--run", "boom", "--screenshot", "80x20", dir)
	if !strings.Contains(out, "boom") {
		t.Errorf("no frame of the run: %q", out)
	}
	status, ok := errors.AsType[exitError](err)
	if !ok || status.code == 0 {
		t.Errorf("err = %v, want the task's non-zero status", err)
	}
}
