package app

import (
	"strings"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/romanidis/taskui/internal/run"
	"github.com/romanidis/taskui/internal/theme"
)

// minRunColumn is the same for output, which needs more width than a task name does, so it
// splits later than the picker.
const minRunColumn = 60

// View is what Bubble Tea asks for: the frame, plus the terminal state the frame wants.
//
// The alternate screen is declared here rather than passed to NewProgram because that is
// where v2 moved it — a frame now carries the modes it needs, so there is one answer to
// "are we on the alternate screen" instead of a startup flag and a pair of commands.
func (a *App) View() tea.View {
	v := tea.NewView(a.frame())
	v.AltScreen = true
	// Asking for mouse events is what makes the wheel reach this program at all. Without it
	// the terminal keeps the wheel and scrolls its own scrollback — which in a Neovim
	// terminal buffer means the wheel scrolls the buffer taskui is drawing into, past
	// frames it has already thrown away.
	//
	// Cell motion rather than all motion: it is the least that carries the wheel, and the
	// difference is a report on every pixel of movement that nothing here would read.
	if a.Mouse {
		v.MouseMode = tea.MouseModeCellMotion
	}
	return v
}

// frame renders one frame. Rendering to a string rather than into a cell buffer is what
// makes `--screenshot` and the render tests the same code path as the live UI.
func (a *App) frame() string {
	width, height := a.Width, a.Height
	if width <= 0 || height <= 0 {
		return ""
	}

	// The slot bar costs a line, so it only appears once there is something to switch
	// between. One run open is the common case and should look exactly as it always did.
	slotBar := a.Screen == ScreenRun && len(a.Slots()) > 1

	headerH, footerH, slotsH := 0, 0, 0
	left := height
	if left > 0 {
		headerH = 1
		left--
	}
	if left > 0 {
		footerH = 1
		left--
	}
	if slotBar && left > 0 {
		slotsH = 1
		left--
	}
	// The hairlines are what separate the three bands, but they are the first thing to go
	// when there is no room: a rule costs a row of content, and on an eight-row terminal
	// content is the only thing worth spending rows on.
	rules := 0
	if left >= minRuledBody+2 {
		rules = 2
		left -= 2
	}
	bodyH := left
	a.Viewport = bodyH

	out := make([]string, 0, height)
	blank := strings.Repeat(" ", width)

	var header, footer line
	var body []string

	switch a.Screen {
	case ScreenPicker:
		header = a.pickerHeader()
		body = a.drawPicker(width, bodyH)
		footer = a.pickerFooter()
	case ScreenRun:
		header = a.runHeader()
		body = a.drawRun(width, bodyH)
		footer = a.runFooter()
	case ScreenHistory:
		header = a.historyHeader()
		body = a.drawHistory(width, bodyH)
		footer = a.historyFooter()
	case ScreenHelp:
		header = a.helpHeader()
		body = a.drawHelp(width, bodyH)
		footer = a.helpFooter()
	case ScreenDetail:
		header = a.detailHeader()
		body = a.drawDetail(width, bodyH)
		footer = a.detailFooter()
	case ScreenTimeline:
		header = a.timelineHeader()
		body = a.drawTimeline(width, bodyH)
		footer = a.timelineFooter()
	case ScreenDiff:
		header = a.diffHeader()
		body = a.drawDiff(width, bodyH)
		footer = a.diffFooter()
	case ScreenProfile:
		header = a.profileHeader()
		body = a.drawProfile(width, bodyH)
		footer = a.profileFooter()
	}

	// The palette takes the body and the footer of whatever screen it was opened on, and
	// leaves the header saying which screen that is.
	if a.Palette {
		body = a.drawPalette(width, bodyH)
		footer = a.paletteFooter()
	}

	if headerH == 1 {
		out = append(out, header.render(width, false, a.Theme.Colors.Selection))
	}
	if slotsH == 1 {
		out = append(out, a.drawSlotBar().render(width, false, a.Theme.Colors.Selection))
	}
	// The rule closes the header band, so the slot bar sits inside it rather than adrift
	// between the rule and the body.
	if rules > 0 {
		out = append(out, a.hairline(width))
	}
	for i := range bodyH {
		if i < len(body) {
			out = append(out, body[i])
		} else {
			out = append(out, blank)
		}
	}
	if rules > 0 {
		out = append(out, a.hairline(width))
	}
	if footerH == 1 {
		out = append(out, footer.render(width, false, a.Theme.Colors.Selection))
	}

	return strings.Join(out, "\n")
}

// minRuledBody is how much body has to be left over before the hairlines earn their rows.
const minRuledBody = 4

// hairline is the rule between the bands. It starts one column in, under the cursor rail,
// so the rail's column stays the only thing that ever appears there.
func (a *App) hairline(width int) string {
	return line{
		plain(" "),
		styled(strings.Repeat(a.Theme.Glyphs.Rule, max(0, width-1)), fg(a.Theme.Colors.Rule)),
	}.render(width, false, a.Theme.Colors.Selection)
}

// RenderFrame renders one frame at a given size, escape sequences and all.
//
// The colours are the point when you are writing a theme: the loop is edit the file, run
// this, look at it. Everything else — the tests, the diffs — wants the text underneath,
// which is what RenderHeadless returns.
func (a *App) RenderFrame(w, h int) string {
	a.Width, a.Height = w, h
	return a.frame()
}

// RenderHeadless renders one frame to plain text off-screen. It backs both the render
// tests and `--screenshot`, which is how you look at the real thing without a terminal.
func (a *App) RenderHeadless(w, h int) []string {
	frame := a.RenderFrame(w, h)
	rows := strings.Split(frame, "\n")
	out := make([]string, 0, h)
	for i := range h {
		text := ""
		if i < len(rows) {
			text = strings.TrimRight(ansi.Strip(rows[i]), " ")
		}
		out = append(out, text)
	}
	return out
}

// header lays a header out: the wordmark and a subject on the left, state right-anchored
// against the last column.
//
// Right-anchoring is what makes the state block a column rather than a tail. It always ends
// in the same place, so the eye knows where to look for "how many tasks" or "did it fail"
// without reading the left-hand side first.
func (a *App) header(subject string, state []span) line {
	mark := a.wordmark()
	l := line{plain(" "), styled(mark, fgBold(a.Theme.Colors.Accent))}
	stateWidth := 0
	for _, s := range state {
		stateWidth += cells(s.text)
	}
	// `taskui · taskui` — the wordmark, then a directory that happens to share its name —
	// reads as a bug, and spends the most valuable row on the screen saying it twice.
	//
	// Compared against the wordmark as drawn rather than as written, so a theme that put
	// `{project}` in it is caught by the same rule that catches one whose name happens to
	// match.
	if subject != "" && !strings.EqualFold(subject, plainWordmark(mark)) {
		sep := " " + a.Theme.Glyphs.Separator + " "
		// The subject gives way, not the state. A run with long arguments made the subject
		// wide enough to push the state off the right edge, and the state is where PASSED,
		// the elapsed time and the exit code are — the part of the header you look at.
		room := a.Width - 1 - cells(" "+mark+sep) - stateWidth - 1
		if room > 0 {
			l = append(l, styled(sep, fg(a.Theme.Colors.Faint)), styled(clip(subject, room), bold()))
		}
	}
	used := 0
	for _, s := range l {
		used += cells(s.text)
	}
	l = append(l, plain(strings.Repeat(" ", max(1, a.Width-1-used-stateWidth))))
	return append(l, state...)
}

func statusChip(status run.Status, t theme.Theme) span {
	switch status {
	case run.Ok:
		return styled(" PASSED ", onBg(t.Colors.WarningFg, t.Colors.StatusOk))
	case run.Failed:
		return styled(" FAILED ", onBg(t.Colors.WarningFg, t.Colors.StatusFailed))
	default:
		return styled(" RUNNING ", onBg(t.Colors.WarningFg, t.Colors.StatusRunning))
	}
}

// statusGlyph is the theme's mark for a status. `run.Status.Glyph()` stays as the default
// for the headless `--run` output, which is piped and diffed rather than looked at.
func statusGlyph(status run.Status, t theme.Theme) string {
	switch status {
	case run.Pending:
		return t.Glyphs.StatusPending
	case run.Running:
		return t.Glyphs.StatusRunning
	case run.Ok:
		return t.Glyphs.StatusOk
	case run.Failed:
		return t.Glyphs.StatusFailed
	default:
		return t.Glyphs.StatusSkipped
	}
}

// statusMark is a status as the rows draw it: the theme's glyph, bold in the status's colour,
// with the space that keeps it off whatever follows.
func statusMark(status run.Status, t theme.Theme) span {
	return styled(statusGlyph(status, t)+" ", fgBold(statusStyle(status, t)))
}

// outcome is a pass or a fail as the status it ended in, so a result read back from the
// archive is drawn with the same glyph and colour as a live one.
func outcome(ok bool) run.Status {
	if ok {
		return run.Ok
	}
	return run.Failed
}

// projectPlaceholder is what a wordmark writes to mean "whatever this project is called",
// and framePlaceholder is where the wordmark's animation goes.
const (
	projectPlaceholder = "{project}"
	framePlaceholder   = "{frame}"
)

// wordmark is the theme's wordmark with its placeholders filled in.
//
// `{project}` is what makes the header able to name the thing you are looking at rather
// than the tool you are looking at it with, without a theme having to give up its own
// decoration to do it: `"✧･ﾟ {project} ･ﾟ✧"` keeps the sparkles and changes the middle.
func (a *App) wordmark() string {
	mark := a.Theme.Glyphs.Wordmark
	if strings.Contains(mark, projectPlaceholder) {
		name := baseName(a.Root)
		if name == "" {
			name = a.Root
		}
		mark = strings.ReplaceAll(mark, projectPlaceholder, name)
	}
	return strings.ReplaceAll(mark, framePlaceholder, a.Theme.Animation.WordmarkFrame(a.Phase))
}

// plainWordmark is the wordmark with any decoration stripped, so a themed one still
// recognises the project it is named after.
//
// Trims from both ends anything that is not a letter or a digit, rather than a list of the
// decoration characters seen so far — that list rotted the moment two themes were added
// whose flourishes were not on it, and the header started printing the name twice. Modifier
// letters go too: `ﾟ` is one, and it is punctuation everywhere this is used.
//
// A cosmetic comparison, so the odd script that builds words out of modifier letters losing
// a character here costs nothing but a repeated word in a header.
func plainWordmark(text string) string {
	return strings.TrimFunc(text, func(r rune) bool {
		if unicode.IsDigit(r) {
			return false
		}
		return !unicode.IsLetter(r) || unicode.Is(unicode.Lm, r)
	})
}

func statusStyle(status run.Status, t theme.Theme) theme.Color {
	switch status {
	case run.Pending:
		return t.Colors.StatusPending
	case run.Running:
		return t.Colors.StatusRunning
	case run.Ok:
		return t.Colors.StatusOk
	case run.Failed:
		return t.Colors.StatusFailed
	default:
		return t.Colors.StatusSkipped
	}
}

// scrollPane clamps an offset and cuts a paragraph down to the visible rows.
func (a *App) scrollPane(lines []line, width, height int, offset *int) []string {
	overflow := max(0, len(lines)-height)
	*offset = min(*offset, overflow)
	out := make([]string, 0, height)
	for i := *offset; i < len(lines) && len(out) < height; i++ {
		// renderRow, not render: the blank rail column keeps these panes flush with the
		// rows on every other screen.
		out = append(out, lines[i].renderRow(width, false, a.Theme, a.Phase, 0, 1))
	}
	return out
}

// frameWidth is the two columns the cursor's frame occupies — the lit edge on the left and
// its shade on the right — which every row builder has to leave for it.
//
// Both are reserved whatever the theme does with them, so a row is the same width in every
// look. Geometry that changed with the colours would be a theme that could break a layout.
const frameWidth = 2

// bodyWidth is how much of a row is content: everything the cursor's frame does not take,
// less whatever room the theme's jiggle needs to lean into.
//
// One function rather than the subtraction spelled out at each site, because the number has
// to be the same on both sides of the render: this is what decides where text wraps, and
// renderRow does the same arithmetic to decide where to draw it. The two disagreeing by one
// column is a row that wraps a word it then has space for.
func (a *App) bodyWidth(width int) int {
	return width - frameWidth - a.Theme.Animation.MaxLean()
}

// composeColumns lays items into columns and zips them into terminal rows.
//
// The cursor lives in whichever column contains it; the rest are a look-ahead, which is
// why the selection is only drawn once. item renders one row, and only the rows that land
// in a column are asked for.
func (a *App) composeColumns(
	bounds [][2]int,
	widths []int,
	colWidth, height, cursor int,
	item func(i int) []line,
) []string {
	cols := make([][]string, len(widths))
	for c := range cols {
		cols[c] = make([]string, height)
		for i := range cols[c] {
			cols[c][i] = strings.Repeat(" ", widths[c])
		}
	}

	for c, b := range bounds {
		if c >= len(widths) || b[0] >= b[1] {
			break
		}
		at := 0
		for i := b[0]; i < b[1]; i++ {
			lines := item(i)
			selected := i == cursor
			for li, l := range lines {
				if at >= height {
					break
				}
				// len(lines), not 1: this is the one place a row can be more than a line
				// tall, and the rail has to know so it does not animate itself into pieces.
				text := l.renderRow(colWidth, selected, a.Theme, a.Phase, li, len(lines))
				// The gap to the next column is outside the row: its shade glyph is the row's
				// right edge. Painting the gap in the selection colour ran the bar past that
				// edge, and did it on every frame, so a blinking theme's bar went dark while
				// the gap beside it stayed lit.
				if pad := widths[c] - colWidth; pad > 0 {
					text += strings.Repeat(" ", pad)
				}
				cols[c][at] = text
				at++
			}
		}
	}

	out := make([]string, height)
	for i := range height {
		var b strings.Builder
		for c := range cols {
			b.WriteString(cols[c][i])
		}
		out[i] = b.String()
	}
	return out
}

// columnWidths splits width into n columns of as near equal size as whole cells allow.
//
// Each boundary is the *rounded* fraction of the width rather than the truncated one, so
// 200 across three columns comes out 67/66/67 and not 66/67/67. That is not cosmetic: the
// text width every item is built for comes from the first column, so which column carries
// the spare cell decides where every description wraps.
func columnWidths(width, columns int) []int {
	boundary := func(i int) int { return (width*i + columns/2) / columns }
	out := make([]int, columns)
	for i := range columns {
		out[i] = boundary(i+1) - boundary(i)
	}
	return out
}

// scrollTo keeps a cursor inside a simple, one-line-per-row list.
func scrollTo(offset, cursor, count, height int) int {
	if height <= 0 || count == 0 {
		return 0
	}
	if cursor < offset {
		offset = cursor
	}
	if cursor >= offset+height {
		offset = cursor - height + 1
	}
	return clamp(offset, 0, max(0, count-height))
}
