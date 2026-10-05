package app

import (
	"strings"

	"github.com/romanidis/taskui/internal/keys"
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
	t := a.Theme
	// What renderRow leaves a row once the frame and the jiggle's room are taken, less the
	// two-space indent. Measured from the frame alone, a theme that jiggles lost the last
	// character of every wrapped summary line off the end of its row.
	room := max(0, a.bodyWidth(width)-2)
	d := a.Detail
	var lines []line

	section := func(title string) {
		if len(lines) > 0 {
			lines = append(lines, line{})
		}
		lines = append(lines, line{styled(title, fgBold(t.Colors.Accent))})
	}

	for _, para := range d.Summary {
		for _, chunk := range wrap(para, room) {
			lines = append(lines, line{styled("  "+chunk, fg(t.Colors.Text))})
		}
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
		if a.UpToDate(a.DetailOf) {
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
			if name, ok := strings.CutPrefix(cmd, "Task: "); ok && !strings.Contains(name, "\n") {
				for _, chunk := range wrap("  → "+name, room) {
					lines = append(lines, line{styled(chunk, fg(t.Colors.Alias))})
				}
				continue
			}
			// A multi-line block is one command, so its continuation lines sit indented
			// under its first rather than level with it, where they would read as commands
			// of their own.
			for i, text := range strings.Split(cmd, "\n") {
				indent := "  "
				if i > 0 {
					indent = "    "
				}
				// Expanded before it is measured: a heredoc keeps its tabs, and a tab
				// counted as nothing and drawn as several pushed the row past its width.
				for _, chunk := range wrap(indent+expandTabs(text), room) {
					lines = append(lines, line{styled(chunk, fg(t.Colors.Text))})
				}
			}
		}
	}

	if len(lines) == 0 {
		lines = append(lines, line{styled("  go-task reports nothing about this task", fg(t.Colors.Dim))})
	}

	return a.scrollPane(lines, width, height, &a.DetailOffset)
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
