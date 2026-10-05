package app

import (
	"fmt"
	"strings"
	"time"

	"github.com/romanidis/taskui/internal/keys"
	"github.com/romanidis/taskui/internal/run"
	"github.com/romanidis/taskui/internal/theme"
)

// drawSlotBar names every open run on one line, so a background one is visible without
// going to look for it — a stack that died an hour ago should not need to be discovered.
//
// Numbered from one to match the digit keys that jump to them.
func (a *App) drawSlotBar() line {
	t := a.Theme
	l := line{plain("  ")}
	for i, slot := range a.Slots() {
		if i > 0 {
			l = append(l, styled("   ", fg(t.Colors.Dim)))
		}
		nameStyle := fg(t.Colors.Dim)
		if slot.Focused {
			nameStyle = bold()
		}
		l = append(l,
			styled(fmt.Sprintf("%d ", i+1), fg(t.Colors.Dim)),
			statusMark(slot.Status, t),
			styled(slot.Root, nameStyle),
			styled(" "+duration(slot.Elapsed), fg(t.Colors.Dim)),
		)
		// A slot quitting will not take down should say so where you choose between slots.
		if a.IsDetached(slot.Seq) {
			l = append(l, styled(" detached", fg(t.Colors.Stored)))
		}
	}
	return l
}

func (a *App) runHeader() line {
	t := a.Theme
	r := a.Run
	if r == nil {
		return line{}
	}

	status := r.Outcome()

	var l line
	if r.Interactive && !r.Finished() {
		l = append(l, styled("   interactive", fg(t.Colors.Interactive)))
	}
	if label := a.WatchLabel(); label != "" {
		l = append(l, styled("   watching "+label, fg(t.Colors.Interactive)))
	}
	switch {
	case r.Cancelled():
		// Cancelled and still going is not the same state as cancelled and gone, and it is
		// the one where you need to be told what is left to try: SIGTERM can be caught,
		// and something that catches it looks identical to something that is taking its
		// time. Naming the next press is the difference between waiting and being stuck.
		text := "   stopping — x again to kill it"
		switch {
		case r.Finished():
			text = "   cancelled"
		case r.Killed():
			text = "   killed — waiting on the OS"
		}
		l = append(l, styled(text, fg(t.Colors.StatusFailed)))
	case r.IsStored():
		l = append(l, styled("   from history", fg(t.Colors.Stored)))
	case !r.GraphResolved():
		l = append(l, styled("   resolving graph…", fg(t.Colors.Dim)))
	case a.Following && !r.Finished():
		l = append(l, styled("   following", fg(t.Colors.Notice)))
	}

	if a.Search != nil {
		position := "no matches"
		if len(a.SearchHits) > 0 {
			position = fmt.Sprintf("%d/%d", a.SearchIdx+1, len(a.SearchHits))
		}
		l = append(l,
			styled("   /", fg(t.Colors.Dim)),
			styled(a.Search.Pattern, fg(t.Colors.Search)),
			styled("  "+position, fg(t.Colors.Dim)),
		)
		if a.FilterMatches {
			l = append(l, styled(fmt.Sprintf("  filtered ±%d", a.FilterContext), fg(t.Colors.Search)))
		}
	}

	state := l
	if len(state) > 0 {
		state = append(state, plain("   "))
	}
	state = append(state, statusChip(status, t), styled("   "+duration(r.Elapsed()), fg(t.Colors.Dim)))
	if r.Outcome() == run.Failed {
		// Which task broke. go-task says so too, in a line at the foot of that task's
		// output that was the longest and loudest on the screen; it is drawn quietly now
		// that the header carries the part of it worth reading.
		if culprits := r.Culprits(); len(culprits) > 0 {
			where := "   in " + culprits[0]
			if more := len(culprits) - 1; more > 0 {
				where += fmt.Sprintf(" +%d", more)
			}
			state = append(state, styled(where, fg(t.Colors.StatusFailed)))
		}
		state = append(state, styled(fmt.Sprintf("   exit %d", r.Exit), fg(t.Colors.StatusFailed)))
	}
	return a.header(r.Command(), state)
}

// runRowLines builds the rendered lines for one run row. A task row and a line row are two
// shapes with one gutter between them, and splitting them would duplicate the indent,
// marker and highlight arithmetic.
//
//nolint:cyclop // two row shapes sharing one gutter; see above
func (a *App) runRowLines(r *run.Run, row RunRow, gutter string, width int) []line {
	t := a.Theme
	if r == nil {
		return nil
	}
	width = max(0, width-cells(gutter))

	if row.IsTask {
		tr, hasTask := r.Tasks[row.Name]
		status := run.Pending
		if hasTask {
			status = tr.Status
		}
		// Only offer a fold glyph where there is something to unfold. Filled means open,
		// hollow means ajar — the peek is a door left open a crack, and the glyph should
		// not claim you are looking at all of it.
		hasLines := hasTask && len(tr.Lines) > 0
		glyph := "  "
		if hasLines {
			switch row.Fold {
			case FoldHidden:
				glyph = t.Glyphs.FoldClosed + " "
			case FoldPeek:
				glyph = t.Glyphs.FoldPeek + " "
			case FoldFull:
				glyph = t.Glyphs.FoldOpen + " "
			}
		}

		nameStyle := fg(t.Colors.Text)
		switch status {
		case run.Failed:
			nameStyle = fgBold(t.Colors.StatusFailed)
		case run.Skipped:
			nameStyle = fg(t.Colors.Dim)
		default:
			// Pending, running and finished-well all read as ordinary text; the glyph
			// beside the name is what carries the state.
		}

		l := line{
			styled(gutter, fg(t.Colors.Faint)),
			styled(glyph, fg(t.Colors.Faint)),
			statusMark(status, t),
			styled(row.Name, nameStyle),
		}

		// The reason a task did not run belongs next to the task, not in the parent's output
		// where go-task printed it. `⇧R` is the answer to most of them, and the footer
		// already offers it.
		if hasTask && tr.Note != "" {
			l = append(l, styled("  "+tr.Note, fg(t.Colors.StatusSkipped)))
		}

		var tail strings.Builder
		if hasTask {
			// Closed, say how much there is; ajar, say how much is out of sight. "45
			// lines" next to five of them on screen reads as a contradiction.
			switch row.Fold {
			case FoldHidden:
				if len(tr.Lines) > 0 {
					fmt.Fprintf(&tail, "%d lines", len(tr.Lines))
				}
			case FoldPeek:
				if hidden := len(tr.Lines) - a.PeekLines; hidden > 0 {
					fmt.Fprintf(&tail, "%d more", hidden)
				}
			default:
				// Fully open: the lines are all on screen, so counting them would be
				// telling you what you can already see.
			}
			// Said out loud, open or closed: a buffer that has silently forgotten its
			// first hour is a buffer you would otherwise search and trust.
			if tr.Dropped > 0 {
				if tail.Len() > 0 {
					tail.WriteString("  ")
				}
				fmt.Fprintf(&tail, "%d earlier dropped", tr.Dropped)
			}
			// Ticking while the task runs, final once it stops. Sub-10ms figures are
			// dropped for the same reason `duration` refuses to print `0.00s`: a number
			// that is always zero is a column of noise, and here it is a column the eye
			// runs down looking for the slow step.
			if d, ok := tr.Elapsed(); ok && d >= tooFastToMatter {
				if tail.Len() > 0 {
					tail.WriteString("    ")
				}
				tail.WriteString(duration(d))
			}
		}
		// Right-anchored, so the whole column ends in the same place on every row.
		if tail.Len() > 0 {
			used := 0
			for _, sp := range l {
				used += cells(sp.text)
			}
			gap := max(2, width+cells(gutter)-used-cells(tail.String()))
			l = append(l, plain(strings.Repeat(" ", gap)), styled(tail.String(), fg(t.Colors.Dim)))
		}
		return []line{l}
	}

	text, isCommand := "", false
	if tr, ok := r.Tasks[row.Task]; ok && row.Index < len(tr.Lines) {
		// Through CommandText, which takes go-task's `[test] ` off the echo: the task's
		// header is a few rows above, and restating it on every command it runs spends
		// fifteen columns of a build log saying what the indentation already says.
		text = displayText(tr.Lines[row.Index])
		isCommand = tr.Lines[row.Index].IsCommand
	}

	// A command echo is a step, and what follows it is that step's output — so the two are
	// drawn as one thing. The echo carries its own verdict (▶ while it runs, ✓ once the
	// next one started, ✗ on the one that took the task down) and its output hangs off it
	// on a rail that closes at the last line. "Which step is this", "how did it go" and
	// "what did it print" are the three questions a build log gets read with, and the
	// first two used to be answerable only by finding where the output stopped.
	//
	// This reverses an earlier decision: output used to get no marker at all, on the
	// grounds that a `│` on every line is a column of chrome you stop seeing but keep
	// paying for. That was right while a command echo was only a label. It stopped being
	// right when the echo became a step with a status, because then which lines belong to
	// which step is a question worth being able to answer at a glance.
	rail, endsGroup := a.commandRail(r, row)
	marker := []span{styled(rail, fg(t.Colors.Faint)), plain("  ")}
	switch {
	case isCommand:
		st := run.Pending
		if tr, ok := r.Tasks[row.Task]; ok {
			st = tr.CommandStatus(row.Index)
		}
		marker = []span{
			statusMark(st, t),
			styled(t.Glyphs.Command+" ", fg(t.Colors.Faint)),
		}
	case isFailure(text):
		marker = []span{
			styled(rail, fg(t.Colors.Faint)),
			styled(t.Glyphs.Warning+" ", fg(t.Colors.Faint)),
		}
	}
	// The marker column is measured in cells, whatever it is made of. `❯` is three bytes
	// and one column, and measuring it in bytes — as the Rust original did — wrapped
	// output two columns early and, worse, disagreed with runRowHeight, which always
	// assumed two. A line measured shorter than it renders is a line that overflows its
	// column.
	pad := gutterFor(r)
	room := max(8, width-(pad+1+markerCells))

	// `text` and `command` are the theme's names for exactly these two: ordinary output and
	// go-task's echo of what it is running. Drawing the echo in the alias colour left a
	// theme's `command` colour set and never shown.
	base := fg(t.Colors.Text)
	switch {
	case isCommand:
		base = fg(t.Colors.Command)
	// Real output, so it stays, as it arrived — but it repeats what the ✗ beside the task
	// and the header already say, and at the length of a nested chain of task names it was
	// the loudest line on the screen.
	case isFailure(text):
		base = fg(t.Colors.Dim)
	}

	// One captured line can become several visual rows; the number and the marker belong
	// only to the first. In a peek it stays one row, cut at the edge — which is the trade
	// the window makes: five lines you can count beats one line you can read all of.
	var chunks []string
	if row.Peek {
		chunks = []string{clip(text, room)}
	} else {
		chunks = wrap(text, room)
	}

	out := make([]line, 0, len(chunks))
	for n, chunk := range chunks {
		l := line{styled(gutter, fg(t.Colors.Faint))}
		if n == 0 {
			l = append(l, styled(fmt.Sprintf("%*d ", pad, row.Index+1), fg(t.Colors.Faint)))
			l = append(l, marker...)
		} else {
			// Continuation: no line number, and the rail carries on down the wrapped rows
			// — except past the line that closed the group, where it has already ended.
			cont := "  "
			if rail != blankRail && !endsGroup {
				cont = t.Glyphs.GuideVertical + " "
			}
			l = append(l,
				plain(strings.Repeat(" ", pad+1)),
				styled(cont, fg(t.Colors.Faint)),
				plain("  "),
			)
		}

		// Highlight per chunk, so a match survives being wrapped. A search hit wins over a
		// file location: you went looking for the one and merely happened upon the other.
		if a.Search != nil {
			if start, end, ok := a.Search.FirstMatch(chunk); ok && end <= len(chunk) {
				l = append(l,
					styled(chunk[:start], base),
					styled(chunk[start:end], onBg(t.Colors.MatchFg, t.Colors.MatchBg)),
					styled(chunk[end:], base),
				)
				out = append(out, l)
				continue
			}
		}
		out = append(out, append(l, a.textWithLocations(chunk, base)...))
	}
	return out
}

// guides builds the tree guide for every row of a run: the column that says which task an
// output line came from, and which tasks are still to come below it.
//
// The geometry is what the plain two-spaces-per-depth indent already produced — the same
// columns, with a glyph in them — so nothing moves sideways by adopting it. The picker hands
// the result a prefix of its own, because there a run hangs under a row of the task tree and
// has to connect back to it.
func guides(rows []RunRow, g theme.Glyphs, prefix string) []string {
	// last[i] marks the task rows with no sibling below them: their branch closes, and
	// nothing under them carries a rail.
	last := make([]bool, len(rows))
	for i, row := range rows {
		if !row.IsTask {
			continue
		}
		last[i] = true
		for j := i + 1; j < len(rows); j++ {
			if rows[j].Depth < row.Depth {
				break
			}
			if rows[j].IsTask && rows[j].Depth == row.Depth {
				last[i] = false
				break
			}
		}
	}

	// carries[d] says whether the task drawn at depth d has something below it, and so
	// whether its rail runs on past the rows in between.
	carries := map[int]bool{}
	out := make([]string, len(rows))
	var b strings.Builder
	for i, row := range rows {
		if row.IsTask {
			carries[row.Depth] = !last[i]
		}
		b.Reset()
		b.WriteString(prefix)
		for d := 1; d < row.Depth; d++ {
			if carries[d] {
				b.WriteString(g.GuideVertical + " ")
			} else {
				b.WriteString("  ")
			}
		}
		switch {
		case row.Depth == 0:
			// The root, which has nothing above it to hang from.
		case row.IsTask && last[i]:
			b.WriteString(g.GuideLast + " ")
		case row.IsTask:
			b.WriteString(g.GuideBranch + " ")
		default:
			// An output line: its own depth column is blank, and the rail it hangs from is
			// already in the columns above.
			b.WriteString("  ")
		}
		out[i] = b.String()
	}
	return out
}

// markerCells is the width of a row's marker column: a status glyph and a `❯` on a command
// echo, the rail and a failure mark on everything else. One number rather than one per
// caller — the text width, the continuation indent and runRowHeight disagreeing is exactly
// how a line comes to be measured shorter than it renders.
const markerCells = 4

// blankRail is the rail column of a line no command claimed.
const blankRail = "  "

// commandRail is the guide tying an output line to the command above it: `│` while more of
// that command's output follows, `╰` on its last line, and nothing at all in a task whose
// output no command claimed — go-task's own messages, or a run restored from an archive
// written before the echo was kept.
//
// Read from the whole buffer rather than from what is on screen, so a peek window on the end
// of a command's output still says the lines belong to something above it.
func (a *App) commandRail(r *run.Run, row RunRow) (string, bool) {
	tr, ok := r.Tasks[row.Task]
	if !ok {
		return blankRail, false
	}
	switch under, last := tr.UnderCommand(row.Index); {
	case !under:
		return blankRail, false
	case last:
		return a.Theme.Glyphs.GuideLast + " ", true
	default:
		return a.Theme.Glyphs.GuideVertical + " ", false
	}
}

// tooFastToMatter is the point below which a duration is noise. `duration` already refuses
// to print `0.00s` for the same reason.
const tooFastToMatter = 10 * time.Millisecond

// gutterFor is how wide the line-number column has to be.
//
// Sized to the run rather than fixed at five: a run whose longest task printed nine lines
// numbers in one column, not five, and spends the other four on output. It is the run's
// widest task rather than each task's own width, because a gutter that changed between
// tasks would leave their output ragged against each other — and the column is there to be
// scanned down.
func gutterFor(r *run.Run) int {
	longest := 0
	if r != nil {
		for _, tr := range r.Tasks {
			longest = max(longest, len(tr.Lines))
		}
	}
	width := 1
	for longest >= 10 {
		longest /= 10
		width++
	}
	return width
}

// isFailure spots go-task's own report of what broke. The line is left exactly as it
// arrived — it is real output — but it does not have to look like ordinary output.
func isFailure(text string) bool {
	return strings.HasPrefix(text, "task: ") && strings.Contains(text, "Failed to run task")
}

// runRowHeight is how many terminal rows one run row occupies once wrapped.
func runRowHeight(r *run.Run, row RunRow, gutter string, width int) int {
	// A peeking line is always exactly one row. That is the whole contract of the window:
	// its height is known before its content is, so it cannot grow under you as output
	// arrives.
	if row.IsTask || row.Peek {
		return 1
	}
	text := ""
	if r != nil {
		if t, ok := r.Tasks[row.Task]; ok && row.Index < len(t.Lines) {
			text = displayText(t.Lines[row.Index])
		}
	}
	prefix := cells(gutter) + gutterFor(r) + 1 + markerCells
	return len(wrap(text, max(8, width-prefix)))
}

// displayText is a line of output as the run view draws it: a command echo without
// go-task's `task: [name] ` prefix, and tabs expanded.
//
// One function for the drawing and the measuring both. They used to disagree — the height
// was measured on the whole echo and the row drawn without its prefix — so a 50-character
// command at width 60 was measured as two rows and drawn as one, which left blank rows at
// the bottom of the body and could tip a run that fitted into columns.
func displayText(l run.Line) string { return expandTabs(run.CommandText(l)) }

func (a *App) drawRun(width, height int) []string {
	if a.Run == nil {
		return nil
	}
	height = max(1, height)

	// One column at full width unless the output genuinely overflows, in which case use
	// whatever the width can carry. Heights depend on the column width, so they are
	// measured once to decide and again to lay out.
	gutters := guides(a.RunRows, a.Theme.Glyphs, "")
	single := 0
	for i, r := range a.RunRows {
		single += runRowHeight(a.Run, r, gutters[i], a.bodyWidth(width))
	}
	columns := 1
	if single > height {
		columns = clamp(width/minRunColumn, 1, 3)
	}
	widths := columnWidths(width, columns)
	colWidth := widths[0]
	if columns > 1 {
		colWidth = max(0, colWidth-2)
	}

	heights := make([]int, len(a.RunRows))
	for i, r := range a.RunRows {
		heights[i] = runRowHeight(a.Run, r, gutters[i], a.bodyWidth(colWidth))
	}

	a.RunCursor = min(a.RunCursor, max(0, len(a.RunRows)-1))
	a.RunOffset = offsetForCursor(heights, a.RunCursor, height, columns)
	bounds := columnBounds(heights, a.RunOffset, height, columns)

	item := func(i int) []line {
		return a.runRowLines(a.Run, a.RunRows[i], gutters[i], a.bodyWidth(colWidth))
	}
	return a.composeColumns(bounds, widths, colWidth, height, a.RunCursor, item)
}

func (a *App) runFooter() line {
	t := a.Theme

	if a.SendingInput {
		l := line{styled("  input  ", onBg(t.Colors.WarningFg, t.Colors.Interactive))}
		if prompt, ok := promptOf(a.Run); ok {
			l = append(l, styled(" "+prompt, fg(t.Colors.Interactive)))
		} else {
			l = append(l, styled(" keys go to the task", fg(t.Colors.Interactive)))
		}
		// The receipt: what has actually gone down the pipe. Without it, "I typed y and
		// nothing happened" cannot be told apart from "y never left the building".
		if a.Run != nil && a.Run.Sent != "" {
			l = append(l,
				styled("   sent: ", fg(t.Colors.Dim)),
				styled(a.Run.Sent, fgBold(t.Colors.Interactive)),
			)
		}
		// Typing works either way, but in a buffered run you are doing it blind.
		if a.Run != nil && !a.Run.Interactive {
			l = append(
				l,
				styled("   buffered: ⏎ sends a newline, output may lag   ⇧I re-runs visibly", fg(t.Colors.Notice)),
			)
		}
		return append(l, styled("   esc to stop typing", fg(t.Colors.Dim)))
	}

	// A question taskui is asking, and a prompt you opened, come before what the task is
	// doing. Behind the `?` bar, `r` on a task sitting at `[y/N]` asked whether to restart
	// it out of sight, and the `y` meant for the task answered taskui instead.
	if l, ok := a.confirmBar(); ok {
		return l
	}
	if l, ok := a.argsPrompt(); ok {
		return l
	}

	if a.Searching {
		// The prompt says which of the two jobs it is doing — jump to matches, or hide
		// everything that is not one — and how to switch.
		label, other := "find /", "filter"
		if a.FilterMatches {
			label, other = "filter /", "find"
		}
		l := line{
			plain(" "),
			styled(label, fg(t.Colors.Search)),
			plain(a.SearchInput),
			styled(t.Glyphs.Cursor, fg(t.Colors.Search)),
		}
		switch {
		case a.SearchError != "":
			l = append(l, styled("   "+a.SearchError, fg(t.Colors.StatusFailed)))
		case a.Search != nil:
			tasks := map[string]bool{}
			for _, h := range a.SearchHits {
				tasks[h.Task] = true
			}
			n := len(a.SearchHits)
			l = append(l, styled(fmt.Sprintf("   %d %s in %d %s",
				n, plural(n, "match", "matches"),
				len(tasks), plural(len(tasks), "task", "tasks")), fg(t.Colors.Dim)))
		}
		return append(l, styled("   ⇥ "+other+"   ⏎ keep   esc clear", fg(t.Colors.Dim)))
	}

	if a.Run != nil && a.Run.PossiblyStuck() {
		return line{
			styled("  …  ", onBg(t.Colors.WarningFg, t.Colors.WarningBg)),
			styled(" no output for a while", fg(t.Colors.Notice)),
			styled("   waiting for input?  i types at it   ⇧I re-runs so you can see it   x stops", fg(t.Colors.Dim)),
		}
	}

	// A task blocked on a question looks identical to a slow one; say which it is.
	if a.AwaitingInput() {
		prompt, _ := promptOf(a.Run)
		return line{
			styled("  ?  ", onBg(t.Colors.WarningFg, t.Colors.WarningBg)),
			styled(" "+prompt, fg(t.Colors.Notice)),
			styled("   i to answer   x to stop", fg(t.Colors.Dim)),
		}
	}

	return a.statusBar(&keys.Run)
}

func promptOf(r *run.Run) (string, bool) {
	if r == nil {
		return "", false
	}
	return r.PendingPrompt()
}
