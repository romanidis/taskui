package run

import (
	"sort"
	"time"
)

// Cost is one task's share of a run.
type Cost struct {
	Name     string
	Duration time.Duration
	Lines    int
	Status   Status
	// Self is Duration minus whatever its children accounted for. An aggregate that only
	// invokes other tasks has a large Duration and almost no Self, and reporting the first
	// as though it were the second would put `all` at the top of every profile saying
	// nothing.
	Self time.Duration
	// Children is how many tasks ran underneath it, for telling an aggregate from a leaf.
	Children int
}

// Profile is where a run's time went, slowest first.
//
// Every task already carries its own clock and the run view already shows it — beside the
// task, in tree order, which is the right place to answer "is this step slow" and the wrong
// one to answer "what makes this take four minutes". That second question is about the
// whole run at once, and it is a sort, not a walk.
//
// Self time is what makes it useful. A `task all` that invokes six things has a duration
// equal to the sum of theirs, so ranking by duration puts every aggregate above every task
// that did any work. Subtracting the children leaves the time a task spent on its own
// commands, which is the time that would actually go away if you made it faster.
func (r *Run) Profile() []Cost {
	out := make([]Cost, 0, len(r.Tasks))
	for name, t := range r.Tasks {
		d, ok := t.Elapsed()
		if !ok || t.Status == Skipped || t.Status == Pending {
			continue
		}
		children := r.Graph.Children(name)
		self := d
		for _, child := range children {
			if c, ok := r.Tasks[child]; ok {
				if cd, ok := c.Elapsed(); ok {
					self -= cd
				}
			}
		}
		// A parent that overlapped its children, or a clock that rounded the wrong way, can
		// take this below zero. Zero is the honest floor: it did not spend negative time.
		self = max(0, self)

		out = append(out, Cost{
			Name: name, Duration: d, Lines: len(t.Lines),
			Status: t.Status, Self: self, Children: len(children),
		})
	}

	sort.SliceStable(out, func(i, j int) bool {
		if out[i].Self != out[j].Self {
			return out[i].Self > out[j].Self
		}
		return out[i].Name < out[j].Name
	})
	return out
}
