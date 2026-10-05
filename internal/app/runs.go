package app

import (
	"errors"
	"fmt"

	"github.com/romanidis/taskui/internal/run"
	"github.com/romanidis/taskui/internal/store"
	"github.com/romanidis/taskui/internal/task"
)

// stopRun stops a run, escalating on a second ask, and says what happened.
//
// A free function rather than a method because every caller wants to write the answer into
// the app's status line, and the escalation is the whole point of it being one function:
// `x` on a wedged run should not be a key that does nothing the second time you press it —
// but neither should the first press be a SIGKILL, so the two live together where the
// order is obvious.
func stopRun(r *run.Run) string {
	if r.Finished() {
		return "that run has already finished"
	}
	if r.Killed() {
		return fmt.Sprintf("`%s` has had SIGKILL — nothing louder to send, waiting on the OS", r.Root)
	}
	if r.Cancelled() {
		r.Kill()
		return fmt.Sprintf("killed `%s` — SIGKILL to the process group", r.Root)
	}
	r.Cancel()
	return fmt.Sprintf("stopping `%s` — again to kill it outright", r.Root)
}

// StartRun kicks off `task <name>` and switches to the run view.
func (a *App) StartRun(name string) error {
	return a.StartRunWith(name, nil)
}

// ResumeRun returns to a run already in progress.
//
// `v` means take me to the thing that is going, so it seeks out a live slot rather than
// reopening whichever one you happened to leave last. It used to only flip the screen,
// which gave the one answer the key must never give: a run that finished twenty minutes
// ago, while the deploy you were asking about carries on in a slot you cannot see.
//
// A live focused slot stays put — there is no reason to move you off a run you are already
// watching — and otherwise it takes the earliest-started live slot, so pressing `v` twice
// lands in the same place instead of hopping between two running tasks. With nothing
// running at all it falls back to the last run, because "show me what just happened" is
// still the useful answer to a key that has nothing live to offer.
func (a *App) ResumeRun() bool {
	if a.Run == nil {
		a.Status = "no run to go back to"
		return false
	}
	if a.Run.Finished() {
		for _, s := range a.Slots() {
			if s.Status == run.Running {
				a.FocusSlot(s.Seq)
				break
			}
		}
	}
	a.Screen = ScreenRun
	a.Status = ""
	return true
}

// RequestRun runs a task, unless something needs a yes first.
//
// Starting a different task no longer disturbs what is already going: it parks the current
// run in its own slot and opens a new one. Starting the task that is already running still
// means "show me it" rather than "start a second one" — one slot per task name, so a
// second copy would have nowhere to live even if it were wanted.
func (a *App) RequestRun(name string, args []string) {
	a.requestRun(a.armed(name, args))
}

// invocation is one run to start: the task, its arguments, and the flags it goes out with.
//
// A value rather than the app's sticky toggles, because not every start is the next run
// you armed: `r` repeats a run the way it ran, and so does watch mode. Both used to do it
// by writing that run's flags into ForceNext and InteractiveNext on the way, so a plain `r`
// switched off a force you had set with `F`, and a watched task inherited the flags of the
// one watched before it.
type invocation struct {
	name        string
	args        []string
	interactive bool
	force       bool
}

// armed is a start with whatever `F` and `I` have armed.
func (a *App) armed(name string, args []string) invocation {
	return invocation{name: name, args: args, interactive: a.InteractiveNext, force: a.ForceNext}
}

// repeating is a start of name the way r ran.
func repeating(name string, args []string, r *run.Run) invocation {
	return invocation{name: name, args: args, interactive: r.Interactive, force: r.Force}
}

func (a *App) requestRun(inv invocation) {
	name := inv.name
	if a.liveSlot(name) {
		// Focus it, so `v` goes to the right one — but stay where you are. From the
		// picker the run is already on screen, under the row the cursor is on.
		screen := a.Screen
		a.focusTask(name)
		a.Screen = screen
		if screen == ScreenPicker {
			a.Status = "`" + name + "` is already running — `v` for the whole screen"
			a.RebuildPickerRows()
			return
		}
		a.ResumeRun()
		return
	}

	if a.Confirm == nil {
		if q, ok := a.productionQuestion(inv); ok {
			a.Confirm = q
			return
		}
	}
	a.Confirm = nil
	if err := a.start(inv); err != nil {
		a.Status = fmt.Sprintf("could not start `task %s`: %v", name, err)
	}
}

// StartRunWith starts a task with whatever `F` and `I` have armed, past every question.
func (a *App) StartRunWith(name string, args []string) error {
	return a.start(a.armed(name, args))
}

// isDangerous reports whether a task is on the danger list.
//
// A task the listing leaves out is still asked about: an `internal: true` task never appears
// in `task --list-all`, so `release` calling `deploy:apply` used to start without a word
// however plainly `.taskui-danger` said `deploy:*`.
func (a *App) isDangerous(name string) bool {
	for _, t := range a.Tasks {
		if t.Name == name {
			return t.Dangerous
		}
	}
	return task.Dangerous(name, task.DangerPatterns(a.Root))
}

// dangerCalled is the tasks on the danger list that name calls, at any depth.
//
// As far as the coverage walk knows, which is nothing until it lands: it is the one place
// every task's graph is resolved ahead of being run, and resolving one here, on the key
// press, would hold the screen still for as long as a `--summary` per task it calls takes.
func (a *App) dangerCalled(name string) []string {
	var out []string
	for _, called := range a.calls.Reachable(name) {
		if called != name && a.isDangerous(called) {
			out = append(out, called)
		}
	}
	return out
}

// touchesProduction reports whether starting name reaches the danger list at all: the task
// itself, or anything it calls.
func (a *App) touchesProduction(name string) bool {
	return a.isDangerous(name) || len(a.dangerCalled(name)) > 0
}

// productionQuestion is what starting inv has to ask about production, if anything.
func (a *App) productionQuestion(inv invocation) (ConfirmRun, bool) {
	if a.isDangerous(inv.name) {
		return inv.confirm(TouchesProduction), true
	}
	if calls := a.dangerCalled(inv.name); len(calls) > 0 {
		q := inv.confirm(CallsProduction)
		q.Calls = calls
		return q, true
	}
	return ConfirmRun{}, false
}

func (a *App) start(inv invocation) error {
	name, args := inv.name, inv.args
	seq, why := a.claimSlot(name)
	if why != "" {
		return errors.New(why)
	}
	r, err := run.Start(a.Root, name, args, inv.interactive, inv.force)
	if err != nil {
		return err
	}
	// Whatever was in this slot is being replaced. Rust relied on Drop to take its process
	// group with it; here it has to be said out loud, or a restart silently orphans the
	// run it was restarting.
	a.retire(a.Run)
	a.slot = newSlot(r, seq)
	a.Status = ""
	a.ClearSearch()
	a.RebuildRunRows()
	// Starting a run no longer takes the screen. The list stays where it was and the run
	// grows under the row it came from — which is what makes starting a second one, and a
	// third, something you can do without losing sight of the first.
	if a.Screen == ScreenPicker {
		a.Status = "running `" + name + "` — `v` for the whole screen"
	}
	a.RebuildPickerRows()
	a.emit()
	return nil
}

// retire takes a displaced run's process group with it.
func (a *App) retire(r *run.Run) {
	if r != nil && !r.Finished() {
		r.Cancel()
	}
}

// PollRun drains every slot's capture goroutine and refreshes the run view. It returns
// true if anything moved.
//
// Parked runs are drained too, and for the same reason they were parked rather than
// killed: a background run that is not being read is still a run, and letting its queue
// back up would lose the output you came back for.
func (a *App) PollRun() bool {
	moved := false
	for _, p := range a.Parked {
		if p.Run.Poll() {
			moved = true
			if p.Run.Finished() && a.needsSaving(p) {
				a.saveParked(p)
			}
		}
	}

	if a.Run == nil {
		if moved {
			// A parked run said something, and the picker is showing it under its task.
			a.RebuildPickerRows()
			a.emit()
		}
		return moved
	}
	if !a.Run.Poll() {
		if moved {
			a.RebuildPickerRows()
			a.emit()
		}
		return moved
	}
	a.follow()
	a.refreshSearch()
	a.RebuildRunRows()
	a.RebuildPickerRows()
	a.saveIfFinished()
	a.emit()
	return true
}

// saveParked archives a background run the moment it ends, and says so — otherwise the
// only way to find out your build finished is to go and look at it.
func (a *App) saveParked(p *slot) {
	name := p.Run.Root
	ok := p.Run.Outcome() == run.Ok
	if err := a.archive(p); err != nil {
		a.Status = fmt.Sprintf("could not save `task %s`: %v", name, err)
		return
	}
	mark := "✗"
	if ok {
		mark = "✓"
	}
	a.Status = fmt.Sprintf("%s `task %s` finished in the background", mark, name)
}

// needsSaving reports whether a slot's finished run still has to be archived: never saved,
// or saved only as far as it had got when it was detached.
func (a *App) needsSaving(s *slot) bool {
	return s.SavedTo == "" || a.partial[s.Run]
}

// archive saves a slot's finished run, rewriting the partial record a detach left rather
// than adding a second one beside it.
func (a *App) archive(s *slot) error {
	var path string
	var err error
	if s.SavedTo == "" || !a.partial[s.Run] {
		path, err = store.Save(a.stateDir, a.Root, s.Run)
	} else {
		delete(a.partial, s.Run)
		path, err = store.Resave(a.stateDir, s.SavedTo, a.Root, s.Run)
	}
	if err != nil {
		return err
	}
	s.SavedTo = path
	// The picker's ✓/✗ column is only useful if it is current.
	a.Outcomes = store.LastOutcomes(a.stateDir, a.Root)
	return nil
}

// saveIfFinished persists the run once, the moment it ends.
func (a *App) saveIfFinished() {
	if a.Run == nil || !a.Run.Finished() || !a.needsSaving(a.slot) {
		return
	}
	if err := a.archive(a.slot); err != nil {
		a.Status = fmt.Sprintf("could not save this run: %v", err)
		return
	}
	masked := a.Run.RedactedSecrets
	switch masked {
	case 0:
		a.Status = "saved — no dotenv values found to mask"
	case 1:
		a.Status = "saved — 1 secret masked"
	default:
		a.Status = fmt.Sprintf("saved — %d secrets masked", masked)
	}
}

func (a *App) ToggleForce() {
	a.ForceNext = !a.ForceNext
	if a.ForceNext {
		a.Status = "force: the next run ignores up-to-date checks — again to turn it off"
	} else {
		a.Status = "force off"
	}
}

func (a *App) ToggleInteractive() {
	a.InteractiveNext = !a.InteractiveNext
	if a.InteractiveNext {
		a.Status = "interactive: the next run can be typed at, but output is attributed by command"
	} else {
		a.Status = "interactive off"
	}
}

// CancelRun stops the run on screen, escalating if it has already been asked once.
func (a *App) CancelRun() {
	if a.Run == nil {
		return
	}
	a.Status = stopRun(a.Run)
}

// CancelTask stops the run in this task's slot, wherever that slot is.
//
// The point of addressing a run by task name is that it reaches the ones you are not
// looking at: from the picker there is no run "on screen" at all, and switching to a slot
// merely to stop it means loading a 20,000-line buffer to press one key.
func (a *App) CancelTask(name string) {
	if r := a.slotRun(name); r != nil {
		a.Status = stopRun(r)
		return
	}
	a.Status = fmt.Sprintf("`%s` is not running", name)
}

// RequestStopAll stops every live slot without leaving. It asks first — this reaches runs
// that are not on screen, and a stack you deliberately left up is exactly what it would
// take down.
func (a *App) RequestStopAll() {
	live := a.InFlightCount()
	if live == 0 {
		a.Status = "nothing is running"
		return
	}
	a.Confirm = ConfirmStopAll{Live: live}
}

// StopAll stops every live slot, reporting what was reached.
func (a *App) StopAll() {
	live := a.InFlightCount()
	a.CancelAll()
	switch live {
	case 0:
		a.Status = "nothing is running"
	case 1:
		a.Status = "stopping 1 run"
	default:
		a.Status = fmt.Sprintf("stopping %d runs", live)
	}
}

// RunInFlight is true while the run on screen is still going.
func (a *App) RunInFlight() bool { return a.Run != nil && !a.Run.Finished() }

// AnyInFlight is true while a run quitting is responsible for is still going: quitting
// without dealing with it would leave containers running with nothing watching them. A
// detached run does not count, because it is going to outlive the wait by design.
func (a *App) AnyInFlight() bool { return len(a.attachedRuns()) > 0 }

// AnyRunning includes the detached ones — for anything asking "is something happening",
// which is a different question from "is something holding up the exit".
func (a *App) AnyRunning() bool {
	if a.RunInFlight() {
		return true
	}
	for _, p := range a.Parked {
		if !p.Run.Finished() {
			return true
		}
	}
	return false
}

// InFlightCount is how many runs quitting would stop, for the quit prompt.
func (a *App) InFlightCount() int { return len(a.attachedRuns()) }

// CancelAll stops everything quitting is responsible for, on the way out — which is not
// everything. `x` still reaches a detached run; this is the blanket that no longer covers it.
func (a *App) CancelAll() {
	for _, s := range a.attachedRuns() {
		s.Run.Cancel()
	}
}

// KillAll SIGKILLs every slot still standing. Only used on the way out, once SIGTERM has
// been sent and given time to work: a process that ignored it is about to be orphaned, and
// an orphaned container is worse than a skipped cleanup handler.
func (a *App) KillAll() {
	for _, s := range a.attachedRuns() {
		s.Run.Kill()
	}
}

// RerunSelected re-runs the task under the cursor, keeping the args the run was started
// with.
func (a *App) RerunSelected() { a.rerunSelectedWith(false) }

// ForceRerunSelected is `⇧R`: the same re-run, but with `--force`.
//
// The tight loop when you are fixing one broken step is `r`, and `r` inherits the original
// run's flags — so a task go-task considers up to date declines to run again and you get a
// green tick that proves nothing. Reaching for the picker's `F` means leaving the output
// you are working against. This is the same key with the checks off, which is what you
// wanted the second time you pressed `r`.
//
// It also arms force for what you start next, and the header says so: having needed the
// checks off once, the next thing you start from the picker usually needs them off too.
func (a *App) ForceRerunSelected() {
	a.ForceNext = true
	a.rerunSelectedWith(true)
}

// force is an override, not a setting: false still inherits whatever the run used, so
// plain `r` keeps re-running a forced run forced.
func (a *App) rerunSelectedWith(force bool) {
	name, ok := a.RunSelectedTask()
	if !ok {
		return
	}
	var args []string
	// Only the root was invoked with these args; a child was not.
	if a.Run != nil && a.Run.Root == name {
		args = a.Run.Args
	}
	// Re-run it the way it was run: non-interactively it would hang again, and without
	// `--force` a cached task would simply decline.
	inv := a.armed(name, args)
	if a.Run != nil {
		inv = repeating(name, args, a.Run)
		inv.force = inv.force || force
	}
	a.restart(inv)
}

// restart starts inv, asking first when its slot is still live.
//
// Re-running a task whose slot is still live means restarting it, and a restart kills what
// is in there. On a stack you deliberately left up that is worth a yes — and it is the only
// way to bounce one without stopping it by hand first. Going through requestRun instead
// only focused the live run, which is why `⇧I` on a task stuck at a hidden prompt — exactly
// what it is for — did nothing.
func (a *App) restart(inv invocation) {
	if a.liveSlot(inv.name) {
		a.Confirm = inv.confirm(WouldStopRunning)
		return
	}
	a.requestRun(inv)
}

// InteractiveRerun is `⇧I`: this run again, interactively, so a prompt it is waiting on
// can be seen and answered.
func (a *App) InteractiveRerun() {
	if a.Run == nil {
		return
	}
	inv := repeating(a.Run.Root, a.Run.Args, a.Run)
	inv.interactive = true
	// Armed as well, as `⇧R` arms force: a task that needed its prompt seen once will
	// need it again.
	a.InteractiveNext = true
	a.restart(inv)
}

// RunSelectedTask is the task under the cursor, whether the cursor is on it or on one of
// its lines.
func (a *App) RunSelectedTask() (string, bool) {
	if a.RunCursor >= len(a.RunRows) {
		return "", false
	}
	row := a.RunRows[a.RunCursor]
	if row.IsTask {
		return row.Name, true
	}
	return row.Task, true
}
