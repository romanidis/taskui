package app

import (
	"fmt"
	"os/exec"
	"strings"
)

// clipboardTools are shelled out to rather than taken as a dependency: one of these exists
// on any machine that has a clipboard at all, and a library for it would pull in a
// windowing stack for the sake of `pbcopy`.
var clipboardTools = []struct {
	name string
	args []string
}{
	{"pbcopy", nil},
	{"wl-copy", nil},
	{"xclip", []string{"-selection", "clipboard"}},
	{"xsel", []string{"--clipboard", "--input"}},
}

// Copy puts text on the system clipboard.
func (a *App) Copy(text, what string) {
	for _, tool := range clipboardTools {
		//nolint:gosec // the program and its flags come from the literal table above; the
		// only user data is the text, and that goes in on stdin.
		cmd := exec.Command(tool.name, tool.args...)
		cmd.Stdin = strings.NewReader(text)
		if err := cmd.Run(); err != nil {
			continue
		}
		lines := strings.Count(strings.TrimSuffix(text, "\n"), "\n") + 1
		if text == "" {
			lines = 0
		}
		if lines <= 1 {
			a.Status = "copied " + what
		} else {
			a.Status = fmt.Sprintf("copied %s — %d lines", what, lines)
		}
		return
	}
	a.Status = "no clipboard tool found (pbcopy, wl-copy, xclip, xsel)"
}

// YankLine copies the line under the cursor in the run view.
func (a *App) YankLine() {
	if a.RunCursor >= len(a.RunRows) || a.RunRows[a.RunCursor].IsTask {
		a.YankTaskOutput()
		return
	}
	row := a.RunRows[a.RunCursor]
	text := ""
	if a.Run != nil {
		if t, ok := a.Run.Tasks[row.Task]; ok && row.Index < len(t.Lines) {
			text = t.Lines[row.Index].Plain
		}
	}
	a.Copy(text, "line")
}

// YankTaskOutput copies everything the task under the cursor printed.
func (a *App) YankTaskOutput() {
	name, ok := a.RunSelectedTask()
	if !ok {
		return
	}
	var lines []string
	if a.Run != nil {
		if t, ok := a.Run.Tasks[name]; ok {
			lines = t.Output()
		}
	}
	a.Copy(strings.Join(lines, "\n"), name+" output")
}
