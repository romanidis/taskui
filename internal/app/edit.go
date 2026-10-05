package app

import (
	"fmt"
	"path/filepath"
	"strings"

	"github.com/romanidis/taskui/internal/diff"
	"github.com/romanidis/taskui/internal/events"
	"github.com/romanidis/taskui/internal/loc"
)

// TakeEdit hands over the editor the last keypress asked for, and clears it.
//
// The key handlers cannot return a Bubble Tea command, so the intent is parked here and
// Update collects it. Storing the intent rather than the command is also what lets a test
// assert what would have been launched without launching anything.
func (a *App) TakeEdit() (loc.Editor, bool) {
	if a.pendingEdit == nil {
		return loc.Editor{}, false
	}
	e := *a.pendingEdit
	a.pendingEdit = nil
	return e, true
}

// EditUnderCursor opens whatever file the row under the cursor points at.
//
// The row is tried first and the rest of the screen second. A failing Go test prints its
// assertion on one line and the `--- FAIL` on another, and pressing `e` on the wrong one of
// those two should not be a dead keystroke — so a row with no location of its own falls back
// to the first one its task printed, which for a compiler is the error and for a test runner
// is the first failing assertion. Both are the place you wanted to go.
//
// What the rest of the screen is depends on the screen. The run view has the task's captured
// output in memory; a diff opened from a timeline does not — `a.Run` there is whatever
// happened to be open before, which is a different run or none at all, and reading it would
// answer a question about the wrong thing.
func (a *App) EditUnderCursor() {
	// Tried in order. at is the line a candidate is on, counted from 1, for the note saying
	// where a location came from; the row under the cursor needs no note.
	type candidate struct {
		text string
		at   int
	}
	var tries []candidate
	var task, source string

	if a.Screen == ScreenDiff {
		if a.DiffCursor >= len(a.DiffRows) {
			a.Status = "nothing here to open"
			return
		}
		task, source = a.DiffOf, "the diff"
		tries = append(tries, candidate{text: a.DiffRows[a.DiffCursor].Text})
		// The lines that arrived first. In a diff, what is new is what you are there about — a
		// location on a line both runs printed is the one that was already fine.
		for _, arrived := range []bool{true, false} {
			for i, row := range a.DiffRows {
				if !row.Gap && (row.Op == diff.Ins) == arrived {
					tries = append(tries, candidate{row.Text, i + 1})
				}
			}
		}
	} else {
		if a.Run == nil || a.RunCursor >= len(a.RunRows) {
			a.Status = "nothing here to open"
			return
		}
		row := a.RunRows[a.RunCursor]
		task = row.Task
		if row.IsTask {
			task = row.Name
		}
		source = "`" + task + "`"
		if t := a.Run.Tasks[task]; t != nil {
			if !row.IsTask && row.Index < len(t.Lines) {
				tries = append(tries, candidate{text: t.Lines[row.Index].Plain})
			}
			for i, l := range t.Lines {
				tries = append(tries, candidate{l.Plain, i + 1})
			}
		}
	}

	for _, c := range tries {
		if l, ok := loc.First(c.text); ok {
			note := ""
			if c.at > 0 {
				note = fmt.Sprintf(" (from line %d of %s)", c.at, source)
			}
			a.openLocationFrom(l, task, note)
			return
		}
	}
	if a.Screen == ScreenDiff {
		a.Status = "no file:line anywhere in this diff"
		return
	}
	a.Status = "no file:line here — `" + task + "` did not print one"
}

// openLocationFrom resolves a location printed by task and parks the editor command for
// Update to run.
//
// Every way this can fail says what it was trying to do. A key that silently does nothing
// is indistinguishable from a key that is broken, and this one has four separate ways of
// not working — the file is not there, the name is ambiguous, no editor is configured, or
// the editor is one whose line-number spelling is unknown.
func (a *App) openLocationFrom(l loc.Loc, task, note string) {
	where := fmt.Sprintf("%s:%d", l.Path, l.Line)
	// Looked for where the task runs, as far as taskui can tell: beside the Taskfile that
	// defines it, which is go-task's default for an included one. A task with its own `dir:`
	// is the case this misses — neither listing says what that is — and it falls back to the
	// project root, which is where everything was resolved before.
	dir := ""
	if def, ok := a.WhereIs(task); ok {
		dir = filepath.Dir(def.File)
	}
	// The project is indexed on the first `e` rather than in New: most sessions never press
	// it, and walking the tree for a key nobody used is a cost paid by everyone.
	if a.locs == nil {
		a.locs = loc.NewResolver(a.Root)
	}
	abs, ambiguous, ok := a.locs.ResolveIn(dir, l.Path)
	if !ok {
		a.Status = where + " — no such file under " + baseName(a.Root) + note
		return
	}

	// With a host attached — an editor showing this terminal — the file is its to open.
	// Launching $EDITOR here would put a second editor inside the first one's window.
	if a.HasHost() {
		reason := note
		if ambiguous {
			reason += " — several files share that name"
		}
		a.events.Send(events.Edit{
			Type: "edit", Path: abs, Line: l.Line, Col: l.Col, Note: strings.TrimSpace(reason),
		})
		a.Status = fmt.Sprintf("opening %s:%d in the editor", relativeTo(a.Root, abs), l.Line) + reason
		return
	}

	editor, ok := loc.EditorFor(l, abs)
	if !ok {
		a.Status = "set $EDITOR or $VISUAL to open " + where
		return
	}
	a.pendingEdit = &editor

	a.Status = fmt.Sprintf("opening %s:%d in %s", relativeTo(a.Root, abs), l.Line, baseName(editor.Name))
	if ambiguous {
		a.Status += " — several files share that name"
	}
	a.Status += note
}

// EditDefinition opens the Taskfile a task is written in, at its own line.
//
// The same key as the one that opens a `file:line` from output, because it is the same
// intent: take me to the thing on screen. In a run that is whatever the error named; in the
// picker there is no error, and the thing on screen is the task.
func (a *App) EditDefinition(name string) {
	if name == "" {
		a.Status = "nothing here to open — space folds it"
		return
	}
	where, ok := a.WhereIs(name)
	if !ok {
		// The listing arrives on a background goroutine and can take seconds on a workspace
		// with a lot of `sources:` globs. Saying which of the two it is beats a bare no.
		if a.Details == nil {
			a.Status = "still reading the Taskfile — try `e` again in a moment"
		} else {
			a.Status = "go-task did not say where `" + name + "` is defined"
		}
		return
	}
	a.openLocationFrom(loc.Loc{Path: where.File, Line: where.Line}, name, "")
}
