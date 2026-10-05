package app

import (
	"strings"

	"github.com/romanidis/taskui/internal/graph"
	"github.com/romanidis/taskui/internal/shellwords"
	"github.com/romanidis/taskui/internal/task"
)

// BeginArgs opens the args prompt for a task, pre-filled with the variables the task
// actually asks for.
//
// `requires: { vars: [NAME] }` is a declaration, not prose, so `NAME=` can be filled in
// with confidence — and only the key, never the example value, which would be handing you
// someone else's argument. The caret lands after the last `=`.
func (a *App) BeginArgs(name string) {
	a.EnteringArgs = true
	a.ArgsTarget = name
	a.Status = ""
	// What ⇥ completes belongs to the task the prompt is aimed at, and this may be the
	// second task it has been aimed at.
	a.argsPast = nil
	a.argsPastRead = false
	a.argsComp = nil
	a.argsFromHistory = false

	// One `--summary`, ~40ms, only when the prompt opens.
	vars := graph.RequiredVars(a.Root, name)
	if len(vars) == 0 {
		// Nothing declared: fall back to a `KEY=value` shape in the description.
		if hint, ok := a.ArgsHint(); ok {
			vars = task.KeysInHint(hint)
		}
	}

	parts := make([]string, 0, len(vars))
	for _, k := range vars {
		parts = append(parts, k+"=")
	}
	a.argsVars = vars
	a.ArgsInput = strings.Join(parts, " ")
	// Nothing declared and nothing mined leaves an empty line — and the best answer
	// available then is the one you gave last time. It is your own decision coming back,
	// not an example from someone else's description, which is the whole reason the `e.g.`
	// hint is only ever shown: `-- -p ingest` typed four times a day is four times it did
	// not have to be. A declaration still wins, because its value is what changes per run.
	if a.ArgsInput == "" {
		if past := a.argsHistory(); len(past) > 0 {
			a.ArgsInput = shellwords.Join(past[0])
			a.argsFromHistory = true
		}
	}
	a.argsPrefill = a.ArgsInput
	a.ArgsCursor = len([]rune(a.ArgsInput))
}

func (a *App) CancelArgs() {
	a.EnteringArgs = false
	a.ArgsTarget = ""
	a.ArgsInput = ""
	a.ArgsCursor = 0
	a.argsPrefill = ""
	a.argsFromHistory = false
	a.argsVars = nil
	a.argsPast = nil
	a.argsPastRead = false
	a.argsComp = nil
}

func (a *App) ArgsInsert(c rune) {
	runes := []rune(a.ArgsInput)
	at := min(a.ArgsCursor, len(runes))
	out := make([]rune, 0, len(runes)+1)
	out = append(out, runes[:at]...)
	out = append(out, c)
	out = append(out, runes[at:]...)
	a.ArgsInput = string(out)
	a.ArgsCursor = at + 1
}

func (a *App) ArgsBackspace() {
	if a.ArgsCursor == 0 {
		return
	}
	runes := []rune(a.ArgsInput)
	at := a.ArgsCursor - 1
	a.ArgsInput = string(append(runes[:at], runes[at+1:]...))
	a.ArgsCursor = at
}

func (a *App) ArgsDelete() {
	runes := []rune(a.ArgsInput)
	if a.ArgsCursor >= len(runes) {
		return
	}
	a.ArgsInput = string(append(runes[:a.ArgsCursor], runes[a.ArgsCursor+1:]...))
}

func (a *App) ArgsMove(delta int) {
	a.ArgsCursor = clamp(a.ArgsCursor+delta, 0, len([]rune(a.ArgsInput)))
}

func (a *App) ArgsHome() { a.ArgsCursor = 0 }
func (a *App) ArgsEnd()  { a.ArgsCursor = len([]rune(a.ArgsInput)) }

func (a *App) ConfirmArgs() {
	name := a.ArgsTarget
	if name == "" {
		return
	}
	args := shellwords.Split(a.ArgsInput)
	a.CancelArgs()
	// Through restart, as `r` and `⇧I` go: on a task that is still running, a plain request
	// only focused it, and the arguments just typed were dropped without a word.
	a.restart(a.armed(name, args))
}

// ArgsHint is the usage hint for whatever the args prompt is aimed at.
func (a *App) ArgsHint() (string, bool) {
	if a.ArgsTarget == "" {
		return "", false
	}
	for _, t := range a.Tasks {
		if t.Name == a.ArgsTarget {
			return t.ArgsHint()
		}
	}
	return "", false
}

func (a *App) handleArgsKey(k Key) {
	// ⇥ walks the completion; everything else ends the cycle it was walking, so the list
	// can never outlive the word it was built for.
	if k.kind == keyTab || k.kind == keyBackTab {
		delta := 1
		if k.kind == keyBackTab {
			delta = -1
		}
		a.CompleteArgs(delta)
		return
	}
	a.argsComp = nil

	switch {
	case k.kind == keyEsc:
		a.CancelArgs()
	case k.kind == keyEnter:
		a.ConfirmArgs()
	case k.kind == keyBackspace:
		a.ArgsBackspace()
	case k.kind == keyDelete:
		a.ArgsDelete()
	case k.kind == keyLeft:
		a.ArgsMove(-1)
	case k.kind == keyRight:
		a.ArgsMove(1)
	case k.kind == keyHome:
		a.ArgsHome()
	case k.kind == keyEnd:
		a.ArgsEnd()
	case k.typed():
		a.ArgsInsert(k.ch)
	}
}
