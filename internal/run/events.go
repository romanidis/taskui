package run

import (
	"sync"

	"github.com/romanidis/taskui/internal/graph"
	"github.com/romanidis/taskui/internal/task"
)

// Event is what the capture goroutine sends back to the UI.
type Event interface{ event() }

// GraphReady arrives first: resolving the graph costs one `task --summary` per node — over
// a second for a large aggregate — so it happens on the worker goroutine and arrives here
// rather than freezing the UI at the moment you press enter.
type GraphReady struct{ Graph graph.Graph }

// Partial is output with no newline yet — an interactive prompt. `Do you want to proceed?
// (y/n) ` never terminates its line, so a strictly line-based reader shows nothing at all
// and the run just appears to hang. Task is the one its tag named, empty when it had none.
type Partial struct{ Task, Text string }

// Redacting says how many distinct secrets the redactor is masking, so the UI can say
// whether output has been through it.
type Redacting struct{ N int }

// Naming carries the project's task names, ahead of the graph and the output that are
// both spelled by them. See task.Names for why a run cannot just use what it is given.
type Naming struct{ Names task.Names }

// LineEvent is one complete line. Task is empty when the line carried no `[name]` tag.
type LineEvent struct {
	Task      string
	Raw       string
	IsCommand bool
}

// FailedEvent is go-task reporting a specific task as the failure.
type FailedEvent struct{ Task string }

// Exited ends the run.
type Exited struct{ Code int }

func (GraphReady) event()  {}
func (Partial) event()     {}
func (Redacting) event()   {}
func (Naming) event()      {}
func (LineEvent) event()   {}
func (FailedEvent) event() {}
func (Exited) event()      {}

// Skipping records why go-task declined to run a task.
type Skipping struct {
	Task string
	Why  string
}

func (Skipping) event() {}

// queue is an unbounded event queue: the capture goroutine must never block on a UI that
// is between frames, and a bounded channel would eventually wedge the pty read — which
// wedges the child writing into it.
type queue struct {
	mu    sync.Mutex
	items []Event
}

func (q *queue) push(e Event) {
	q.mu.Lock()
	q.items = append(q.items, e)
	q.mu.Unlock()
}

func (q *queue) drain() []Event {
	q.mu.Lock()
	out := q.items
	q.items = nil
	q.mu.Unlock()
	return out
}
