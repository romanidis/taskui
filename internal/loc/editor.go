package loc

import (
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/romanidis/taskui/internal/shellwords"
)

// editorWords splits $VISUAL or $EDITOR into the program and its flags.
//
// Split the way a shell would, so a quoted path survives: `"/Applications/Sublime
// Text.app/Contents/SharedSupport/bin/subl" -w` is the documented way to write one. And
// before that, the whole value is tried as a path, because the undocumented way — the same
// path, unquoted — is what most people actually paste, and taking it apart at its space
// asked the system to run `/Applications/Sublime`.
func editorWords(spec string) []string {
	spec = strings.TrimSpace(spec)
	if spec == "" {
		return nil
	}
	if strings.ContainsAny(spec, " \t") {
		//nolint:gosec // the path is the user's own $EDITOR, only asked whether it exists
		if info, err := os.Stat(spec); err == nil && !info.IsDir() {
			return []string{spec}
		}
	}
	return shellwords.Split(spec)
}

// Editor is the command to open a location, already split into argv.
type Editor struct {
	// Name is the program, for saying what is about to happen.
	Name string
	Args []string
	// Terminal is whether it takes over this terminal. A `code --goto` returns immediately
	// and should not black out the UI to do it; a `vim` must.
	Terminal bool
}

// terminalEditors are the ones that draw on this terminal, and therefore need it handed
// over. Anything unrecognised is assumed to be one of these: waiting for an editor that had
// already returned costs a keypress, while drawing over one that is mid-screen costs the
// session.
var terminalEditors = map[string]bool{
	"vi": true, "vim": true, "nvim": true,
	"nano": true, "pico": true, "emacs": true, "emacsclient": true,
	"hx": true, "helix": true, "kak": true, "micro": true,
	"ne": true, "joe": true, "mg": true, "ed": true,
}

// windowedEditors return immediately, handing the file to an already-running window.
var windowedEditors = map[string]bool{
	"code": true, "code-insiders": true, "codium": true, "cursor": true, "zed": true,
	"subl": true, "sublime_text": true, "atom": true, "gvim": true, "mate": true,
	"idea": true, "goland": true, "pycharm": true, "webstorm": true, "rubymine": true,
	"clion": true, "phpstorm": true, "rustrover": true, "fleet": true,
}

// EditorFor builds the command that opens loc, from $VISUAL or $EDITOR.
//
// The line number is the entire point, so the argv is per-editor rather than a bare
// `$EDITOR file`: an editor that opens at line 1 when the output said line 212 has done the
// tedious half of the job and left the useful half. Anything unrecognised still gets
// opened, just at the top — which is worse than the alternative but much better than
// refusing.
//
// Flags already in the variable are honoured: `EDITOR="code -w"` is a real thing people
// have set, and dropping the `-w` would silently change what it does.
func EditorFor(l Loc, abs string) (Editor, bool) {
	spec := os.Getenv("VISUAL")
	if strings.TrimSpace(spec) == "" {
		spec = os.Getenv("EDITOR")
	}
	fields := editorWords(spec)
	if len(fields) == 0 {
		return Editor{}, false
	}
	prog := fields[0]
	// Copied rather than resliced: every arm below appends to it, and appending to a slice
	// of `fields` would write into `fields` itself whenever the capacity happened to allow.
	extra := append([]string(nil), fields[1:]...)
	base := strings.TrimSuffix(filepath.Base(prog), ".exe")

	line := strconv.Itoa(l.Line)
	col := l.Col
	if col <= 0 {
		col = 1
	}

	// Each arm produces only the editor-specific part; `extra` is prepended once at the end.
	// Appending to `extra` in every arm reads as nine chances to append to the wrong slice.
	var tail []string
	switch base {
	case "vi", "vim", "nvim", "gvim", "ex":
		// `+N file` is the POSIX spelling and every vi understands it.
		tail = []string{"+" + line, abs}
	case "nano", "pico":
		tail = []string{"+" + line + "," + strconv.Itoa(col), abs}
	case "emacs", "emacsclient":
		tail = []string{"+" + line + ":" + strconv.Itoa(col), abs}
	case "micro", "ne", "joe", "mg":
		// mg takes `+number` and nothing more: given `+3:2` it opens a new buffer by that
		// name, which is the failure the default arm below exists to avoid.
		tail = []string{"+" + line, abs}
	case "hx", "helix":
		// Helix takes the location suffixed to the path.
		tail = []string{abs + ":" + line + ":" + strconv.Itoa(col)}
	case "kak":
		// Kakoune does not read a suffix off the path — it would open a file named for it
		// — and takes the position as its own argument instead.
		tail = []string{"+" + line + ":" + strconv.Itoa(col), abs}
	case "code", "code-insiders", "codium", "cursor":
		tail = []string{"--goto", abs + ":" + line + ":" + strconv.Itoa(col)}
	case "zed", "subl", "sublime_text", "atom":
		// Zed has no `--goto`; like Sublime it reads the position off the path, and an
		// unknown flag is an error to it rather than something ignored.
		tail = []string{abs + ":" + line + ":" + strconv.Itoa(col)}
	case "mate":
		// TextMate's `mate` takes the line as a flag.
		tail = []string{"-l", line, abs}
	case "idea", "goland", "pycharm", "webstorm", "rubymine", "clion", "phpstorm", "rustrover", "fleet":
		tail = []string{"--line", line, "--column", strconv.Itoa(col), abs}
	default:
		// Unknown: open the file and say nothing about where. `+N` is close to universal
		// among terminal editors, but "close to" is not good enough when being wrong means
		// the editor treats it as a second filename and creates it.
		tail = []string{abs}
	}

	args := append(append([]string(nil), extra...), tail...)

	terminal := true
	switch {
	case windowedEditors[base]:
		terminal = false
	case terminalEditors[base]:
		terminal = true
	}
	return Editor{Name: prog, Args: args, Terminal: terminal}, true
}
