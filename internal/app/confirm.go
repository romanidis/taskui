package app

import (
	"fmt"
)

// ConfirmReason says why a run is waiting for a yes.
type ConfirmReason int

const (
	// TouchesProduction means the task is on the `.taskui-danger` list.
	TouchesProduction ConfirmReason = iota
	// WouldStopRunning means this task's slot already holds a live run, and restarting it
	// kills that one.
	WouldStopRunning
)

type ConfirmKind int

const (
	ConfirmRun ConfirmKind = iota
	ConfirmQuit
	ConfirmStopAll
	// ConfirmRunMarked is a whole batch at once. Asking per task would put a prompt between
	// each pair of starts, which is not a confirmation, it is an obstacle course.
	ConfirmRunMarked
)

// Confirm is something waiting on a yes.
//
// One mechanism rather than one per question: a confirmation takes over the whole keymap
// while it is up, and a second flag for the key handler to check is a second flag to
// forget to check.
type Confirm struct {
	Kind ConfirmKind
	// Name and Args belong to ConfirmRun: start this task, once the reason has been
	// answered.
	Name string
	Args []string
	// Interactive and Force are how it will be started, settled when the question was
	// asked: a re-run carries the flags of the run it repeats, not whatever is armed.
	Interactive bool
	Force       bool
	Reason      ConfirmReason
	// Live is how many runs there were when a quit or stop-all question was asked — it is
	// what the prompt says, and re-counting as runs finish under it would make the number
	// move while you read it.
	Live int
	// Detached is how many will survive it, counted at the same moment and for the same
	// reason.
	Detached int
}

// confirm is the question to ask before starting inv.
func (inv invocation) confirm(why ConfirmReason) *Confirm {
	return &Confirm{
		Kind:        ConfirmRun,
		Name:        inv.name,
		Args:        append([]string(nil), inv.args...),
		Interactive: inv.interactive,
		Force:       inv.force,
		Reason:      why,
	}
}

func (c *Confirm) invocation() invocation {
	return invocation{name: c.Name, args: c.Args, interactive: c.Interactive, force: c.Force}
}

// ConfirmYes answers whatever is pending. It returns true if the answer was "quit".
func (a *App) ConfirmYes() bool {
	pending := a.Confirm
	a.Confirm = nil
	if pending == nil {
		return false
	}
	switch pending.Kind {
	case ConfirmRun:
		// "Restarting stops the one running" was the question; whether this task touches
		// production is a second one, and answering the first is not answering it.
		if pending.Reason == WouldStopRunning && a.isDangerous(pending.Name) {
			a.Confirm = pending.invocation().confirm(TouchesProduction)
			return false
		}
		if err := a.start(pending.invocation()); err != nil {
			a.Status = fmt.Sprintf("could not start `task %s`: %v", pending.Name, err)
		}
		return false
	case ConfirmStopAll:
		a.StopAll()
		return false
	case ConfirmRunMarked:
		a.startMarked(a.Marked())
		return false
	default:
		return true
	}
}

func (a *App) ConfirmNo() {
	pending := a.Confirm
	a.Confirm = nil
	if pending == nil {
		return
	}
	switch pending.Kind {
	case ConfirmRun, ConfirmRunMarked:
		a.Status = "not run"
	default:
		a.Status = "left running"
	}
}
