package run

import (
	"errors"
	"os"
	"os/exec"
	"sync"
	"sync/atomic"
	"syscall"
	"time"

	"github.com/creack/pty"
)

// stopGrace is how long the process group gets after go-task has gone, before the rest is
// taken.
//
// go-task exits the moment it is signalled, which is not the moment its commands are done
// reacting: a compose stack stopping containers is still working, and interrupting it is
// how you end up with half a stack. Long enough for that, short enough that stopping still
// feels like stopping.
const stopGrace = time.Second

// process is the child behind a run, and everything that reaches it: starting it, typing at
// it, stopping it and reaping it.
//
// It is the part of a run that more than one goroutine touches, so every field is a lock or
// an atomic. A run with no child behind it, stored or built for a test, has one all the
// same: the flags are how the UI tells a run it asked to stop from one it did not, and
// there is simply never a leader to signal.
type process struct {
	// mu guards the leader and its terminal, which the capture goroutine publishes once
	// the child has started.
	mu     sync.Mutex
	leader *os.Process
	master *os.File
	// reapMu keeps the group signal and the wait on the leader from overlapping.
	reapMu sync.Mutex
	// reaped is set by the capture goroutine once it has waited on the leader, which is the
	// moment the leader's pid stops being safe to signal.
	reaped atomic.Bool

	// cancelled and killed are written by whoever stops the run — the UI, or a signal
	// handler on a goroutine of its own — and read by the capture goroutine deciding
	// whether to start the child at all.
	cancelled atomic.Bool
	// killed records whether the polite signals have already been sent and ignored. Kept
	// so a second `x` can escalate rather than sending a process the same signal it just
	// sat through.
	killed atomic.Bool
	// now: something asked this run to stop and then asked again, or is quitting: skip what
	// is left of the grace.
	now atomic.Bool
}

// start runs cmd on a pty of its own and publishes it, unless the run was stopped first.
//
// Started and published under one lock, with the stop checked inside it. stop sets the flag
// before it looks for a leader, so either this sees the flag and never starts the child, or
// stop sees the leader and signals it. Checked only before, the child could start in the
// gap after a stop that found nothing to signal, and run to completion — which is what `x`
// during graph resolution did.
func (p *process) start(cmd *exec.Cmd) (*os.File, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	if p.cancelled.Load() {
		return nil, errStoppedBeforeStart
	}
	master, err := pty.StartWithSize(cmd, &pty.Winsize{
		Rows: 50,
		// Wide, so tools that wrap to the terminal width do not hard-wrap the capture at
		// something narrow. The UI wraps for display instead.
		Cols: 200,
	})
	if err != nil {
		return nil, err
	}
	p.leader, p.master = cmd.Process, master
	return master, nil
}

// write types at the child. False when there is no child yet, or its terminal would not
// take the bytes.
func (p *process) write(bytes []byte) bool {
	p.mu.Lock()
	master := p.master
	p.mu.Unlock()
	if master == nil {
		return false
	}
	_, err := master.Write(bytes)
	return err == nil
}

// stop sends the polite signals Run.Cancel describes, and leaves reapGroup to insist once
// the grace is up.
func (p *process) stop() {
	// First, so a capture goroutine that has not started the child yet never does.
	p.cancelled.Store(true)
	p.mu.Lock()
	leader := p.leader
	p.mu.Unlock()
	if leader == nil {
		return
	}
	// Under reapMu, and only if the leader has not been waited on: once it has, its pid is
	// back in circulation and the signal would go to a stranger. Tried rather than waited
	// for, because whoever holds it is reaping this run already, and waiting would hang
	// the UI on that.
	if !p.reapMu.TryLock() {
		return
	}
	if !p.reaped.Load() {
		// SIGTERM first so the tools get to clean up after themselves.
		_ = syscall.Kill(-leader.Pid, syscall.SIGTERM)
		// SIGHUP is the backstop for a child that never became a group leader — and is
		// still catchable, which is the point.
		_ = leader.Signal(syscall.SIGHUP)
	}
	p.reapMu.Unlock()
	go p.reapGroup()
}

// kill takes the group now, without waiting out the grace.
func (p *process) kill() {
	// All three: a kill that arrives before anything was asked politely still has to leave
	// a stopped run behind, and one the capture goroutine will not start if it has not yet.
	p.cancelled.Store(true)
	p.killed.Store(true)
	p.now.Store(true)
	// Safe on a group that is already gone: reapGroup looks, under the lock the reaping
	// takes, before it sends anything to a pid that may belong to somebody else by now.
	go p.reapGroup()
}

// reapGroup takes what is left of the process group once the grace is up.
//
// This runs on its own goroutine rather than after the capture loop, and that is the whole
// point. go-task catches both polite signals and then waits for its commands, so a command
// that ignores signals keeps it alive; go-task is the session leader holding the pty; and
// while anything in the group holds the pty slave open, the master never reaches EOF and
// the capture goroutine stays blocked in its read. Hanging the cleanup off the end of that
// read meant the cleanup could only run once the thing it was cleaning up had already let
// go.
//
// macOS hid this. BSD revokes the controlling terminal when the session leader dies, so
// taking the leader was enough to force the EOF and everything downstream ran. Linux does
// not revoke, so the same run simply never ended — which is exactly what
// `a command that ignores SIGTERM does not outlive the run` was written to catch, and did,
// on the first CI run that put it on Linux.
//
// Killing the group is what frees the pty on both, so the read ends because the run is
// over rather than the run ending because the read did.
func (p *process) reapGroup() {
	// go-task exits the instant it is signalled; its commands are still reacting. Wait out
	// the grace before insisting, unless somebody already has.
	deadline := time.Now().Add(stopGrace)
	for time.Now().Before(deadline) && !p.now.Load() {
		if p.reaped.Load() {
			return
		}
		time.Sleep(25 * time.Millisecond)
	}

	// Under the same lock the capture goroutine waits on, so the group can never be
	// signalled after the leader has been reaped: that pid goes straight back into
	// circulation, and signalling a recycled one means signalling a stranger.
	p.reapMu.Lock()
	defer p.reapMu.Unlock()
	if p.reaped.Load() {
		return
	}
	p.mu.Lock()
	leader := p.leader
	p.mu.Unlock()
	if leader != nil {
		_ = syscall.Kill(-leader.Pid, syscall.SIGKILL)
		_ = leader.Signal(syscall.SIGKILL)
	}
}

// wait reaps the leader and says how it exited: -1 when it cannot tell.
//
// Under the same lock reapGroup signals under, which is what keeps the two apart. A pid
// names its group only until it is waited on; after that it names whatever the OS hands it
// to next, and a signal arriving in that window would go to a stranger.
func (p *process) wait(cmd *exec.Cmd) int {
	code := -1
	p.reapMu.Lock()
	if err := cmd.Wait(); err == nil {
		code = 0
	} else if state := cmd.ProcessState; state != nil {
		code = state.ExitCode()
	}
	p.reaped.Store(true)
	p.reapMu.Unlock()
	return code
}

// errStoppedBeforeStart is capture declining to start a child that was stopped while the
// graph was still being resolved.
var errStoppedBeforeStart = errors.New("stopped before it started")
