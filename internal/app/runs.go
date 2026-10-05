package app

import (
	"errors"
	"fmt"

	"github.com/romanidis/taskui/internal/run"
	"github.com/romanidis/taskui/internal/task"
)

// stopRun stops a run one step louder than last time, and says what that step was.
func stopRun(r *run.Run) string {
	switch r.Stop() {
	case run.StopOver:
		return "that run has already finished"
	case run.StopNothingLouder:
		return fmt.Sprintf("`%s` has had SIGKILL — nothing louder to send, waiting on the OS", r.Task)
	case run.StopKilled:
		return fmt.Sprintf("killed `%s` — SIGKILL to the process group", r.Task)
	default:
		return fmt.Sprintf("stopping `%s` — again to kill it outright", r.Task)
	}
}

// StartRun kicks off `task <name> <args>` with whatever `F` and `I` have armed, past every
// question, and switches to the run view.
func (a *App) StartRun(name string, args []string) error {
	return a.start(a.armed(name, args))
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

// armed is a start with whatever `F` and `I` have armed.
func (a *App) armed(name string, args []string) run.Invocation {
	return run.Invocation{Task: name, Args: args, Interactive: a.InteractiveNext, Force: a.ForceNext}
}

func (a *App) requestRun(inv run.Invocation) {
	name := inv.Task
	if a.liveSlot(name) {
		// Focus it, so `v` goes to the right one — but stay where you are. From the
		// picker the run is already on screen, under the row the cursor is on.
		screen := a.Screen
		a.FocusSlot(a.taskSlot(name).Seq)
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

// productionReach is how starting name reaches the danger list: whether it is on it itself,
// and the tasks on it that name calls, at any depth.
//
// A task the listing leaves out is still asked about: an `internal: true` task never appears
// in `task --list-all`, so `release` calling `deploy:apply` used to start without a word
// however plainly `.taskui-danger` said `deploy:*`.
//
// The calls are as far as the coverage walk knows, which is nothing until it lands: it is the
// one place every task's graph is resolved ahead of being run, and resolving one here, on the
// key press, would hold the screen still for as long as a `--summary` per task it calls takes.
func (a *App) productionReach(name string) (bool, []string) {
	onList := func(name string) bool {
		for _, t := range a.Tasks {
			if t.Name == name {
				return t.Dangerous
			}
		}
		return task.Dangerous(name, task.DangerPatterns(a.Root))
	}
	var calls []string
	for _, called := range a.calls.Reachable(name) {
		if called != name && onList(called) {
			calls = append(calls, called)
		}
	}
	return onList(name), calls
}

// productionQuestion is what starting inv has to ask about production, if anything.
func (a *App) productionQuestion(inv run.Invocation) (ConfirmRun, bool) {
	itself, calls := a.productionReach(inv.Task)
	switch {
	case itself:
		return ConfirmRun{Invocation: inv, Reason: TouchesProduction}, true
	case len(calls) > 0:
		return ConfirmRun{Invocation: inv, Reason: CallsProduction, Calls: calls}, true
	}
	return ConfirmRun{}, false
}

func (a *App) start(inv run.Invocation) error {
	name := inv.Task
	seq, why := a.claimSlot(name)
	if why != "" {
		return errors.New(why)
	}
	r := run.Start(a.Root, inv)
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
			a.archiveIfFinished(p)
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
	a.Follow()
	a.refreshSearch()
	a.RebuildRunRows()
	a.RebuildPickerRows()
	a.archiveIfFinished(a.slot)
	a.emit()
	return true
}

// archiveIfFinished saves a slot's run the moment it ends, once, and says so. On screen,
// that is how many secrets were masked; in the background it is how the run went, since
// otherwise the only way to find out your build finished is to go and look at it.
//
// A run a detach saved partway is rewritten whole, rather than given a second record beside
// the first.
func (a *App) archiveIfFinished(s *slot) {
	partial := a.partial[s.Run]
	if !s.Run.Finished() || (s.SavedTo != "" && !partial) {
		return
	}
	var path string
	var err error
	if partial {
		delete(a.partial, s.Run)
		path, err = a.archive.Resave(s.SavedTo, a.Root, s.Run)
	} else {
		path, err = a.archive.Save(a.Root, s.Run)
	}
	if err != nil {
		a.Status = fmt.Sprintf("could not save `task %s`: %v", s.Run.Task, err)
		return
	}
	s.SavedTo = path
	// The picker's ✓/✗ column is only useful if it is current.
	a.ReloadOutcomes()

	if s != a.slot {
		a.Status = fmt.Sprintf("%s `task %s` finished in the background", s.Run.Outcome().Glyph(), s.Run.Task)
		return
	}
	switch masked := s.Run.RedactedSecrets; masked {
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
	inv := a.armed(name, nil)
	if a.Run != nil {
		inv = a.Run.Rerun(name)
		inv.Force = inv.Force || force
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
func (a *App) restart(inv run.Invocation) {
	if a.liveSlot(inv.Task) {
		a.Confirm = ConfirmRun{Invocation: inv, Reason: WouldStopRunning}
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
	inv := a.Run.Invocation
	inv.Interactive = true
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
