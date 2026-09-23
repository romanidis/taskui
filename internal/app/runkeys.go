package app

import (
	"github.com/romanidis/taskui/internal/keys"
)

//nolint:cyclop // as handlePickerKey: one table, read top to bottom.
func (a *App) handleRunKey(k Key) bool {
	// Input mode: everything except the escape hatch goes to the child.
	if a.SendingInput {
		switch {
		case k.kind == keyEsc:
			a.EndInput()
		// A pty expects carriage return, not newline.
		case k.kind == keyEnter:
			a.SendInput([]byte("\r"))
		case k.kind == keyBackspace:
			a.SendInput([]byte{0x7f})
		case k.kind == keyTab:
			a.SendInput([]byte("\t"))
		case k.kind == keyUp:
			a.SendInput([]byte("\x1b[A"))
		case k.kind == keyDown:
			a.SendInput([]byte("\x1b[B"))
		case k.kind == keyRight:
			a.SendInput([]byte("\x1b[C"))
		case k.kind == keyLeft:
			a.SendInput([]byte("\x1b[D"))
		case k.isCtrl('c'):
			a.SendInput([]byte{0x03})
		case k.isCtrl('d'):
			a.SendInput([]byte{0x04})
		// What was typed, not what was held: a modified key used to arrive here
		// indistinguishable from its bare letter, so ⌃z sent the child a `z`.
		case k.typed():
			a.SendInput([]byte(string(k.ch)))
		}
		return false
	}

	if a.EnteringArgs {
		a.handleArgsKey(k)
		return false
	}

	if a.Searching {
		switch {
		case k.kind == keyEsc:
			a.ClearSearch()
			return false
		// Keep the query and its highlights; leave the input line.
		case k.kind == keyEnter:
			a.Searching = false
			return false
		case k.kind == keyBackspace:
			a.PopSearch()
			return false
		case k.kind == keyDown:
			a.SearchStep(1)
			return false
		case k.kind == keyUp:
			a.SearchStep(-1)
			return false
		case k.typed():
			a.PushSearch(k.ch)
			return false
		}
	}

	act := func() keys.Action { return a.action(k, ScreenRun) }

	switch {
	// Stop the run without leaving the view.
	case act() == keys.Stop:
		a.CancelRun()
	case act() == keys.StopAll:
		a.RequestStopAll()

	// Answer whatever the task is asking.
	//
	// This works even in a non-interactive run: go-task wraps stdout and stderr for
	// prefixing but leaves stdin alone, so keystrokes reach the child regardless —
	// verified against a real `task` process. You may not be able to see the question, but
	// typing `y⏎` still answers it, which beats re-running a deploy from the start just to
	// be able to type.
	case act() == keys.Input:
		a.BeginInput()

	// Re-run whenever the source changes.
	case act() == keys.Watch:
		a.ToggleWatch()

	// Re-run this task interactively, when seeing the prompt matters more than not
	// starting over.
	case act() == keys.InteractiveRerun:
		a.InteractiveRerun()

	// Back to the picker. The run keeps going in the background and is still there when
	// you come back.
	case k.kind == keyEsc:
		a.Screen = ScreenPicker
		a.Status = ""

	// Reading an error usually ends with pasting it somewhere.
	case act() == keys.Yank:
		a.YankLine()
	case act() == keys.YankAll:
		a.YankTaskOutput()

	// …or with going there. The line under the cursor names a file and a line; this is the
	// step the tool used to leave you to do by hand.
	case act() == keys.Edit:
		a.EditUnderCursor()

	// What changed since this task last worked.
	case act() == keys.Diff:
		a.DiffAgainstLastGreen()

	// How it has been going, run after run.
	case act() == keys.Timeline:
		a.OpenTimeline(a.TimelineTaskFor())

	// Where the whole run's time went. Every task already shows its own clock in tree
	// order, which answers "is this step slow" and not "what makes this take four minutes".
	case act() == keys.Profile:
		a.OpenProfile()

	case k.isChar(' '), k.kind == keyRight, k.kind == keyLeft:
		a.RunToggleFold()
	case act() == keys.Fold:
		a.RunToggleFold()
	case act() == keys.FoldAll:
		a.RunToggleFoldAll()

	// Switch between open runs. Handled here rather than through the keymap because that
	// table is keyed by character and these are not characters.
	case k.kind == keyTab:
		a.CycleSlot(1)
	case k.kind == keyBackTab:
		a.CycleSlot(-1)
	case act() == keys.CloseSlot:
		a.CloseSlot()

	// Let it go. Quitting stops being responsible for it; `x` still is not.
	case act() == keys.Detach:
		a.Detach()

	// Search the output. `/` in the picker filters task names; here it searches what those
	// tasks printed. Different corpora, deliberately different jobs.
	case act() == keys.Search:
		a.Searching = true
		a.Status = ""
	case act() == keys.NextMatch:
		a.SearchStep(1)
	case act() == keys.PrevMatch:
		a.SearchStep(-1)

	// Collapse the run to just the matching lines, kept under their tasks.
	case act() == keys.FilterMatches:
		a.ToggleFilterMatches()

	// More or less context around each hit.
	case act() == keys.ContextMore:
		a.SetFilterContext(1)
	case act() == keys.ContextLess:
		a.SetFilterContext(-1)

	// Resume tracking whatever is running after you have gone looking around.
	case act() == keys.Follow:
		a.Following = !a.Following

	case act() == keys.History:
		a.OpenHistory()

	// Re-run the task under the cursor — the tight loop when you are fixing one broken
	// step. Note this is a fresh `task <name>`, not a resume of the parent.
	case act() == keys.Rerun:
		a.RerunSelected()

	// The same, minus go-task's up-to-date checks — the second thing you want when `r`
	// came back green without having run anything.
	case act() == keys.ForceRerun:
		a.ForceRerunSelected()

	// Everything that broke, at once. After a red `task all` this is the whole loop.
	case act() == keys.RerunFailed:
		a.RerunFailed()

	// Re-run with different arguments.
	case act() == keys.Args:
		if name, ok := a.RunSelectedTask(); ok {
			a.BeginArgs(name)
		}

	// Jump straight to a slot, as the bar numbers them. Last, so that rebinding an action
	// onto a digit still wins — the keymap is the thing users can change.
	case k.typed() && k.ch >= '1' && k.ch <= '9':
		a.FocusSlotNumber(int(k.ch - '0'))
	}
	return false
}
