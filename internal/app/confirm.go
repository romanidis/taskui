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
	// CallsProduction means the task is not on the list itself but calls one that is, at
	// some depth. `release` running `deploy:prod` is a production run whatever it is named.
	CallsProduction
)

// Confirm is something waiting on a yes.
//
// One mechanism rather than one per question: a confirmation takes over the whole keymap
// while it is up, and a second flag for the key handler to check is a second flag to
// forget to check.
//
// Each question is its own type, holding what it asks about and nothing else. They were one
// struct whose fields changed meaning with its kind: Name was a task for one question and a
// comma-joined list for another, and Live counted running runs for a quit and marked tasks
// for a batch.
//
//sumtype:decl
type Confirm interface{ isConfirm() }

// ConfirmRun is starting one task, once the reason has been answered.
type ConfirmRun struct {
	Name string
	Args []string
	// Interactive and Force are how it will be started, settled when the question was
	// asked: a re-run carries the flags of the run it repeats, not whatever is armed.
	Interactive bool
	Force       bool
	Reason      ConfirmReason
	// Calls is what CallsProduction is about: the tasks on the danger list this one calls.
	Calls []string
}

// ConfirmRunMarked is the marked set, started at once. Asking per task would put a prompt
// between each pair of starts, which is not a confirmation, it is an obstacle course.
type ConfirmRunMarked struct {
	// Names is the set, in the order it starts.
	Names []string
	// Dangerous is the part of it on the danger list, named in the question: the whole
	// reason this is one question rather than several is that they are in a batch with
	// tasks that are not.
	Dangerous []string
}

// ConfirmRerunFailed is the tasks that broke in the run on screen, started again at once —
// the same question as a marked batch, about a set the run chose rather than you.
type ConfirmRerunFailed struct {
	Names     []string
	Dangerous []string
}

// ConfirmQuit is leaving, and stopping the runs quitting is responsible for.
type ConfirmQuit struct {
	// Live is how many runs there were when the question was asked — it is what the prompt
	// says, and re-counting as runs finish under it would make the number move while you
	// read it.
	Live int
	// Detached is how many will survive it, counted at the same moment and for the same
	// reason.
	Detached int
}

// ConfirmStopAll is stopping every live slot without leaving.
type ConfirmStopAll struct {
	// Live is how many runs there were when the question was asked, counted then for the
	// same reason as a quit's.
	Live int
}

func (ConfirmRun) isConfirm()         {}
func (ConfirmRunMarked) isConfirm()   {}
func (ConfirmRerunFailed) isConfirm() {}
func (ConfirmQuit) isConfirm()        {}
func (ConfirmStopAll) isConfirm()     {}

// confirm is the question to ask before starting inv.
func (inv invocation) confirm(why ConfirmReason) ConfirmRun {
	return ConfirmRun{
		Name:        inv.name,
		Args:        append([]string(nil), inv.args...),
		Interactive: inv.interactive,
		Force:       inv.force,
		Reason:      why,
	}
}

func (c ConfirmRun) invocation() invocation {
	return invocation{name: c.Name, args: c.Args, interactive: c.Interactive, force: c.Force}
}

// ConfirmYes answers whatever is pending. It returns true if the answer was "quit".
func (a *App) ConfirmYes() bool {
	pending := a.Confirm
	a.Confirm = nil
	switch c := pending.(type) {
	case ConfirmRun:
		// "Restarting stops the one running" was the question; whether this task touches
		// production is a second one, and answering the first is not answering it.
		if c.Reason == WouldStopRunning {
			if next, ok := a.productionQuestion(c.invocation()); ok {
				a.Confirm = next
				return false
			}
		}
		if err := a.start(c.invocation()); err != nil {
			a.Status = fmt.Sprintf("could not start `task %s`: %v", c.Name, err)
		}
	case ConfirmRunMarked:
		a.startMarked(c.Names)
	case ConfirmRerunFailed:
		a.startBatch(c.Names)
	case ConfirmStopAll:
		a.StopAll()
	case ConfirmQuit:
		return true
	}
	return false
}

func (a *App) ConfirmNo() {
	pending := a.Confirm
	a.Confirm = nil
	switch pending.(type) {
	case ConfirmRun, ConfirmRunMarked, ConfirmRerunFailed:
		a.Status = "not run"
	case ConfirmQuit, ConfirmStopAll:
		a.Status = "left running"
	}
}
