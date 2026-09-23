package app

import (
	"github.com/romanidis/taskui/internal/graph"
	"github.com/romanidis/taskui/internal/keys"
)

// OpenDetail shows what the task under the cursor actually is: its description, what it
// requires, and the commands it will run — before running it.
func (a *App) OpenDetail() {
	ti := a.SelectedTask()
	if ti < 0 {
		a.Status = "nothing here to describe — space folds it"
		return
	}
	name := a.Tasks[ti].Name
	// One `--summary`, the same call the graph and the args prompt already make.
	a.Detail = graph.DescribeTask(a.Root, name)
	a.DetailOf = name
	a.DetailOffset = 0
	a.Screen = ScreenDetail
	a.Status = ""
}

func (a *App) CloseDetail() {
	a.DetailOf = ""
	a.Screen = ScreenPicker
}

func (a *App) DetailScroll(delta int) {
	a.DetailOffset = max(0, a.DetailOffset+delta)
}

func (a *App) handleDetailKey(k Key) bool {
	act := func() keys.Action { return a.action(k, ScreenDetail) }

	switch {
	case k.kind == keyEsc, act() == keys.Detail:
		a.CloseDetail()
	// Running it is the point of having read this.
	case k.kind == keyEnter:
		if name := a.DetailOf; name != "" {
			a.CloseDetail()
			a.RequestRun(name, nil)
		}
	case act() == keys.Args:
		if name := a.DetailOf; name != "" {
			a.CloseDetail()
			a.BeginArgs(name)
		}
	// Reading what a task will run is the moment you most want to change it.
	case act() == keys.Edit:
		a.EditDefinition(a.DetailOf)
	}
	return false
}
