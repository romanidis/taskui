package app

import (
	"fmt"

	"github.com/romanidis/taskui/internal/run"
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
	// Invocation is the start being asked about, settled when the question was asked: a
	// re-run carries the flags of the run it repeats, not whatever is armed.
	run.Invocation

	Reason ConfirmReason
	// Calls is what CallsProduction is about: the tasks on the danger list this one calls.
	Calls []string
}

// ConfirmRunSet is several tasks started at once, the marked set or the failures of the
// run on screen, asked about as one question.
type ConfirmRunSet struct {
	Set RunSet
	// Dangerous is the part of the set that reaches production, named in the question: the
	// whole reason this is one question rather than several is that they are in a set with
	// tasks that are not.
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

func (ConfirmRun) isConfirm()     {}
func (ConfirmRunSet) isConfirm()  {}
func (ConfirmQuit) isConfirm()    {}
func (ConfirmStopAll) isConfirm() {}

// ConfirmYes answers whatever is pending. It returns true if the answer was "quit".
func (a *App) ConfirmYes() bool {
	pending := a.Confirm
	a.Confirm = nil
	switch c := pending.(type) {
	case ConfirmRun:
		// "Restarting stops the one running" was the question; whether this task touches
		// production is a second one, and answering the first is not answering it.
		if c.Reason == WouldStopRunning {
			if next, ok := a.productionQuestion(c.Invocation); ok {
				a.Confirm = next
				return false
			}
		}
		if err := a.start(c.Invocation); err != nil {
			a.Status = fmt.Sprintf("could not start `task %s`: %v", c.Task, err)
		}
	case ConfirmRunSet:
		a.startRunSet(c.Set)
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
	case ConfirmRun, ConfirmRunSet:
		a.Status = "not run"
	case ConfirmQuit, ConfirmStopAll:
		a.Status = "left running"
	}
}

// handleConfirmKey: something is waiting on a yes; nothing else gets through until it is
// answered.
func (a *App) handleConfirmKey(k Key) bool {
	// Only ConfirmYes knows what was being asked, and only the quit answer ends the loop —
	// so the teardown hangs off its return value rather than off the key.
	if k.isChar('y') || k.isChar('Y') {
		return a.ConfirmYes()
	}
	a.ConfirmNo()
	return false
}
