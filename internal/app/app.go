// Package app holds the application state: the task list, the active pivot, fold state,
// filter and cursor — plus the Bubble Tea model that drives them.
package app

import (
	"strings"
	"time"

	"github.com/romanidis/taskui/internal/diff"
	"github.com/romanidis/taskui/internal/events"
	"github.com/romanidis/taskui/internal/graph"
	"github.com/romanidis/taskui/internal/keys"
	"github.com/romanidis/taskui/internal/loc"
	"github.com/romanidis/taskui/internal/pivot"
	"github.com/romanidis/taskui/internal/run"
	"github.com/romanidis/taskui/internal/search"
	"github.com/romanidis/taskui/internal/store"
	"github.com/romanidis/taskui/internal/task"
	"github.com/romanidis/taskui/internal/theme"
	"github.com/romanidis/taskui/internal/watch"
)

type Screen int

const (
	// ScreenPicker is browsing the Taskfile.
	ScreenPicker Screen = iota
	// ScreenRun is watching a run.
	ScreenRun
	// ScreenHistory is browsing runs that already happened.
	ScreenHistory
	// ScreenHelp is the keymap.
	ScreenHelp
	// ScreenDetail is what a task is and what it will run.
	ScreenDetail
	// ScreenTimeline is how one task has gone, run after run.
	ScreenTimeline
	// ScreenDiff is what changed between two runs of one task.
	ScreenDiff
	// ScreenProfile is where a run's time went.
	ScreenProfile
)

type App struct {
	// The run on screen, and how you are looking at it. Embedded, so its fields read as the
	// app's own — a.Run, a.RunCursor, a.Following — which to everything drawing the run view
	// is what they are. Never nil: with nothing open it is an empty slot, in no list.
	*slot

	Tasks []task.Task
	// Pivots is every grouping available, in the order `p` cycles them. The two built-ins
	// plus `file`, plus whatever the config added.
	Pivots []pivot.Pivot
	// Mouse says whether the frame asks the terminal for mouse events, which is what makes
	// the wheel scroll this program rather than whatever is drawing it.
	Mouse bool
	// Order is what sits above what, from the config. The archive lookup it sorts `recent`
	// and `failed` on is attached at Rebuild time by Ordering, since that answer changes
	// every time a run finishes and this value does not.
	Order pivot.Order
	// Pivot indexes Pivots.
	Pivot int
	Tree  *pivot.Tree
	Rows  []pivot.Row
	// PickerRows is the tree and every open run laid out as one list: what the cursor
	// indexes and the viewport scrolls. Rows stays the tree on its own, because that is
	// what the pivot builds and what filtering and jumping work over.
	PickerRows []PickerRow
	Cursor     int
	// Offset is the viewport offset, kept here so scrolling survives rebuilds.
	Offset int

	// expanded is per-mode: bouncing between pivots must not collapse what you opened on
	// the other side.
	expanded map[string]map[string]bool

	Filtering bool
	Query     string

	// marked is the set `⏎` runs at once. Nil until something is marked, so the common case
	// of running one task costs nothing.
	marked map[string]bool

	// Root is the directory taskui was opened in. It is where go-task is run from, which is
	// what a task sees as USER_WORKING_DIR, and how the archive files a run.
	Root string
	// Project is the directory of the Taskfile that governs Root: Root itself, or the
	// nearest directory above it with one. It is where the tasks run, so it is what the paths
	// they print and the paths they are given are relative to. Opened from `web/src`, the two
	// differ, and a Taskfile watch, a path completion or an `e` that read Root looked in the
	// wrong place.
	Project string
	Status  string
	// statusShown is the notice the clock below is running for, and statusAt is when it
	// went up. Kept beside the text rather than folded into it because every one of the
	// hundred-odd places that writes a status writes the field directly.
	statusShown string
	statusAt    time.Time
	Theme       theme.Theme
	Keymap      *keys.Keymap

	Screen Screen
	// Parked holds the slots that are open but not on screen. Their capture goroutines keep
	// draining and their processes keep going: parking is a state of the UI, not of the
	// child.
	Parked []*slot
	// Bell says when a finished run should ring the terminal.
	Bell theme.BellMode
	// belled remembers which runs have already rung, so a run that stays finished does not
	// ring on every poll. Keyed by the run, not its slot: a restart reuses the slot, and a
	// slot that had rung once never rang again.
	belled map[*run.Run]bool
	// pendingBell is collected by Update, which is the only place that may write to the
	// terminal — the model itself must not, or the byte lands in the middle of a frame.
	pendingBell bool

	// detached marks the runs quitting will leave alone. Keyed by the run rather than its
	// slot: a restart reuses the slot, and the new run was silently left running on quit
	// because the old one had been let go of.
	detached map[*run.Run]bool
	// partial marks runs whose archive was written before they finished — by detaching —
	// so finishing writes it again rather than leaving the record cut off where it was.
	partial map[*run.Run]bool

	// nextSeq is the last slot number handed out.
	nextSeq uint64
	// PeekLines is how many lines a peeking task shows. Configurable.
	PeekLines int

	// Search is the output search. Distinct from Query, which filters task names in the
	// picker — a couple of hundred short strings versus potentially megabytes of output,
	// so they get separate affordances even though both are bound to `/`.
	Search      *search.Query
	Searching   bool
	SearchInput string
	SearchHits  []search.LiveHit
	SearchIdx   int
	// FilterMatches shows only matching lines, grouped under the task that produced them.
	FilterMatches bool
	SearchError   string

	// Outcomes says how each task went last time, so browsing answers "what is broken
	// right now" without opening anything.
	Outcomes map[string]store.Outcome

	History       []store.Manifest
	HistoryCursor int
	HistoryOffset int
	// HistoryScope is how wide the archive list is: this directory, this repository, or
	// everything. Narrow by default — runs from every project in one list stops being
	// useful the moment you use taskui in two repos — and widened a rung at a time, because
	// the rung in the middle is the one a worktree needs. The archive is keyed by directory,
	// and a worktree is a different directory holding the same project, so without it
	// `⇧H` on a task you have run for months is empty the day you branch.
	HistoryScope HistoryScope
	// repo is this project's checkout, shared by every worktree of it, and repoRead says the
	// question has been put to git. Asked once and lazily: it is a process spawn on a path
	// only the history list takes, and the several hundred tests that build an App from a
	// fixture are not in a checkout at all.
	repo     string
	repoRead bool

	// Jumping moves the cursor without narrowing the list — what you want when you are
	// looking at the tree rather than filtering it.
	Jumping     bool
	JumpQuery   string
	JumpMatches []int
	JumpIdx     int
	// jumpOrigin is where the cursor was before the jump, so `esc` really does cancel.
	// jumpNode is the tree node it was on, when it was on one: the jump opens folds on its
	// way to a match, which moves every row below them, so the index alone put the cursor
	// back on whatever had slid into that position.
	jumpOrigin int
	jumpNode   int

	// PendingG is half of a `gg`. Any other key clears it, so a stray `g` cannot lurk.
	PendingG bool
	// EscStreak counts consecutive `esc` presses with nothing left to dismiss.
	//
	// `esc` used to quit from the picker, which put "leave the tool" on the key everyone
	// presses to mean back out of whatever this is. It no longer does — but a key that
	// silently does nothing reads as broken, so the second press in a row says where the
	// exit actually is. Any other key resets it.
	EscStreak int
	// helpReturn is where `?` was pressed, so `esc` puts you back rather than somewhere
	// arbitrary.
	helpReturn Screen
	inHelp     bool
	HelpOffset int
	// HelpFinding is the find prompt on the `?` screen taking the keys; HelpQuery is what
	// it narrows the keymap to, and outlives the prompt so `⏎` can leave you reading the
	// handful of bindings you were looking for.
	HelpFinding  bool
	HelpQuery    string
	DetailOf     string
	Detail       graph.Detail
	DetailOffset int
	// The Taskfile watch: what it is pointed at, and the re-read it kicks off. See
	// reload.go — the list used to be read once and never again, while `e` opens the file
	// it was read from.
	taskfileWatch    *watch.Watch
	watchedTaskfiles []string
	watchingTaskfile bool
	reload           pending[reloaded]
	reloadPending    bool
	// enriching and covering say the two background lookups were asked for, so a reload
	// knows to start them again — and, just as importantly, knows not to start them on a
	// path that never opted in. Both shell out.
	enriching bool
	covering  bool

	// Watching is the tasks being re-run when the source changes.
	//
	// A set rather than one name: marks already say "these belong together", and `check`
	// split across three tasks is the loop watch mode exists for. One task is the set of
	// one, which is what it was before.
	Watching []string
	watcher  *watch.Watch

	// One task's history, and the diff between two of its runs.
	TimelineOf     string
	Timeline       []store.Point
	TimelineCursor int
	TimelineOffset int
	// TimelineFlakes are the commits at which this task went both ways.
	TimelineFlakes []store.Flake
	timelineReturn Screen

	DiffOf string
	// DiffAgainstWhat names the older side in words — "when it last passed", "the run
	// before". The header says which comparison you are looking at because there are two,
	// and a diff you have mistaken for the other one is worse than no diff.
	DiffAgainstWhat string
	DiffAgainst     store.Point
	DiffStat        diff.Stat
	DiffRows        []DiffRow
	DiffCursor      int
	DiffOffset      int
	// DiffContext is how many unchanged lines are kept either side of a change. The whole
	// value of the view is that it is short, so this starts low.
	DiffContext int
	diffEdits   []diff.Edit
	diffReturn  Screen

	// Details is what `--list-all --json` knows and the text form does not: where each task
	// is written, and whether go-task thinks it is up to date. Filled in from a background
	// goroutine, because computing it can take seconds on a workspace with a lot of
	// `sources:` globs and the UI is usable without it.
	Details map[string]task.Detail
	details pending[map[string]task.Detail]

	// Reaches is which aggregates run each namespace: `backend` is run by `fmt`, `lint`,
	// `test`. From `internal/cover`, on a background goroutine, because working it out means
	// a `task --summary` for every task and the list must not wait on that. Nil until it
	// lands, which the header rows read as "nothing to say yet" rather than as "nothing runs
	// this".
	Reaches map[string][]string
	reaches pending[covered]
	// calls is what every task calls, at every depth, from the same walk. What the danger
	// check reads to see past the task you started; empty until the walk lands.
	calls graph.Graph

	// Where the run on screen spent its time, slowest first.
	ProfileRows   []run.Cost
	ProfileCursor int
	ProfileOffset int
	profileReturn Screen
	// profileFinal is the finished run the profile last took its figures from, so it
	// refreshes once on the poll that ends the run and then holds still.
	profileFinal *run.Run

	// locs indexes the project so a `file:line` in captured output can be opened. Built on
	// first use — most sessions never press `e`.
	locs *loc.Resolver
	// pendingEdit is an editor waiting to be launched, parked here because a key handler
	// cannot return a Bubble Tea command.
	pendingEdit *loc.Editor

	// Viewport is the body height of the last frame, so `^d` can move by half a screen.
	Viewport int

	// FilterContext is the lines of context kept either side of a hit in the filtered
	// view. A `FAIL:` line without the assertion underneath it is the half that does not
	// tell you anything.
	FilterContext int

	// The args prompt. Plenty of tasks need arguments — `wt:new NAME=backend`,
	// `backend:test -- -p ingest` — and a runner that cannot pass them can only run half
	// of a real Taskfile.
	EnteringArgs bool
	ArgsInput    string
	// ArgsCursor is the caret position, in characters. Pre-filling `NAME=` is only useful
	// if the caret lands after the `=`.
	ArgsCursor int
	ArgsTarget string
	// argsVars is what the target task declares it requires, kept from the lookup that
	// pre-filled the prompt so ⇥ can complete against it without spending a second one.
	argsVars []string
	// argsPast is the argument lists this task has been run with before, and argsPastRead
	// says the archive has been walked — the walk is lazy, and finding nothing is an answer
	// worth remembering.
	argsPast     [][]string
	argsPastRead bool
	// argsComp is the ⇥-completion cycle, while one is running.
	argsComp *argsCompletion
	// argsPrefill is what BeginArgs put on the line and argsFromHistory says it came from
	// the archive rather than from a declaration. The footer says so only while the line is
	// still untouched — compared against the input rather than cleared on edit, because a
	// flag cleared in four places is a label that lies the once it is missed.
	argsPrefill     string
	argsFromHistory bool

	// Confirm is whatever is waiting on a yes. `⏎` runs things for real, and a fuzzy
	// filter puts every task one keypress away, so the ones that touch production get a
	// stop — as do the two ways of killing a run you cannot see.
	Confirm Confirm

	// SendingInput sends keystrokes to the running task instead of to taskui.
	// Deliberately a mode: half the run view's keys are single letters, and `y` meaning
	// both "yes" and "move the cursor" would be intolerable.
	SendingInput bool
	// InteractiveNext is sticky: the next run goes out interleaved so the task can ask
	// questions.
	InteractiveNext bool
	// ForceNext is sticky: the next run passes `--force`, so go-task's up-to-date checks
	// do not skip it.
	ForceNext bool

	// Cross-run search over the archive, from the history list.
	HistorySearching bool
	HistoryQuery     string
	// HistoryHits maps a run id to its hit count, for the runs that matched.
	HistoryHits map[string]int

	// Width and Height are the terminal size, as Bubble Tea reports it.
	Width  int
	Height int

	// Phase is which animation frame the cursor's edges are drawn at. It only moves for a
	// theme that asked to animate; everything else leaves it at zero, which is also what
	// keeps `--screenshot` deterministic.
	Phase int
	// animStart is when the animation began, which Phase is counted from.
	animStart time.Time

	// archive is where runs are kept. A field rather than a call so tests can point it
	// somewhere disposable.
	archive store.Archive

	// events is where a host — an editor showing this terminal — is told what the runs are
	// doing. Nil when nobody asked, which is every session started by hand.
	events *events.Sink
	// deltas is one tracker per run, so a busy run cannot renumber a quiet one. Per run
	// rather than per task name: a re-run of `test` is a new run, and a tracker that had
	// already said `exit` for the last one never said `run` or `exit` for this one.
	deltas map[*run.Run]*events.Deltas
}

func New(tasks []task.Task, root string) *App {
	a := &App{
		slot:          newSlot(nil, 0),
		Tasks:         tasks,
		Pivots:        pivot.Builtins(),
		Tree:          &pivot.Tree{},
		expanded:      map[string]map[string]bool{},
		Root:          root,
		Project:       task.ProjectDir(root),
		Theme:         theme.DefaultTheme(),
		Keymap:        keys.NewKeymap(),
		Screen:        ScreenPicker,
		PeekLines:     theme.DefaultPeekLines,
		Mouse:         theme.DefaultMouse,
		Outcomes:      map[string]store.Outcome{},
		HistoryHits:   map[string]int{},
		Viewport:      20,
		FilterContext: 2,
		DiffContext:   3,
		Width:         80,
		Height:        24,
		archive:       store.Default(),
	}
	a.Rebuild(-1)
	a.ReloadOutcomes()
	return a
}

// Archive is where this app keeps its runs.
func (a *App) Archive() store.Archive { return a.archive }

// SetArchive points the app at another archive, for tests.
func (a *App) SetArchive(archive store.Archive) {
	a.archive = archive
	a.ReloadOutcomes()
}

func (a *App) ReloadOutcomes() {
	a.Outcomes = a.archive.LastOutcomes(a.Root)
}

// WithConfig applies a loaded config. Anything wrong with the file is surfaced rather than
// swallowed — a colour that silently does nothing is worse than one that says why.
func (a *App) WithConfig(config theme.Config) *App {
	a.Theme = config.Theme
	a.Keymap = config.Keymap
	a.PeekLines = config.PeekLines
	a.Bell = config.Bell
	a.Mouse = config.Mouse
	a.Order = config.Order
	// Custom groupings go after the built-ins, in the order they were written — `p` cycles
	// through them, so the order in the file is the order at the keyboard.
	for _, spec := range config.Pivots {
		p, err := spec.Compile(a.Root)
		if err != nil {
			config.Problems = append(config.Problems, err.Error())
			continue
		}
		a.Pivots = append(a.Pivots, p)
	}
	if len(config.Problems) > 0 {
		a.Status = "config: " + strings.Join(config.Problems, "; ")
	}
	// The tree was built by New, before any of this was known. An `order:` that only took
	// effect after the first keypress would look like it had not been read at all.
	a.Rebuild(a.SelectedTask())
	return a
}

// Ordering is the configured order with the archive attached, which is what `recent` and
// `failed` are actually sorted on.
//
// Attached on every rebuild rather than stored, because the last outcome of every task
// changes whenever a run finishes.
func (a *App) Ordering() pivot.Order {
	order := a.Order
	order.Outcomes = make(map[string]pivot.Outcome, len(a.Outcomes))
	for name, outcome := range a.Outcomes {
		order.Outcomes[name] = pivot.Outcome{Ok: outcome.Ok, WhenUnix: outcome.WhenUnix}
	}
	return order
}

func clamp(v, lo, hi int) int {
	return min(max(v, lo), hi)
}
