package store

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// saveMany archives n runs of distinct tasks in one project, oldest first.
func saveMany(t *testing.T, archive Archive, project string, n int) {
	t.Helper()
	for i := range n {
		if _, err := archive.Save(project, agedRun("t"+string(rune('a'+i)), "task", true, n-i)); err != nil {
			t.Fatal(err)
		}
	}
}

// The reason the ledger exists: one project's runs used to evict another's, because the cap
// counted every project's against the same fifty. A timeline with one point in it and a
// `--flaky` that never fires are what that cost.
func TestOneProjectsRunsDoNotEvictAnothersHistory(t *testing.T) {
	archive := At(t.TempDir())
	saveMany(t, archive, "/alpha", 3)
	// Enough to push alpha's runs past any plausible output cap.
	saveMany(t, archive, "/beta", 4)
	if _, err := archive.Prune(2); err != nil {
		t.Fatal(err)
	}

	alpha := 0
	for _, m := range archive.List() {
		if m.Dir == "/alpha" {
			alpha++
		}
	}
	if alpha != 3 {
		t.Errorf("alpha's three runs should survive beta's; %d left", alpha)
	}
}

// A timeline is built from verdicts and durations, which the ledger holds, so it keeps
// answering after the text is gone. This is the whole point of splitting the two.
func TestATimelineOutlivesTheOutput(t *testing.T) {
	archive := At(t.TempDir())
	for i := range 4 {
		if _, err := archive.Save("/proj", agedRun("run"+string(rune('a'+i)), "test", i%2 == 0, 4-i)); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := archive.Prune(1); err != nil {
		t.Fatal(err)
	}

	if got := len(archive.Timeline("/proj", "test")); got != 4 {
		t.Errorf("the timeline should still hold four points, got %d", got)
	}
}

// A diff needs the text, so it looks past runs that are only remembered. Comparing against a
// run whose lines are gone would report every line as deleted, which is worse than saying
// there is nothing to compare with.
func TestADiffSkipsRunsWhoseOutputIsGone(t *testing.T) {
	archive := At(t.TempDir())
	if _, err := archive.Save("/proj", agedRun("old", "test", true, 9)); err != nil {
		t.Fatal(err)
	}
	if _, err := archive.Save("/proj", agedRun("mid", "test", true, 5)); err != nil {
		t.Fatal(err)
	}
	if _, err := archive.Save("/proj", agedRun("new", "test", false, 1)); err != nil {
		t.Fatal(err)
	}
	// Keeps the newest two, so `old` is remembered without its text.
	if _, err := archive.Prune(2); err != nil {
		t.Fatal(err)
	}

	green, ok := archive.LastGreen("/proj", "test", "", 0)
	if !ok {
		t.Fatal("there is still a green run with output")
	}
	if !archive.HasOutput(green.RunID) {
		t.Errorf("%s was picked to diff against and has no output", green.RunID)
	}
	if green.Run.Task != "mid" {
		t.Errorf("want the newest green run that still has text, got %q", green.Run.Task)
	}
}

// Reopening a run whose output was pruned says so. Rebuilding it would produce a run with
// every task empty, which reads as "it printed nothing".
func TestLoadingAPrunedRunSaysTheOutputIsGone(t *testing.T) {
	archive := At(t.TempDir())
	saveMany(t, archive, "/proj", 3)
	if _, err := archive.Prune(1); err != nil {
		t.Fatal(err)
	}

	all := archive.List()
	oldest := all[len(all)-1]
	_, err := archive.Load(oldest)
	if err == nil {
		t.Fatal("want an error for a run with no output left")
	}
	if !strings.Contains(err.Error(), "no longer stored") {
		t.Errorf("the message should say what happened, got %q", err)
	}
}

// An archive written before the ledger existed is absorbed by the next save rather than
// needing a migration step of its own.
func TestAnOlderArchiveIsAbsorbedOnTheNextSave(t *testing.T) {
	archive := At(t.TempDir())
	saveMany(t, archive, "/proj", 2)

	// What an archive from before the ledger looks like: run directories, no history file.
	if err := os.Remove(archive.historyPath()); err != nil {
		t.Fatal(err)
	}
	if got := len(archive.List()); got != 2 {
		t.Fatalf("the directories alone should still list, got %d", got)
	}

	if _, err := archive.Save("/proj", agedRun("third", "task", true, 1)); err != nil {
		t.Fatal(err)
	}
	if got := len(archive.readHistory()); got != 3 {
		t.Errorf("the ledger should have absorbed the two older runs, holds %d", got)
	}
}

// Merged on id, so a run that is in both places is one run.
func TestARunInBothPlacesIsListedOnce(t *testing.T) {
	archive := At(t.TempDir())
	saveMany(t, archive, "/proj", 3)
	if got := len(archive.List()); got != 3 {
		t.Errorf("listed %d", got)
	}
}

// The file is appended to by several processes at once, so a half-written last line is a
// thing that can happen. One unreadable run is not a reason to lose the others.
func TestATornLineDoesNotLoseTheLedger(t *testing.T) {
	archive := At(t.TempDir())
	saveMany(t, archive, "/proj", 3)

	blob, err := os.ReadFile(archive.historyPath())
	if err != nil {
		t.Fatal(err)
	}
	torn := string(blob) + `{"id":"tor`
	if err := os.WriteFile(archive.historyPath(), []byte(torn), 0o600); err != nil {
		t.Fatal(err)
	}
	if got := len(archive.readHistory()); got != 3 {
		t.Errorf("want the three good lines, got %d", got)
	}
}

// Compaction keeps the newest of each project rather than the newest overall, which is the
// same mistake the global cap made.
func TestCompactionKeepsEachProjectsNewest(t *testing.T) {
	archive := At(t.TempDir())
	var b strings.Builder
	write := func(id, dir string, started int64) {
		b.WriteString(`{"version":1,"id":"` + id + `","root":"r","dir":"` + dir +
			`","started_unix":` + itoa(started) + "}\n")
	}
	// More lines than compactAt, so compaction runs.
	for i := range compactAt + 10 {
		write("beta"+itoa(int64(i)), "/beta", int64(1000+i))
	}
	write("alpha-1", "/alpha", 1)
	if err := os.WriteFile(archive.historyPath(), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}

	if err := archive.compactHistory(); err != nil {
		t.Fatal(err)
	}

	kept := archive.readHistory()
	alpha, beta := 0, 0
	for _, m := range kept {
		switch m.Dir {
		case "/alpha":
			alpha++
		case "/beta":
			beta++
		}
	}
	if alpha != 1 {
		t.Errorf("alpha's single old run should survive beta's flood, got %d", alpha)
	}
	if beta != KeepHistory {
		t.Errorf("beta should be trimmed to %d, got %d", KeepHistory, beta)
	}
}

func itoa(n int64) string {
	if n == 0 {
		return "0"
	}
	var digits []byte
	for n > 0 {
		digits = append([]byte{byte('0' + n%10)}, digits...)
		n /= 10
	}
	return string(digits)
}

// The ledger lives beside the runs rather than in a config directory: deleting the state
// directory has always been how you remove everything taskui accumulated.
func TestTheLedgerLivesBesideTheRuns(t *testing.T) {
	archive := At(t.TempDir())
	saveMany(t, archive, "/proj", 1)
	if _, err := os.Stat(filepath.Join(archive.dir, "history.ndjson")); err != nil {
		t.Errorf("want the ledger in the state directory: %v", err)
	}
}

// Past compactAt with nothing to drop — six projects, each within what it keeps — there is
// nothing for a rewrite to do, and every save used to do it anyway. So did a single line
// over one project's limit.
func TestCompactionLeavesALedgerItCannotShrinkMuchAlone(t *testing.T) {
	archive := At(t.TempDir())
	var b strings.Builder
	n := int64(0)
	for p := range 6 {
		for range KeepHistory {
			n++
			b.WriteString(`{"version":1,"id":"r` + itoa(n) + `","root":"r","dir":"/p` + itoa(int64(p)) +
				`","started_unix":` + itoa(n) + "}\n")
		}
	}
	// One over the limit: droppable, and not worth a rewrite on its own.
	b.WriteString(`{"version":1,"id":"extra","root":"r","dir":"/p0","started_unix":0}` + "\n")
	if err := os.WriteFile(archive.historyPath(), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	old := time.Now().Add(-time.Hour)
	if err := os.Chtimes(archive.historyPath(), old, old); err != nil {
		t.Fatal(err)
	}

	if err := archive.compactHistory(); err != nil {
		t.Fatal(err)
	}
	info, err := os.Stat(archive.historyPath())
	if err != nil {
		t.Fatal(err)
	}
	if !info.ModTime().Equal(old) {
		t.Error("the ledger was rewritten to drop almost nothing")
	}
}

// A line a newer taskui wrote is one this build cannot read, which does not make it this
// build's to throw away when it compacts.
func TestCompactionKeepsWhatANewerBuildWrote(t *testing.T) {
	archive := At(t.TempDir())
	var b strings.Builder
	for i := range compactAt + KeepHistory/2 {
		b.WriteString(`{"version":1,"id":"r` + itoa(int64(i)) + `","root":"r","dir":"/beta","started_unix":` +
			itoa(int64(1000+i)) + "}\n")
	}
	future := `{"version":2,"id":"from-the-future","root":"r","dir":"/beta","started_unix":1}`
	b.WriteString(future + "\n")
	if err := os.WriteFile(archive.historyPath(), []byte(b.String()), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := archive.compactHistory(); err != nil {
		t.Fatal(err)
	}
	blob, err := os.ReadFile(archive.historyPath())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(blob), future) {
		t.Error("compacting dropped a run a newer build recorded")
	}
	if n := strings.Count(string(blob), "\n"); n >= compactAt {
		t.Errorf("did not compact: %d lines", n)
	}
}
