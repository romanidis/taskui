package app

import (
	"github.com/romanidis/taskui/internal/pivot"
)

// Mode is the active pivot.
func (a *App) Mode() pivot.Pivot {
	if len(a.Pivots) == 0 {
		return pivot.Builtins()[0]
	}
	return a.Pivots[clamp(a.Pivot, 0, len(a.Pivots)-1)]
}

// ModeLabel is its name, which is also the key its fold state is kept under.
func (a *App) ModeLabel() string { return a.Mode().Name }

// SetPivot switches to a named pivot. Reports false for a name nothing answers to.
func (a *App) SetPivot(name string) bool {
	for i, p := range a.Pivots {
		if p.Name == name {
			keep := a.SelectedTask()
			a.Pivot = i
			a.Rebuild(keep)
			return true
		}
	}
	return false
}

func (a *App) foldSet() map[string]bool {
	label := a.ModeLabel()
	set, ok := a.expanded[label]
	if !ok {
		set = map[string]bool{}
		a.expanded[label] = set
	}
	return set
}

// visible reports which tasks pass the current filter.
func (a *App) visible() []int {
	if a.Query == "" {
		out := make([]int, len(a.Tasks))
		for i := range a.Tasks {
			out[i] = i
		}
		return out
	}
	return a.matchingTasks(a.Query)
}

// Rebuild rebuilds the tree and the flattened rows.
//
// keep is a task index to stay parked on across the rebuild — the property that makes
// toggling a pivot feel like a pivot rather than a navigation reset. Its ancestors get
// opened so it is actually on screen afterwards. Pass -1 for none.
func (a *App) Rebuild(keep int) {
	visible := a.visible()
	a.Tree = pivot.Build(a.Mode(), a.Tasks, visible, a.Ordering())

	if keep >= 0 {
		if ancestors, ok := a.Tree.AncestorsOfTask(keep); ok {
			set := a.foldSet()
			for _, k := range ancestors {
				set[k] = true
			}
		}
	}

	filtering := a.Query != ""
	set := a.foldSet()
	// While filtering, every group is open: a hit hidden behind a fold is a hit you did
	// not find.
	a.Rows = a.Tree.Flatten(func(key string) bool { return filtering || set[key] })

	tree := -1
	if keep >= 0 {
		for i, r := range a.Rows {
			if a.Tree.Nodes[r.Node].Task == keep {
				tree = i
				break
			}
		}
	}
	a.RebuildPickerRows()
	if tree >= 0 {
		a.Cursor = a.pickerIndexOfTree(tree)
		return
	}
	a.Cursor = min(a.Cursor, max(0, len(a.PickerRows)-1))
}

// SelectedTask is the task under the cursor, or pivot.NoTask.
//
// From a row inside an inline run it is the task the block hangs under, so every key that
// acts on "the task under the cursor" keeps working while you are reading its output —
// which is the same rule the run view's `r` follows.
func (a *App) SelectedTask() int {
	node := a.SelectedNode()
	if node == nil {
		return pivot.NoTask
	}
	return node.Task
}

// SelectedNode is the tree node under the cursor.
func (a *App) SelectedNode() *pivot.Node {
	tree := a.cursorTreeRow()
	if tree < 0 || tree >= len(a.Rows) {
		return nil
	}
	return &a.Tree.Nodes[a.Rows[tree].Node]
}

// ToggleMode advances to the next pivot, wrapping.
//
// A cycle rather than a toggle, now that there can be more than two. The selection is kept
// across the change: bouncing between groupings to look at the same task from two angles is
// the entire reason to have more than one.
func (a *App) ToggleMode() {
	if len(a.Pivots) == 0 {
		return
	}
	keep := a.SelectedTask()
	a.Pivot = (a.Pivot + 1) % len(a.Pivots)
	a.Rebuild(keep)
	a.Status = "grouped by " + a.ModeLabel()
}

// CycleOrder advances to the next ordering, wrapping.
//
// The same shape as ToggleMode, and for the same reason: order and pivot are two
// independent questions about one list — what the tree is, and what sits above what inside
// it — so they cycle on two keys rather than one combined list of every pairing. The
// selection survives, because "sort this by what failed and tell me where my task went" is
// the question the key exists to answer.
//
// It moves `sort:` only. `groups:` and `pin:` stay where the config put them: interleaving
// and pinning are decisions about a project, not about the next ten seconds.
func (a *App) CycleOrder() {
	orders := pivot.Orders()
	at := 0
	for i, by := range orders {
		if by == a.Order.By {
			at = i
			break
		}
	}
	a.Order.By = orders[(at+1)%len(orders)]
	a.Rebuild(a.SelectedTask())
	if a.Order.By == pivot.ByNatural {
		a.Status = "back to each pivot's own order"
		return
	}
	a.Status = "sorted by " + a.OrderLabel()
}

// OrderLabel spells the active ordering for the header and the notice. `default` is what
// the config file calls it and says nothing on its own, so here it says what it defers to.
func (a *App) OrderLabel() string {
	if a.Order.By == pivot.ByNatural {
		return "each pivot's own order"
	}
	return a.Order.By.String()
}

func (a *App) ToggleFold() {
	node := a.SelectedNode()
	if node == nil || !node.IsGroup() {
		return
	}
	key := node.Key
	set := a.foldSet()
	if set[key] {
		delete(set, key)
	} else {
		set[key] = true
	}
	a.Rebuild(-1)
	// Stay parked on the group itself, so collapsing does not leave the cursor adrift
	// wherever the row that used to be at this index ended up.
	for i, r := range a.Rows {
		if a.Tree.Nodes[r.Node].Key == key {
			a.Cursor = a.pickerIndexOfTree(i)
			return
		}
	}
}

// ToggleFoldAll is `O` in the picker: close everything, or — if it is already all closed —
// open it.
//
// A toggle rather than a pair of keys because the two states are each other's only useful
// destination: you collapse to see the shape of the tree, then expand to get back to work.
func (a *App) ToggleFoldAll() {
	// Decided from the fold set rather than from what is on screen. Collapsing keeps the
	// path to the cursor open so the selection is not lost, which means a row is always
	// open afterwards — an "is anything open?" test therefore answers yes forever and the
	// toggle only ever works once.
	var groups []string
	for _, n := range a.Tree.Nodes {
		if n.IsGroup() {
			groups = append(groups, n.Key)
		}
	}
	open := a.expanded[a.ModeLabel()]
	allOpen := len(groups) > 0
	for _, g := range groups {
		if !open[g] {
			allOpen = false
			break
		}
	}
	a.SetFoldAll(!allOpen)
}

func (a *App) SetFoldAll(open bool) {
	keep := a.SelectedTask()
	var groups []string
	for _, n := range a.Tree.Nodes {
		if n.IsGroup() {
			groups = append(groups, n.Key)
		}
	}
	set := a.foldSet()
	for k := range set {
		delete(set, k)
	}
	if open {
		for _, k := range groups {
			set[k] = true
		}
	}
	a.Rebuild(keep)
}

func (a *App) MoveCursor(delta int) {
	if len(a.PickerRows) == 0 {
		return
	}
	a.Cursor = clamp(a.Cursor+delta, 0, len(a.PickerRows)-1)
}

// MoveGroup is `{` and `}` in the picker — the previous or next group header.
//
// Vim's paragraph keys, on a tree instead of on blank lines: every group counts whatever
// its depth, because `{` and `}` land on the next boundary rather than on one of a
// particular size. Rows inside an unfolded run are stepped over, which is most of the point
// — the block a run prints is the thing you want past. Running out of groups goes to the
// end of the list, as vim's do to the end of the file, so the key always moves.
func (a *App) MoveGroup(delta int) {
	if len(a.PickerRows) == 0 {
		return
	}
	for i := a.Cursor + delta; i >= 0 && i < len(a.PickerRows); i += delta {
		row := a.PickerRows[i]
		if row.IsRun() {
			continue
		}
		if a.Tree.Nodes[a.Rows[row.Tree].Node].IsGroup() {
			a.Cursor = i
			return
		}
	}
	if delta > 0 {
		a.Cursor = len(a.PickerRows) - 1
	} else {
		a.Cursor = 0
	}
}

func (a *App) PushQuery(c rune) {
	keep := a.SelectedTask()
	a.Query += string(c)
	a.Rebuild(keep)
}

func (a *App) PopQuery() {
	keep := a.SelectedTask()
	a.Query = withoutLastRune(a.Query)
	a.Rebuild(keep)
}

func (a *App) ClearQuery() {
	keep := a.SelectedTask()
	a.Query = ""
	a.Filtering = false
	a.Rebuild(keep)
}

// handleFilterKey returns true if the key was consumed by filter mode.
func (a *App) handleFilterKey(k Key) bool {
	switch {
	case k.kind == keyEsc:
		a.ClearQuery()
		return true
	case k.kind == keyEnter:
		// Keep the filter applied, leave the input — you filter to narrow the tree, then
		// navigate what is left.
		a.Filtering = false
		return true
	case k.kind == keyBackspace:
		a.PopQuery()
		return true
	case k.kind == keyDown, k.kind == keyUp:
		return false
	case k.typed():
		a.PushQuery(k.ch)
		return true
	default:
		return false
	}
}
