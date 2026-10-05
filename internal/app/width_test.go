package app

import (
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/charmbracelet/lipgloss"
	"github.com/muesli/termenv"

	"github.com/romanidis/taskui/internal/run"
	"github.com/romanidis/taskui/internal/task"
	"github.com/romanidis/taskui/internal/theme"
)

// A row is laid out in terminal columns, not characters: Japanese output takes two columns
// a character, and a tab takes however many reach the next stop.
func TestOutputWithWideCharactersAndTabsFitsItsRow(t *testing.T) {
	a := sample(t)
	r := run.Detached("test", run.GraphFrom(run.Edge{Parent: "test"}))
	r.Feed("test", "ok  \tgithub.com/x/y\t0.012s\tcoverage: 81.2% of statements in the package")
	r.Feed("test", "エラー: 設定ファイルが見つかりません。パスを確認してから、もう一度実行してください。")
	r.Finish(0)
	a.OpenRunForTest(r)
	a.Screen = ScreenRun
	a.RunSetFold("test", FoldFull)
	a.RebuildRunRows()

	for _, width := range []int{40, 60, 100} {
		for i, row := range strings.Split(a.RenderFrame(width, 20), "\n") {
			if n := lipgloss.Width(row); n != width {
				t.Errorf("width %d: row %d is %d columns: %q", width, i, n, row)
			}
		}
	}
}

func TestRenderFillsExactlyItsWidthInCells(t *testing.T) {
	for _, text := range []string{"plain ascii that runs long", "日本語のテキストがここにあります", "a😀b😀c😀d😀e"} {
		for _, width := range []int{1, 5, 9, 30} {
			got := line{plain(text)}.render(width, false, theme.Default)
			if n := cells(got); n != width {
				t.Errorf("render(%q, %d) is %d cells: %q", text, width, n, got)
			}
		}
	}
}

func TestWrapMeasuresInCellsAndNeverStartsARowWithASpace(t *testing.T) {
	if got := wrap("abcde fg", 5); !reflect.DeepEqual(got, []string{"abcde", "fg"}) {
		t.Errorf("wrap = %q", got)
	}
	for _, chunk := range wrap("設定ファイルが見つかりません 確認してください", 7) {
		if n := cells(chunk); n > 7 {
			t.Errorf("chunk %q is %d cells", chunk, n)
		}
		if strings.HasPrefix(chunk, " ") {
			t.Errorf("chunk %q starts with a space", chunk)
		}
	}
	// A character wider than the whole row still goes somewhere, once.
	if got := wrap("日本", 1); !reflect.DeepEqual(got, []string{"日", "本"}) {
		t.Errorf("wrap = %q", got)
	}
}

func TestTabsReachTheNextStop(t *testing.T) {
	if got := expandTabs("ok  \tgithub.com/x/y\t0.012s"); got != "ok      github.com/x/y  0.012s" {
		t.Errorf("expandTabs = %q", got)
	}
	// Measured in cells, so a wide character before a tab counts as the two it takes.
	if got := expandTabs("日本\tx"); got != "日本    x" {
		t.Errorf("expandTabs = %q", got)
	}
}

// The height is measured on the text that is drawn. A command echo is drawn without its
// `task: [name] ` prefix, and measuring it with the prefix called a one-row echo two.
func TestACommandEchoIsMeasuredAsItIsDrawn(t *testing.T) {
	a := sample(t)
	r := run.Detached("build", run.GraphFrom(run.Edge{Parent: "build"}))
	// As the capture delivers an echo: marked as one, which Feed does not do.
	r.Apply(run.LineEvent{
		Task:      "build",
		Raw:       "task: [build] go build -trimpath -ldflags=-s -o bin/taskui ./cmd/x",
		IsCommand: true,
	})
	a.OpenRunForTest(r)
	a.RunSetFold("build", FoldFull)
	a.RebuildRunRows()

	for i, row := range a.RunRows {
		if row.IsTask {
			continue
		}
		gutter := guides(a.RunRows, a.Theme.Glyphs, "")[i]
		for _, width := range []int{50, 60, 70, 90} {
			drawn := len(a.runRowLines(r, row, gutter, width))
			if measured := runRowHeight(r, row, gutter, width); measured != drawn {
				t.Errorf("width %d: measured %d rows, drew %d", width, measured, drawn)
			}
		}
	}
}

// A long subject gives way to the state, not the other way round: the state is where
// PASSED and the exit code are.
func TestTheHeaderCutsItsSubjectRatherThanItsState(t *testing.T) {
	a := sample(t)
	a.Width = 70
	state := []span{plain("PASSED  12.3s  exit 0")}
	got := a.header("task backend:lint VERSION=v1.2.3-rc.4 ENVIRONMENT=staging-eu", state)
	rendered := got.render(70, false, theme.Default)
	if !strings.Contains(rendered, "PASSED  12.3s  exit 0") {
		t.Errorf("the state fell off the edge: %q", rendered)
	}
	if !strings.Contains(rendered, "…") {
		t.Errorf("the subject should say it was cut: %q", rendered)
	}
}

// A frame lasts the theme's Interval, however often the poll loop happens to wake.
func TestTheAnimationKeepsItsOwnTime(t *testing.T) {
	a := sample(t)
	a.Theme.Animation = theme.Animation{Blink: []bool{true, false}, Interval: 2 * time.Second}
	start := time.Now()
	for _, c := range []struct {
		after time.Duration
		want  int
	}{
		{0, 0},
		{50 * time.Millisecond, 0},
		{1900 * time.Millisecond, 0},
		{2 * time.Second, 1},
		{7 * time.Second, 3},
	} {
		if got := a.animationPhase(start.Add(c.after)); got != c.want {
			t.Errorf("after %v: phase %d, want %d", c.after, got, c.want)
		}
	}
}

// A theme's `command` colour is what go-task's echo is drawn in, and its `text` colour is
// what ordinary output is drawn in. The echo used to take the alias colour and output the
// terminal's own, so both keys could be set and never seen.
func TestEchoesAndOutputTakeTheirOwnColours(t *testing.T) {
	restore := lipgloss.ColorProfile()
	lipgloss.SetColorProfile(termenv.TrueColor)
	t.Cleanup(func() { lipgloss.SetColorProfile(restore) })

	a := sample(t)
	must := func(text string) theme.Color {
		c, ok := theme.ParseColor(text)
		if !ok {
			t.Fatalf("not a colour: %s", text)
		}
		return c
	}
	a.Theme.Colors.Command = must("#ff0000")
	a.Theme.Colors.Alias = must("#00ff00")
	a.Theme.Colors.Text = must("#0000ff")

	r := run.Detached("build", run.GraphFrom(run.Edge{Parent: "build"}))
	r.Apply(run.LineEvent{Task: "build", Raw: "task: [build] go build ./...", IsCommand: true})
	r.Feed("build", "compiled fine")
	a.OpenRunForTest(r)
	a.Screen = ScreenRun
	a.RunSetFold("build", FoldFull)
	a.RebuildRunRows()

	echo, ok := find(strings.Split(a.RenderFrame(80, 12), "\n"), "go build")
	if !ok {
		t.Fatal("no echo row")
	}
	if !strings.Contains(echo, "38;2;255;0;0") || strings.Contains(echo, "38;2;0;255;0") {
		t.Errorf("the echo should be in the command colour, not the alias one: %q", echo)
	}
	output, ok := find(strings.Split(a.RenderFrame(80, 12), "\n"), "compiled fine")
	if !ok {
		t.Fatal("no output row")
	}
	if !strings.Contains(output, "38;2;0;0;255") {
		t.Errorf("output should be in the text colour: %q", output)
	}
}

// `b` is `a`'s last child, open, with children of its own. Its corner is a corner, and
// nothing hangs a rail down from `a` past it.
func TestAnOpenLastGroupClosesItsBranch(t *testing.T) {
	a := appWith(t, []string{"a:x", "a:b:c", "a:b:d"})
	a.SetFoldAll(true)
	g := a.Theme.Glyphs
	frame := a.RenderHeadless(60, 12)

	b, ok := find(frame, g.FoldOpen+"b")
	if !ok {
		t.Fatalf("no row for b in:\n%s", strings.Join(frame, "\n"))
	}
	if !strings.Contains(b, g.GuideLast+g.FoldOpen+"b") {
		t.Errorf("b is the last child and should take the corner: %q", b)
	}
	c, ok := find(frame, " c")
	if !ok {
		t.Fatal("no row for c")
	}
	if strings.Contains(c, g.GuideVertical) {
		t.Errorf("nothing follows b under a, so no rail runs past it: %q", c)
	}
}

// A top-level task draws no guide, so its wrapped description has none to continue.
func TestATopLevelDescriptionWrapsWithoutAGuide(t *testing.T) {
	// Not the last row: the last one never drew the guide, and would pass either way.
	tasks := []task.Task{
		{Name: "lint", Desc: strings.Repeat("checks every source file for style ", 4)},
		{Name: "test", Desc: "runs the suite"},
	}
	a := New(tasks, "/tmp/repo")
	a.SetStateDir(t.TempDir())
	g := a.Theme.Glyphs
	for _, row := range a.RenderHeadless(70, 12) {
		if strings.Contains(row, "style") && strings.Contains(row, g.GuideVertical) {
			t.Errorf("a stray guide on a top-level description: %q", row)
		}
	}
}

func TestARowNarrowerThanItsFrameStillFitsIt(t *testing.T) {
	for _, width := range []int{1, 2, 3} {
		got := line{plain("anything")}.renderRow(width, true, theme.DefaultTheme(), 0, 0, 1)
		if n := cells(got); n != width {
			t.Errorf("width %d: row is %d cells", width, n)
		}
	}
}

func TestBackspaceTakesOffOneCharacter(t *testing.T) {
	for in, want := range map[string]string{"lint": "lin", "адрес": "адре", "a😀": "a", "": ""} {
		if got := withoutLastRune(in); got != want {
			t.Errorf("withoutLastRune(%q) = %q, want %q", in, got, want)
		}
	}
}

func TestAPendingResultIsTakenOnceItLands(t *testing.T) {
	release := make(chan struct{})
	p := begin(func() (int, bool) { <-release; return 7, true })
	if _, ok := p.take(); ok || !p.running() {
		t.Fatal("taken before it landed")
	}
	// A grace that runs out leaves it pending, for the poll loop.
	if _, ok := p.await(10 * time.Millisecond); ok || !p.running() {
		t.Fatal("a timed-out wait dropped the job")
	}
	close(release)
	if v, ok := p.await(time.Second); !ok || v != 7 || p.running() {
		t.Fatalf("got %d, %v, running %v", v, ok, p.running())
	}
	if _, ok := p.take(); ok {
		t.Error("taken twice")
	}

	none := begin(func() (int, bool) { return 0, false })
	if _, ok := none.await(time.Second); ok || none.running() {
		t.Error("a job with no answer gave one, or stayed pending")
	}
}
