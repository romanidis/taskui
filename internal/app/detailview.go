package app

import (
	"fmt"
	"slices"
	"strings"

	"github.com/romanidis/taskui/internal/graph"
	"github.com/romanidis/taskui/internal/keys"
	"github.com/romanidis/taskui/internal/run"
	"github.com/romanidis/taskui/internal/task"
)

func (a *App) detailHeader() line {
	t := a.Theme
	var state []span
	if o, ok := a.Outcomes[a.DetailOf]; ok {
		state = []span{statusMark(outcome(o.Ok), t), styled(Ago(o.WhenUnix), fg(t.Colors.Dim))}
	}
	return a.header(a.DetailOf, state)
}

func (a *App) drawDetail(width, height int) []string {
	// What renderRow leaves a row once the frame and the jiggle's room are taken, less the
	// two-space indent. Measured from the frame alone, a theme that jiggles lost the last
	// character of every wrapped summary line off the end of its row.
	room := max(0, a.bodyWidth(width)-2)
	return a.scrollPane(a.describeTask(a.DetailOf, a.Detail, room), width, height, &a.DetailOffset)
}

// describeTask is what is known about a task, as rows: what it says it does, what the rest of
// taskui knows about it, and what go-task says it will run.
//
// d is what go-task's `--summary` said. The detail panel asks for it fresh when it opens, and
// the preview takes it from the coverage walk, which asked for every task's already. Both
// read this one function, so the two cannot disagree about a task.
//
//nolint:cyclop // one description, read top to bottom: each branch is a fact known or not
func (a *App) describeTask(name string, d graph.Detail, room int) []line {
	t := a.Theme
	var lines []line
	section := func(title string) {
		if len(lines) > 0 {
			lines = append(lines, line{})
		}
		lines = append(lines, line{styled(title, fgBold(t.Colors.Accent))})
	}

	var listed task.Task
	for _, candidate := range a.Tasks {
		if candidate.Name == name {
			listed = candidate
			break
		}
	}

	// The summary when go-task gave one, which is the long form; the list's one line when it
	// did not, or has not yet.
	summary := d.Summary
	if len(summary) == 0 && listed.Desc != "" {
		summary = []string{listed.Desc}
	}
	for _, para := range summary {
		for _, chunk := range wrap(para, room) {
			lines = append(lines, line{styled("  "+chunk, fg(t.Colors.Text))})
		}
	}

	// What taskui itself knows, a fact to a row. Each of these was somewhere else — an alias
	// at the right edge of the list, a ✓ in a column, where it is written only behind `e` —
	// and none of them was where you go to find out what a task is.
	if len(lines) > 0 {
		lines = append(lines, line{})
	}
	fact := func(label string, value ...span) {
		lines = append(lines, append(line{styled("  "+padRight(label, 13), fg(t.Colors.Dim))}, value...))
	}
	if len(listed.Aliases) > 0 {
		fact("also called", styled(strings.Join(listed.Aliases, ", "), fg(t.Colors.Alias)))
	}
	if where, ok := a.WhereIs(name); ok {
		fact("written in", plain(fmt.Sprintf("%s:%d", relativeTo(a.Project, where.File), where.Line)))
	}
	if listed.Dangerous {
		fact("production", styled(t.Glyphs.Danger+" on the danger list — ⏎ asks first", fg(t.Colors.Danger)))
	}
	live := a.slotRun(name)
	last, ran := a.Outcomes[name]
	switch {
	case live != nil && !live.Finished():
		fact("running", statusMark(run.Running, t), styled(duration(live.Elapsed()), fg(t.Colors.Dim)))
	case ran:
		fact("last run", statusMark(outcome(last.Ok), t), styled(Ago(last.WhenUnix), fg(t.Colors.Dim)))
	default:
		fact("last run", styled("not since taskui started keeping runs", fg(t.Colors.Dim)))
	}
	if a.UpToDate(name) {
		fact("up to date", styled("go-task would skip it — ^f in the args prompt forces it", fg(t.Colors.Dim)))
	}
	var callers []string
	for parent, children := range a.calls.Edges {
		if parent != name && slices.Contains(children, name) {
			callers = append(callers, parent)
		}
	}
	if len(callers) > 0 {
		slices.Sort(callers)
		fact("called by", styled(clip(strings.Join(callers, ", "), max(8, room-13)), fg(t.Colors.Alias)))
	}

	if len(d.Requires) > 0 {
		section("requires")
		for _, v := range d.Requires {
			lines = append(lines, line{
				plain("  "),
				styled(v+"=", fgBold(t.Colors.Mode)),
				styled("   must be supplied with `a`", fg(t.Colors.Dim)),
			})
		}
	}

	if len(d.Dependencies) > 0 {
		section("runs first")
		for _, dep := range d.Dependencies {
			lines = append(lines, line{styled("  "+dep, fg(t.Colors.Alias))})
		}
	}

	if len(d.Commands) > 0 {
		heading := "will run"
		if a.UpToDate(name) {
			// go-task's own answer to "would running this do anything". Saying it here is
			// the difference between predicting that `⏎` does nothing and discovering it.
			heading = "would run — but go-task says it is up to date"
		}
		section(heading)
		if len(d.Requires) > 0 {
			// `task --summary` expands the template before printing it, so a variable you
			// have not supplied is substituted with nothing: `case "" in` where the real run
			// would have `case "v0.3.0" in`. The panel is not hiding anything — `requires`
			// is right above — but a command preview that quietly differs from the command
			// is worse than one that says it does.
			lines = append(lines, line{styled(
				"  shown with "+strings.Join(d.Requires, ", ")+" unset — `a` supplies them",
				fg(t.Colors.Notice),
			)})
		}
		for _, cmd := range d.Commands {
			// Another task, or a shell line — worth telling apart at a glance.
			if called, ok := strings.CutPrefix(cmd, "Task: "); ok && !strings.Contains(called, "\n") {
				lines = append(lines, line{styled("  → "+clip(called, max(1, room-4)), fg(t.Colors.Alias))})
				continue
			}
			// A multi-line block is one command, so its continuation lines sit indented
			// under its first rather than level with it, where they would read as commands
			// of their own — and so does the rest of a line too long for the row, which
			// started at the left edge and read as the next command.
			for i, text := range strings.Split(cmd, "\n") {
				indent := "  "
				if i > 0 {
					indent = "    "
				}
				// Expanded before it is measured: a heredoc keeps its tabs, and a tab
				// counted as nothing and drawn as several pushed the row past its width.
				for j, chunk := range wrap(expandTabs(text), max(1, room-cells(indent)-2)) {
					prefix := indent
					if j > 0 {
						prefix = indent + "  "
					}
					lines = append(lines, line{styled(prefix+chunk, fg(t.Colors.Text))})
				}
			}
		}
	}

	return lines
}

func (a *App) detailFooter() line {
	if l, ok := a.confirmBar(); ok {
		return l
	}
	// Through the table like every other screen. This footer used to be a literal, which is
	// exactly the drift the table exists to prevent: `e` was added to the section and this
	// line went on listing four keys, and it could not report a status either.
	return a.statusBar(&keys.DetailSection)
}
