package app

import (
	"fmt"
	"sort"

	"github.com/romanidis/taskui/internal/run"
)

// ToggleMark marks or unmarks the task under the cursor.
//
// Slots already hold several runs at once, and the whole point of them is that a run
// outlives your attention — but getting three going meant three trips back through the
// picker, remembering which two you had already started. Marking is the missing half:
// choose the set, then start it.
func (a *App) ToggleMark() {
	ti := a.SelectedTask()
	if ti < 0 {
		a.Status = "`" + a.selectedLabel() + "` groups tasks but is not one — space folds it"
		return
	}
	name := a.Tasks[ti].Name
	if a.marked == nil {
		a.marked = map[string]bool{}
	}
	if a.marked[name] {
		delete(a.marked, name)
	} else {
		a.marked[name] = true
	}
	// The bar says how many and what the keys are while any are held; this only has
	// something to say when it goes away.
	a.Status = ""
	if len(a.marked) == 0 {
		a.Status = "no tasks marked"
	}
}

func (a *App) selectedLabel() string {
	if n := a.SelectedNode(); n != nil {
		return n.Label
	}
	return ""
}

// Marked is the set, by name: stable between frames, and the order a batch starts them in.
// Not the picker's order, which moves with the pivot and the sort and leaves out whatever a
// filter is hiding — and a marked task stays marked while it is filtered away.
func (a *App) Marked() []string {
	out := make([]string, 0, len(a.marked))
	for name := range a.marked {
		out = append(out, name)
	}
	sort.Strings(out)
	return out
}

func (a *App) IsMarked(name string) bool { return a.marked[name] }

func (a *App) ClearMarks() {
	if len(a.marked) == 0 {
		return
	}
	a.marked = nil
	a.Status = "marks cleared"
}

// RunMarked starts every marked task at once, each in its own slot, with whatever `F` and
// `I` have armed.
func (a *App) RunMarked() {
	var runs []run.Invocation
	for _, name := range a.Marked() {
		runs = append(runs, a.armed(name, nil))
	}
	if len(runs) > 0 {
		a.requestRunSet(RunSet{Runs: runs, Marked: true})
	}
}

// RunSet is several tasks started at once, each in its own slot: the marked set, or the
// tasks that failed in the run on screen. Each goes out with its own arguments and flags.
type RunSet struct {
	Runs []run.Invocation
	// Marked says the set is the marks, which are spent once it has gone out. A run's
	// failures leave the marks alone: they are a set you chose, and what broke in a run is
	// not that set.
	Marked bool
}

// String is the set the way the footer names it: "3 marked tasks", "1 failed task".
func (s RunSet) String() string {
	kind := "failed"
	if s.Marked {
		kind = "marked"
	}
	return fmt.Sprintf("%d %s %s", len(s.Runs), kind, plural(len(s.Runs), "task", "tasks"))
}

// requestRunSet starts a RunSet, unless none of it can start or something needs a yes first.
//
// Capped at the slots there are, and it says what it left behind rather than silently
// starting the first six: a set that quietly did less than you asked is worse than one that
// refused. Anything in it that touches production, itself or through something it calls,
// turns the whole set into one question. Asking per task would put a modal prompt between
// each pair of starts, which is not a confirmation, it is an obstacle course.
func (a *App) requestRunSet(set RunSet) {
	var dangerous []string
	toStart, canStart := 0, 0
	for _, inv := range set.Runs {
		if itself, calls := a.productionReach(inv.Task); itself || len(calls) > 0 {
			dangerous = append(dangerous, inv.Task)
		}
		// Only a live task is left alone: one in a finished slot reuses it.
		if a.liveSlot(inv.Task) {
			continue
		}
		toStart++
		if a.slotAvailable(inv.Task) {
			canStart++
		}
	}

	switch {
	case toStart == 0:
		// Said before the slots are: a set that is all going needs no slot, and "every slot
		// is taken" sent you to close one for nothing.
		a.Status = "those are all running already"
	case canStart == 0:
		a.Status = fmt.Sprintf("every slot is taken — ⇧X closes one (%s)", set)
	case len(dangerous) > 0:
		a.Confirm = ConfirmRunSet{Set: set, Dangerous: dangerous}
	default:
		a.startRunSet(set)
	}
}

// startRunSet starts a RunSet past every question, and spends the marks if it was them.
//
// Each task is asked about as it comes rather than against a count taken up front: the
// count kept disagreeing with what claiming a slot actually does, and "started 1 task"
// with nothing started was the result.
func (a *App) startRunSet(set RunSet) {
	started, skipped := 0, 0
	for _, inv := range set.Runs {
		if a.liveSlot(inv.Task) {
			continue
		}
		if !a.slotAvailable(inv.Task) {
			skipped++
			continue
		}
		if err := a.start(inv); err != nil {
			a.Status = fmt.Sprintf("could not start `task %s`: %v", inv.Task, err)
			return
		}
		started++
	}
	if set.Marked {
		a.marked = nil
	}

	switch {
	case started == 0:
		a.Status = "those are all running already"
	case skipped > 0:
		a.Status = fmt.Sprintf("started %d — %d left unstarted, no slots free", started, skipped)
	default:
		a.Status = fmt.Sprintf("started %d %s", started, plural(started, "task", "tasks"))
	}
}

// RerunFailed starts everything in this run that broke, each in its own slot.
//
// The tightest loop there is after a big red run: `task all` fails in three places, you fix
// them, and what you want is those three — not the whole pipeline again, and not three trips
// back through the tree. The run already knows exactly which they were.
//
// It starts the tasks that actually failed rather than the ones merely reported as failing:
// an aggregate is failed because its child was, and re-running the aggregate would run
// everything, which is what this exists to avoid.
func (a *App) RerunFailed() {
	if a.Run == nil {
		a.Status = "no run to take the failures from"
		return
	}
	failed := a.Run.Culprits()
	if len(failed) == 0 {
		if a.Run.Outcome() == run.Ok {
			a.Status = "nothing in `" + a.Run.Command() + "` failed"
		} else {
			a.Status = "nothing has failed yet"
		}
		return
	}

	// Each failure goes out the way it ran in the run it failed in, and the task that run was
	// started as gets its arguments back. Started bare and with whatever was armed instead,
	// `deploy ENV=staging` failing came back as `task deploy`.
	reruns := make([]run.Invocation, 0, len(failed))
	for _, name := range failed {
		rerun := run.Invocation{Task: name, Force: a.Run.Force, Interactive: a.Run.Interactive}
		if name == a.Run.Task {
			rerun.Args = a.Run.Args
		}
		reruns = append(reruns, rerun)
	}
	// The same production question a marked set gets. A `deploy:prod` that failed inside
	// `release` is still `deploy:prod`, and this key is one keypress from the run it failed in.
	a.requestRunSet(RunSet{Runs: reruns})
}
