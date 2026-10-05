package app

import (
	"fmt"
	"strings"

	"github.com/romanidis/taskui/internal/keys"
	"github.com/romanidis/taskui/internal/pivot"
	"github.com/romanidis/taskui/internal/run"
)

func (a *App) pickerHeader() line {
	t := a.Theme
	dir := baseName(a.Root)
	if dir == "" {
		dir = a.Root
	}

	shown := 0
	for _, r := range a.Rows {
		if a.Tree.Nodes[r.Node].Task != pivot.NoTask {
			shown++
		}
	}

	state := a.pivotNames()
	// The order rides beside the grouping, and only when it is not the default: `⇧S` is a
	// key you press and forget, and a list sorted by what failed looks exactly like a list
	// that is broken unless something says why. The default says nothing, because every
	// pivot already names the order it is read in.
	if a.Order.By != pivot.ByNatural {
		state = append(state, styled("   by "+a.OrderLabel(), fg(t.Colors.Mode)))
	}

	// Leaving the run view does not stop anything; say so, or it is easy to forget — and
	// with several slots open the picker is the only screen that would not otherwise
	// mention the ones you are not looking at. A filter is a view of the list rather than a
	// change to what the program is doing, so it says so under one too — as the count
	// alone, because the header is right-anchored against the match count and a task name
	// is the part of this line with no bound on its length.
	switch n := a.InFlightCount(); {
	case n == 0:
	case n == 1 && a.Query == "":
		name := ""
		for _, s := range a.Slots() {
			if s.Status == run.Running {
				name = s.Root
				break
			}
		}
		state = append(state, styled("   ▶ "+name+" running", fg(t.Colors.Notice)))
	default:
		state = append(state, styled(fmt.Sprintf("   ▶ %d running", n), fg(t.Colors.Notice)))
	}

	if a.Query == "" {
		state = append(state, styled(fmt.Sprintf("   %d tasks", len(a.Tasks)), fg(t.Colors.Dim)))
	} else {
		state = append(state,
			styled("   /", fg(t.Colors.Dim)),
			styled(a.Query, fg(t.Colors.Search)),
			styled(fmt.Sprintf("   %d/%d tasks", shown, len(a.Tasks)), fg(t.Colors.Dim)),
		)
	}

	return a.header(dir, state)
}

// pivotNames lists the groupings with the active one accented.
//
// It replaces the header's `group: domain` and the footer's `p group by verb` at once: a
// control that shows its other positions is more use than either half on its own. With two
// pivots that was the whole list; with a config that can add more it stops fitting, so past
// a few it shows the one you are in and how many others there are — the header's right-hand
// block is right-anchored and a longer list would push the task count off the edge.
func (a *App) pivotNames() []span {
	t := a.Theme
	names := make([]string, 0, len(a.Pivots))
	for _, p := range a.Pivots {
		names = append(names, p.Name)
	}
	if len(names) == 0 {
		return nil
	}

	width := len(names) - 1
	for _, n := range names {
		width += cells(n)
	}
	if width > pivotNamesBudget {
		return []span{
			styled(a.ModeLabel(), fgBold(t.Colors.Mode)),
			styled(fmt.Sprintf(" +%d", len(names)-1), fg(t.Colors.Faint)),
		}
	}

	var out []span
	for i, n := range names {
		if i > 0 {
			out = append(out, styled(t.Glyphs.Dot, fg(t.Colors.Faint)))
		}
		style := fg(t.Colors.Faint)
		if i == a.Pivot {
			style = fgBold(t.Colors.Mode)
		}
		out = append(out, styled(n, style))
	}
	return out
}

// pivotNamesBudget is how much of the header the grouping list may take before it is
// summarised. The block it sits in is right-anchored against the task count.
const pivotNamesBudget = 28

// nameColumn is where a task's description starts, on every row, always.
//
// A fixed column is the whole point. It used to be computed from whatever the row had
// already spent — so a task carrying an outcome badge pushed its description right, and no
// two rows lined up. There is nothing to scan down when the column moves.
const nameColumn = 17

// countWidth is the column a group's task count occupies, and which every other row leaves
// empty so the signals to its left stay in line.
const countWidth = 4

// coversColumn lays the aggregates that run this namespace into the column descriptions
// start in, or says there is no room for them.
func (a *App) coversColumn(row pivot.Row, node pivot.Node, used, width int) (span, bool) {
	names, ok := a.coveredBy(row, node)
	room := width - nameColumn - countWidth - 2
	if !ok || room < 8 || used > nameColumn {
		return span{}, false
	}
	text := clip(a.Theme.Glyphs.Covers+" "+names, room)
	return styled(text, fg(a.Theme.Colors.Covers)), true
}

// coveredBy is the aggregates that run the namespace on this row, if it is one.
//
// Only a top-level group of the domain tree, because that is the only row whose Key *is* a
// namespace — `backend:migrate` is a group too and is nobody's namespace, and in the verb
// and file trees a group is not a namespace at all. Empty until the walk lands, which reads
// as "not known yet" rather than as "nothing runs this"; the two are different answers and
// only one of them is worth drawing.
func (a *App) coveredBy(row pivot.Row, node pivot.Node) (string, bool) {
	if len(a.Reaches) == 0 || row.Depth != 0 || !node.IsGroup() || a.ModeLabel() != pivot.DomainName {
		return "", false
	}
	names := a.Reaches[node.Key]
	if len(names) == 0 {
		return "", false
	}
	return strings.Join(names, " "), true
}

// lastOfParent reports whether the row at i is the final child of whatever contains it, so
// the guide can be a corner rather than a tee. Without it every branch looks like it has a
// sibling below, including the ones that do not.
//
// Found by looking past the row's own subtree rather than at the next row. Under an open
// group the next row is its own first child, which says nothing about whether the group has
// a sibling — so the last group of a namespace drew a tee, open, with nothing below it.
func (a *App) lastOfParent(i int) bool {
	for j := i + 1; j < len(a.Rows); j++ {
		switch {
		case a.Rows[j].Depth < a.Rows[i].Depth:
			return true
		case a.Rows[j].Depth == a.Rows[i].Depth:
			return false
		}
	}
	return true
}

// treeIndent is the guide columns in front of row i's own: one per level between the top
// and its parent, a vertical where the ancestor at that level still has a sibling to come
// and blank where it was the last. A vertical in every column ran each rail on past the end
// of its group, promising rows that were not there.
func (a *App) treeIndent(i int) string {
	g := a.Theme.Glyphs
	depth := a.Rows[i].Depth
	cols := make([]string, max(0, depth-1))
	for c := range cols {
		cols[c] = "  "
	}
	// Walking up, the first row met at each shallower depth is the ancestor at that depth:
	// the list is the tree flattened in order, so nothing shallower can come between.
	want := depth - 1
	for j := i - 1; j >= 0 && want >= 1; j-- {
		if a.Rows[j].Depth != want {
			continue
		}
		if !a.lastOfParent(j) {
			cols[want-1] = g.GuideVertical + " "
		}
		want--
	}
	return strings.Join(cols, "")
}

// treeItem builds one row of the task tree: guide, label, description, signals.
// slotBadge is how a row reports the run in its slot: `▶` and a ticking clock while it is
// going, then the verdict and what it took.
//
// It outranks the archive's "how it went last time", which is what the column says when no
// slot holds this task. The run you started a minute ago is the more current answer to the
// same question — and until it is saved, the archive has never heard of it.
func (a *App) slotBadge(name string) ([]span, bool) {
	r := a.slotRun(name)
	if r == nil {
		return nil, false
	}
	t := a.Theme
	if !r.Finished() {
		return []span{
			styled(t.Glyphs.StatusRunning+" ", fgBold(t.Colors.StatusRunning)),
			styled(duration(r.Elapsed()), fg(t.Colors.Dim)),
		}, true
	}
	return []span{
		statusMark(r.Outcome(), t),
		styled(duration(r.Elapsed()), fg(t.Colors.Dim)),
	}, true
}

// inlineFoldBadge is the fold glyph for a run unfolded under a task's row: how you know
// there is something under it at all, and which of the three states it is in. The
// vocabulary is the run view's own, so `▿` means a peek in both places.
func (a *App) inlineFoldBadge(name string) (span, bool) {
	fold, ok := a.InlineFold(name)
	if !ok {
		return span{}, false
	}
	g := a.Theme.Glyphs
	mark := g.FoldClosed
	switch fold {
	case FoldHidden:
		// Folded away; the closed glyph is what says there is something to open.
	case FoldPeek:
		mark = g.FoldPeek
	case FoldFull:
		mark = g.FoldOpen
	}
	return styled(mark+" ", fg(a.Theme.Colors.Faint)), true
}

func (a *App) treeItem(i, width int) []line {
	t := a.Theme
	row, last := a.Rows[i], a.lastOfParent(i)
	node := a.Tree.Nodes[row.Node]

	// Tree guides, so depth is something you see rather than something you count.
	//
	// Two columns, always, whatever is in them — that is what keeps every label in the same
	// place. A group used to spend both on its fold marker and a space, at every depth, which
	// meant a nested group and a top-level one rendered identically: in a Taskfile with
	// `backend:migrate:*`, the row for `migrate` was indistinguishable from the row for
	// `deploy` beside it, and the tree stopped being a tree exactly where it got deep enough
	// to need to be one. A group below the top now spends the first column on its branch and
	// the second on its fold marker, so it sits with its siblings and still says it opens.
	g := t.Glyphs
	indent := a.treeIndent(i)
	branch := g.GuideBranch
	if last {
		branch = g.GuideLast
	}
	fold := g.FoldClosed
	if row.Open {
		fold = g.FoldOpen
	}

	glyph := "  "
	switch {
	case node.IsGroup() && row.Depth > 0:
		glyph = branch + fold
	case node.IsGroup():
		glyph = fold + " "
	case row.Depth > 0:
		glyph = branch + " "
	}

	labelStyle := fg(a.Theme.Colors.Text)
	if node.IsGroup() {
		labelStyle = bold()
	}

	// A marked task takes its guide column for the mark. That column is one wide and always
	// there, so the mark costs no width and lands where the eye already runs down the list —
	// which is the whole reason to put it there rather than in the signal column, where it
	// would compete with how the task went last time.
	glyphStyle := fg(t.Colors.Faint)
	if node.Task != pivot.NoTask && a.IsMarked(a.Tasks[node.Task].Name) {
		glyph = g.Marked + " "
		glyphStyle = fgBold(t.Colors.Marked)
	}

	l := line{
		styled(indent, fg(t.Colors.Faint)),
		styled(glyph, glyphStyle),
		styled(node.Label, labelStyle),
	}
	used := max(1, row.Depth)*2 + cells(node.Label)

	// Everything that is not content — the count, an alias, how it went — right-anchors
	// into a signal column against the edge, so all of it ends where the eye expects it.
	var signals line
	if node.IsGroup() {
		signals = append(signals, styled(fmt.Sprintf("%*d", countWidth, node.Count), fg(t.Colors.Dim)))
	}

	// What runs this namespace, in the column descriptions start in — so the eye runs down
	// one column whether a row is a task explaining itself or a namespace naming what covers
	// it. On the header rather than as rows inside: a Taskfile with six aggregates would put
	// six near-identical rows above every namespace's real tasks, and the question this
	// answers ("is there something above that runs this?") is one you ask of the fold before
	// you open it.
	if covers, ok := a.coversColumn(row, node, used, width); ok {
		l = append(l, plain(strings.Repeat(" ", max(1, nameColumn-used))), covers)
		used = nameColumn + cells(covers.text)
	}

	var extra []line

	if node.Task != pivot.NoTask {
		task := a.Tasks[node.Task]
		var badges line
		if len(task.Aliases) > 0 {
			badges = append(badges, styled(strings.Join(task.Aliases, ", "), fg(t.Colors.Alias)), plain("  "))
		}
		if task.Dangerous {
			badges = append(badges, styled(g.Danger+" ", fgBold(t.Colors.Danger)))
		}
		// Running now takes the column, because it is the more urgent of the two questions
		// and the only one the list could not previously answer: with runs parked in slots
		// you are not looking at, the footer could say "3 running" while every row in
		// front of you showed nothing but history.
		//
		// Failing that, how it went last time. A blank column means never run, which is
		// information too — it is not the same as having passed.
		if badge, ok := a.inlineFoldBadge(task.Name); ok {
			badges = append(badges, badge)
		}
		if slot, ok := a.slotBadge(task.Name); ok {
			badges = append(badges, slot...)
		} else if o, ok := a.Outcomes[task.Name]; ok {
			badges = append(badges, statusMark(outcome(o.Ok), t), styled(Ago(o.WhenUnix), fg(t.Colors.Dim)))
		}
		signals = append(badges, signals...)
		// Reserve the count's columns on a row that has no count, so that the ✓/✗ ends in the
		// same place whether or not this row is also a group. Without it the outcome sits four
		// columns further right on every leaf than on every namespace, and the column the eye
		// runs down looking for what is broken zigzags — which is most of what that column was
		// for. Only where there is something to align: a row showing no outcome has nothing to
		// put in the space and would rather spend it on its description.
		if len(badges) > 0 && !node.IsGroup() {
			signals = append(signals, plain(strings.Repeat(" ", countWidth)))
		}

		// Descriptions wrap into their own column rather than being cut off mid-word — a
		// truncated description is the half that does not tell you anything. Continuation
		// rows hang under the first, and carry the guide down with them.
		signalWidth := 0
		for _, sp := range signals {
			signalWidth += cells(sp.text)
		}
		room := width - nameColumn - signalWidth - 2
		if task.Desc != "" && room >= 12 {
			chunks := wrap(task.Desc, room)
			// A label wider than the column it was given pushes its description sideways and
			// squeezes the signals off the end — which is how `✓ 9h ago` came out as `✓ 9h`.
			// The domain pivot never hits this, because its labels are single segments; the
			// verb and custom pivots show whole colon paths and hit it constantly. Where the
			// name does not fit, it keeps the row to itself and the description starts on the
			// next one, in the column it belongs to.
			first := 0
			if used <= nameColumn {
				l = append(l,
					plain(strings.Repeat(" ", max(1, nameColumn-used))),
					styled(chunks[0], fg(t.Colors.Dim)),
				)
				used = nameColumn + cells(chunks[0])
				first = 1
			}
			// A wrapped description used to leave the guide column blank, which broke the
			// run of branches in half: the eye follows the vertical down the list, and a
			// task with a two-line description put a gap in it that read as the end of the
			// group. The guide continues instead — except on the last child, where there
			// is nothing below to connect to and a vertical would promise a sibling that
			// does not exist, and at the top level, where the row itself draws no guide
			// for the vertical to continue.
			cont := g.GuideVertical
			if last || row.Depth == 0 {
				cont = " "
			}
			prefix := indent + cont + " "
			for _, chunk := range chunks[first:] {
				extra = append(extra, line{
					styled(prefix, fg(t.Colors.Faint)),
					plain(strings.Repeat(" ", max(0, nameColumn-cells(prefix)))),
					styled(chunk, fg(t.Colors.Dim)),
				})
			}
		}
	}

	if len(signals) > 0 {
		signalWidth := 0
		for _, sp := range signals {
			signalWidth += cells(sp.text)
		}
		l = append(l, plain(strings.Repeat(" ", max(2, width-used-signalWidth))))
		l = append(l, signals...)
	}

	return append([]line{l}, extra...)
}

// drawTree lays the picker out in one column, whatever the width.
//
// A list can be columnised. A tree cannot: the columns fill sequentially, so a group header
// ends up in one column while its own children continue in the next, and the indentation —
// the only thing saying which task belongs to what — stops meaning anything the moment it
// wraps. The width goes to the description instead, which is where it does work.
func (a *App) drawTree(width, height int) []string {
	height = max(1, height)

	const columns = 1
	widths := columnWidths(width, columns)
	colWidth := widths[0]

	item := func(i int) []line {
		row := a.PickerRows[i]
		if !row.IsRun() {
			return a.treeItem(row.Tree, a.bodyWidth(colWidth))
		}
		r := a.slotRun(row.Root)
		if r == nil {
			return []line{{plain("")}}
		}
		return a.runRowLines(r, row.Run, row.Rail, a.bodyWidth(colWidth))
	}

	heights := make([]int, len(a.PickerRows))
	for i, row := range a.PickerRows {
		if row.IsRun() {
			// Measured rather than rendered: a picker with a run unfolded under it is
			// mostly output rows, and every one of them is measured twice per frame.
			heights[i] = a.pickerRunHeight(row, a.bodyWidth(colWidth))
			continue
		}
		heights[i] = len(item(i))
	}

	a.Cursor = min(a.Cursor, max(0, len(a.PickerRows)-1))
	a.Offset = offsetForCursor(heights, a.Cursor, height, columns)
	bounds := columnBounds(heights, a.Offset, height, columns)

	return a.composeColumns(bounds, widths, colWidth, height, a.Cursor, item)
}

// pickerRunHeight is how many terminal rows one inline run row occupies, indentation
// included.
func (a *App) pickerRunHeight(row PickerRow, width int) int {
	r := a.slotRun(row.Root)
	if r == nil {
		return 1
	}
	return runRowHeight(r, row.Run, row.Rail, width)
}

// markBar replaces the hints while a set is waiting: `⏎` means something different with
// marks set, and a footer that went on saying "run" would be describing the other keymap.
func (a *App) markBar() (line, bool) {
	n := len(a.marked)
	if n == 0 {
		return nil, false
	}
	t := a.Theme
	l := line{
		plain(" "),
		styled(t.Glyphs.Marked+" ", fgBold(t.Colors.Marked)),
		styled(fmt.Sprintf("%d marked", n), fgBold(t.Colors.Marked)),
	}
	// What the last key did, in place of the hints: ⏎ on a set with no free slot refuses,
	// and a refusal the bar covered up made the key look as if it did nothing.
	if a.Status != "" {
		return append(l, styled("   "+a.Status, fg(t.Colors.Dim))), true
	}
	hints := "   ⏎ run them"
	// Spelled from the keymap, as every other hint is: a rebound mark key left this
	// advertising `m`.
	if c, ok := a.Keymap.KeyOf(keys.Mark); ok {
		hints += "   " + c.Display() + " unmark"
	}
	if c, ok := a.Keymap.KeyOf(keys.ClearMarks); ok {
		hints += "   " + c.Display() + " clear"
	}
	return append(l, styled(hints, fg(t.Colors.Dim))), true
}

func (a *App) pickerFooter() line {
	t := a.Theme
	if l, ok := a.confirmBar(); ok {
		return l
	}
	if a.Jumping {
		l := line{
			plain(" "),
			styled("jump: ", fg(t.Colors.Accent)),
			plain(a.JumpQuery),
			styled(t.Glyphs.Cursor, fg(t.Colors.Accent)),
		}
		if a.JumpQuery != "" {
			text := "   no match"
			if len(a.JumpMatches) > 0 {
				text = fmt.Sprintf("   %d/%d", a.JumpIdx+1, len(a.JumpMatches))
			}
			l = append(l, styled(text, fg(t.Colors.Dim)))
		}
		return append(l, styled("   ⇥ next   ⏎ stay   esc go back", fg(t.Colors.Dim)))
	}
	if l, ok := a.argsPrompt(); ok {
		return l
	}
	if a.Filtering {
		return line{
			plain(" "),
			styled("/", fg(t.Colors.Search)),
			plain(a.Query),
			styled(t.Glyphs.Cursor, fg(t.Colors.Search)),
			styled("   ⏎ accept   esc clear", fg(t.Colors.Dim)),
		}
	}
	// After the prompts, before the status: a prompt owns the footer while it is open — the
	// filter included, or the bar went on advertising `⏎ run them` while ⏎ accepted the
	// filter — and a set of marks is a state you are holding.
	if l, ok := a.markBar(); ok {
		return l
	}
	// No pivot hint here any more: the header's `domain·verb` names both sides and which
	// one you are in, which is more than this line ever said.
	return a.statusBar(&keys.Picker)
}
