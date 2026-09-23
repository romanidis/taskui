package app

import (
	"fmt"
	"path/filepath"

	"github.com/romanidis/taskui/internal/watch"
)

// ToggleWatch watches the project and re-runs what is watched whenever something changes.
//
// What gets watched is the marked set if there is one, and otherwise the single task this
// screen is about — the run you are looking at, or the row under the cursor in the picker.
// Marks win because they are the more deliberate statement: you do not mark three tasks by
// accident, and a `check` that is three tasks is exactly the loop this replaces.
//
// The marks are left standing rather than consumed the way `⏎` consumes them. `⏎` starts a
// set once, so spending them is right; this arms a mode that keeps referring to them, and a
// set that vanished the moment it was armed could not be seen or corrected.
func (a *App) ToggleWatch() {
	if len(a.Watching) > 0 {
		a.Watching = nil
		if a.watcher != nil {
			a.watcher.Close()
			a.watcher = nil
		}
		a.Status = "watch off"
		return
	}

	names := a.Marked()
	if len(names) == 0 {
		name, ok := a.watchTarget()
		if !ok {
			return
		}
		names = []string{name}
	}

	w, err := watch.Start(a.Root)
	if err != nil {
		a.Status = fmt.Sprintf("could not watch this directory: %v", err)
		return
	}
	a.watcher = w
	a.Watching = names
	if len(names) == 1 {
		a.Status = fmt.Sprintf("watching — `task %s` re-runs when files change", names[0])
		return
	}
	a.Status = fmt.Sprintf("watching — %d marked tasks re-run when files change", len(names))
}

// watchTarget is the one task to watch when nothing is marked.
func (a *App) watchTarget() (string, bool) {
	if a.Screen == ScreenPicker {
		if ti := a.SelectedTask(); ti >= 0 {
			return a.Tasks[ti].Name, true
		}
		a.Status = "nothing to watch here — space folds it"
		return "", false
	}
	if a.Run == nil {
		a.Status = "nothing to watch — run something first"
		return "", false
	}
	return a.Run.Root, true
}

// WatchLabel names what is being watched, for the header.
func (a *App) WatchLabel() string {
	switch len(a.Watching) {
	case 0:
		return ""
	case 1:
		return a.Watching[0]
	default:
		return fmt.Sprintf("%d tasks", len(a.Watching))
	}
}

// PollWatch re-runs if the watcher has settled on a change.
//
// A set is started the way the marked set is started: what fits in the free slots goes, and
// what does not is said out loud. Silently watching four of six tasks would be a mode that
// lies about what it is doing — and with a set larger than the slots, the tasks that lost
// are the ones you would never see fail.
func (a *App) PollWatch() bool {
	if len(a.Watching) == 0 || a.watcher == nil {
		return false
	}
	changed, ok := a.watcher.Poll()
	if !ok {
		return false
	}

	started, skipped := 0, 0
	for _, name := range a.Watching {
		// Never stack a task on top of itself: a save during a build would otherwise kill
		// the build that is already checking the previous save. Scoped to the watched
		// task's own slot — something else running in another slot is not this one's
		// business, which is the whole point of the slots.
		if a.liveSlot(name) {
			continue
		}
		if !a.slotAvailable(name) {
			skipped++
			continue
		}

		// The way it last ran, if it has; otherwise the way `F` and `I` are armed.
		inv := a.armed(name, nil)
		if r := a.slotRun(name); r != nil {
			inv = repeating(name, r.Args, r)
		}

		// Deliberately bypasses the confirmation: watch mode is opt-in, on tasks you chose,
		// and a `y` prompt firing on every keystroke would be unusable. Which is also why
		// arming it on a production task is a bad idea.
		if err := a.start(inv); err != nil {
			a.Status = fmt.Sprintf("could not re-run `task %s`: %v", name, err)
			return false
		}
		started++
	}

	if started == 0 && skipped == 0 {
		return false
	}
	what := filepath.Base(changed) + " changed — re-running"
	if skipped > 0 {
		what = fmt.Sprintf("%s changed — re-ran %d, %d had no free slot", filepath.Base(changed), started, skipped)
	}
	a.Status = what
	return started > 0
}
