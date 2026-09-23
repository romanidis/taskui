package app

import (
	"github.com/romanidis/taskui/internal/keys"
)

// handlePickerKey is the picker's dispatch table. One flat table per screen is the point:
// splitting it would hide the order the arms are tried in, which is what makes a rebound
// key shadow a literal.
//
//nolint:cyclop // a dispatch table, read top to bottom; see above
func (a *App) handlePickerKey(k Key) bool {
	act := func() keys.Action { return a.action(k, ScreenPicker) }

	switch {
	// `esc` backs out of things — a filter, a jump, a panel, a run — and both of those are
	// handled before this point. Landing here means there was nothing left to back out of,
	// and the answer to that is not "close the tool". The second press in a row says where
	// the exit is, because a key that does nothing at all reads as broken.
	case k.kind == keyEsc:
		a.EscStreak++
		if a.EscStreak > 1 {
			a.Status = "nothing left to leave — press q to quit"
		}

	// Vim's paragraph keys, over the tree's groups: `}` past whatever is under this group
	// to the next header, `{` back to the previous one.
	case k.isChar('}'):
		a.MoveGroup(1)
	case k.isChar('{'):
		a.MoveGroup(-1)

	// The pivot. Selection, filter and the other mode's folds all survive it. On `p`, not
	// `g`: `gg` belongs to vim.
	case act() == keys.Pivot:
		a.ToggleMode()

	// The other half of the same question: `p` is what the tree is, `⇧S` is what sits above
	// what inside it.
	case act() == keys.Order:
		a.CycleOrder()

	// Space folds, enter runs — kept strictly separate. A node that is both a group and a
	// task (`backend:migrate`) is then runnable from its own header, so its subtree never
	// has to relist it just to make it reachable.
	//
	// With a run unfolded under a task there are two things a fold key could mean, so
	// there are two keys: `space` is the tree and `o` is the output. Each falls through to
	// the other where it has nothing of its own to fold, which is most rows — a leaf task
	// has no group to open, and a namespace with nothing running has no output.
	case k.isChar(' '):
		if n := a.SelectedNode(); n != nil && n.IsGroup() && !a.CursorInRun() {
			a.ToggleFold()
		} else {
			a.CycleOutputFold()
		}
	case k.kind == keyEnter:
		// Marks first: having chosen a set, `⏎` means run the set. Running whatever the
		// cursor happens to be on instead would quietly discard the choice.
		if len(a.marked) > 0 {
			a.RunMarked()
		} else if ti := a.SelectedTask(); ti >= 0 {
			a.RequestRun(a.Tasks[ti].Name, nil)
		} else {
			what := ""
			if n := a.SelectedNode(); n != nil {
				what = n.Label
			}
			a.Status = "`" + what + "` groups tasks but is not one — space folds it"
		}
	case k.kind == keyRight, k.kind == keyLeft:
		if n := a.SelectedNode(); n != nil && n.IsGroup() {
			a.ToggleFold()
		}

	case k.kind == keyTab, act() == keys.FoldAll:
		a.ToggleFoldAll()
	case act() == keys.Fold:
		if !a.CycleOutputFold() {
			if n := a.SelectedNode(); n != nil && n.IsGroup() {
				a.ToggleFold()
			}
		}

	case act() == keys.Filter:
		a.Filtering = true
		a.Status = ""

	// Jump rather than filter: the list stays whole and only the cursor moves.
	case act() == keys.Jump:
		a.BeginJump()

	// What is this task, and what will it actually run?
	case act() == keys.Detail:
		a.OpenDetail()

	// Run with arguments. Half a real Taskfile needs them.
	case act() == keys.Args:
		if ti := a.SelectedTask(); ti >= 0 {
			a.BeginArgs(a.Tasks[ti].Name)
		} else {
			a.Status = "nothing to run here — space folds it"
		}

	// Let the next run ask questions.
	case act() == keys.Interactive:
		a.ToggleInteractive()

	// Ignore go-task's up-to-date checks on the next run.
	case act() == keys.Force:
		a.ToggleForce()

	// Re-run whenever the source changes — the marked set if there is one, which is the
	// half of this that only the picker can offer, because marks are made here.
	case act() == keys.Watch:
		a.ToggleWatch()

	// Back to whatever is still running.
	case act() == keys.ResumeRun:
		a.ResumeRun()

	// Past runs.
	case act() == keys.History:
		a.OpenHistory()

	// How this one task has been going — the other half of `h`, scoped to what is under
	// the cursor rather than to the project.
	case act() == keys.Timeline:
		a.OpenTimeline(a.TimelineTaskFor())

	// In a run `e` opens the file an error named; here there is no error, so it opens the
	// thing you are actually looking at — the task's own definition.
	case act() == keys.Edit:
		a.EditDefinition(a.TimelineTaskFor())

	// Stop the run belonging to the task under the cursor. Addressing it by name is what
	// makes this reach the slots that are not on screen — which, from here, is all of them.
	case act() == keys.Stop:
		if ti := a.SelectedTask(); ti >= 0 {
			a.CancelTask(a.Tasks[ti].Name)
		} else {
			a.Status = "nothing to stop here — space folds it"
		}

	case act() == keys.StopAll:
		a.RequestStopAll()

	// Choose a set, then start it. Slots already hold several runs; this is how you fill
	// them without three trips back through the list.
	case act() == keys.Mark:
		a.ToggleMark()
	case act() == keys.ClearMarks:
		a.ClearMarks()
	}
	return false
}
