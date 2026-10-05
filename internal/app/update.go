package app

import (
	"fmt"
	"os"
	"os/exec"
	"time"

	tea "charm.land/bubbletea/v2"
)

// tickMsg drives the poll loop. A run's capture goroutine fills its queue whenever it
// likes; this is what drains it and redraws.
type tickMsg struct{}

// liveTick is the cadence while something is running, idleTick the one while nothing is.
//
// Any slot counts, not just the one on screen: the picker ticks an elapsed time for every
// running task, and a parked run's counter should not advance in 200ms lurches while the
// focused one gets 50.
const (
	liveTick = 50 * time.Millisecond
	idleTick = 200 * time.Millisecond
)

// statusLife is how long a notice holds the footer before it hands the keys back.
//
// The status line and the hint bar are the same row, so a message that never leaves is a
// footer you cannot read — and `grouped by verb` was still sitting there twenty minutes
// later, hiding the only place the keys are listed. Three seconds catches it on the way
// past and gives the row back before you go looking for a binding.
const statusLife = 3 * time.Second

func (a *App) tick() tea.Cmd {
	d := idleTick
	if a.AnyInFlight() {
		d = liveTick
	}
	// A theme that animates needs a frame on its own schedule, and the poll loop is
	// already the thing that wakes up — so it wakes up a little more often rather than a
	// second timer racing it.
	if step := a.Theme.Animation.Interval; a.Theme.Animation.Moves() && step < d {
		d = step
	}
	return tea.Tick(d, func(time.Time) tea.Msg { return tickMsg{} })
}

func (a *App) Init() tea.Cmd { return a.tick() }

// animationPhase is the frame the animation is on at now: one per Interval since it began.
//
// Counted from the clock rather than from ticks, because the tick is the poll loop's and
// runs at the poll loop's rate — every 200ms idle and every 50ms during a run — whenever
// that is faster than the theme's own frame. Counting ticks played synthwave's two-second
// frames ten times too fast while idle and forty times too fast while anything ran.
func (a *App) animationPhase(now time.Time) int {
	if a.animStart.IsZero() {
		a.animStart = now
	}
	return int(now.Sub(a.animStart) / a.Theme.Animation.Interval)
}

// Update is the loop's one door, which is what lets the status line be timed in one place:
// whatever a message did to it, expireStatus sees the result on the way out.
func (a *App) Update(msg tea.Msg) (tea.Model, tea.Cmd) {
	model, cmd := a.update(msg)
	a.expireStatus()
	return model, cmd
}

// expireStatus takes the footer back once a notice has had its three seconds.
//
// Timed from here rather than from a setter every caller has to remember: a status is a
// plain field written in a hundred places, and noticing that the text changed is the same
// thing as noticing it was set. Re-arming on a change means a notice that arrives while
// another is showing gets its own three seconds rather than the rest of someone else's.
func (a *App) expireStatus() {
	if a.Status != a.statusShown {
		a.statusShown, a.statusAt = a.Status, time.Now()
		return
	}
	if a.Status != "" && time.Since(a.statusAt) >= statusLife {
		a.Status, a.statusShown = "", ""
	}
}

func (a *App) update(msg tea.Msg) (tea.Model, tea.Cmd) {
	switch msg := msg.(type) {
	case tea.WindowSizeMsg:
		a.Width, a.Height = msg.Width, msg.Height
		return a, nil

	case editorFailed:
		a.Status = "could not run your editor: " + msg.err.Error()
		return a, nil

	case tickMsg:
		if a.Theme.Animation.Moves() {
			a.Phase = a.animationPhase(time.Now())
		}
		a.PollRun()
		a.PollWatch()
		a.PollTaskfile()
		a.collectDetails()
		a.collectCoverage()
		a.collectReload()
		a.RefreshProfile()
		a.noteFinished()
		return a, tea.Batch(a.tick(), a.ringBell())

	case tea.MouseWheelMsg:
		a.handleWheel(tea.Mouse(msg).Button)
		return a, nil

	// Presses only. v2 can also report releases and repeats, but only if a frame asks for
	// them, and nothing here wants a key twice.
	case tea.KeyPressMsg:
		if a.HandleKey(fromTea(msg)) {
			a.shutdown()
			return a, tea.Quit
		}
		a.PollRun()
		a.PollWatch()
		a.RefreshProfile()
		a.noteFinished()
		return a, tea.Batch(a.launchEditor(), a.ringBell())
	}
	return a, nil
}

// ringBell writes a BEL, if one is owed.
//
// From a command rather than from the model: Update is the only place allowed to touch the
// terminal, and a stray byte written mid-frame lands in the middle of whatever was being
// drawn. BEL is safe to send inside the alternate screen — it moves no cursor and occupies
// no cell — so what the terminal does with it, flash or beep or nothing, stays the
// terminal's business.
func (a *App) ringBell() tea.Cmd {
	if !a.TakeBell() {
		return nil
	}
	return func() tea.Msg {
		fmt.Fprint(os.Stdout, "\a")
		return nil
	}
}

// launchEditor runs whatever `e` asked for, if anything.
//
// A terminal editor gets the terminal: Bubble Tea puts it back the way it found it, runs
// the program attached to the real stdin and stdout, and redraws afterwards. Anything that
// opens its own window is run alongside instead — handing the terminal to a `code --goto`
// that returns in ten milliseconds blacks the UI out for no reason, and on a slow start it
// looks like a crash.
func (a *App) launchEditor() tea.Cmd {
	editor, ok := a.TakeEdit()
	if !ok {
		return nil
	}
	//nolint:gosec // this is $EDITOR being run on purpose; the argv is built from the
	// variable the user set and a path that had to exist on disk to get here.
	cmd := exec.Command(editor.Name, editor.Args...)
	if !editor.Terminal {
		return func() tea.Msg {
			// Detached: the window it opens outlives the keystroke, and its exit status is
			// not something taskui has an opinion about.
			if err := cmd.Start(); err != nil {
				return editorFailed{err}
			}
			go func() { _ = cmd.Wait() }()
			return nil
		}
	}
	return tea.ExecProcess(cmd, func(err error) tea.Msg {
		if err != nil {
			return editorFailed{err}
		}
		return nil
	})
}

// editorFailed reports an editor that would not start — a misspelled $EDITOR is otherwise a
// key that appears to do nothing.
type editorFailed struct{ err error }

// shutdownGrace is how long to wait for the slots to report themselves gone before
// insisting.
//
// Deliberately longer than the grace a stopped run gives its own process group, so the
// normal case completes inside it and quitting does not routinely escalate.
const shutdownGrace = 2 * time.Second

// killGrace is how long to wait after insisting. SIGKILL is not instant — the exit still
// has to travel back up the queue — but it is not slow either.
const killGrace = 500 * time.Millisecond

// shutdown leaves, taking every running child with us.
//
// Every slot, not just the one on screen: the whole point of parking a run is that you
// stop looking at it, and a background container left behind on quit is exactly the orphan
// this tool exists to not create.
//
// We wait here rather than leaving. A stopped run only reports itself finished once its
// capture goroutine has taken the process group with it, which means waiting for that
// report is the difference between a stack that is down and a stack that is merely no
// longer being watched. Anything still going when the grace runs out gets SIGKILL, which
// is also what shortens its capture goroutine's own wait.
func (a *App) shutdown() {
	if !a.AnyInFlight() {
		return
	}
	a.CancelAll()
	a.drainUntilGone(shutdownGrace)
	if a.AnyInFlight() {
		a.KillAll()
		a.drainUntilGone(killGrace)
	}
}

func (a *App) drainUntilGone(grace time.Duration) {
	deadline := time.Now().Add(grace)
	for time.Now().Before(deadline) && a.AnyInFlight() {
		a.PollRun()
		time.Sleep(25 * time.Millisecond)
	}
}
