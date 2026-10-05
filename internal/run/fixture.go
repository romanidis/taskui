package run

import (
	"time"

	"github.com/romanidis/taskui/internal/graph"
)

// Detached builds a Run with no child process behind it, for tests that exercise state
// rather than capture.
func Detached(root string, g graph.Graph) *Run {
	tasks := map[string]*TaskRun{}
	for _, name := range g.Reachable(root) {
		tasks[name] = newTaskRun()
	}
	return &Run{
		Task:       root,
		Graph:      g,
		Tasks:      tasks,
		Started:    time.Now(),
		lastOutput: time.Now(),
	}
}

// Feed pushes one line at a task, as a capture would.
func (r *Run) Feed(task, text string) {
	r.apply(LineEvent{Task: task, Raw: text})
}

// ApplyFailed marks a task as the reported failure.
func (r *Run) ApplyFailed(task string) { r.apply(FailedEvent{Task: task}) }

// Finish ends the run with an exit code.
func (r *Run) Finish(exit int) { r.apply(Exited{Code: exit}) }

// Apply hands an event straight to the state machine, bypassing the channel.
func (r *Run) Apply(e Event) { r.apply(e) }

// Edge is one parent and the tasks it invokes.
type Edge struct {
	Parent   string
	Children []string
}

// GraphFrom builds a graph from parent/children pairs.
func GraphFrom(edges ...Edge) graph.Graph {
	g := graph.New()
	for _, e := range edges {
		g.Edges[e.Parent] = e.Children
	}
	return g
}

// SetDurationForTest pins a task's settled duration, so a test can describe a run's shape
// without depending on how long the test itself took.
func (t *TaskRun) SetDurationForTest(d time.Duration) {
	t.duration = d
	t.settled = true
}
