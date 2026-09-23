package watch

import (
	"os"
	"path/filepath"
	"strconv"
	"testing"
	"time"
)

func TestBuildOutputIsIgnored(t *testing.T) {
	for _, path := range []string{
		"/proj/target/debug/taskui",
		"/proj/.git/index",
		"/proj/.task/checksum/lint",
		"/proj/app/node_modules/x/index.js",
	} {
		if !isNoise("/proj", path) {
			t.Errorf("%s should be ignored", path)
		}
	}
}

// Editors write a lot of things that are not source changes.
func TestEditorScratchFilesAreIgnored(t *testing.T) {
	for _, path := range []string{
		"/proj/src/.main.go.swp",
		"/proj/src/main.go~",
		"/proj/src/4913",
		"/proj/.DS_Store",
	} {
		if !isNoise("/proj", path) {
			t.Errorf("%s should be ignored", path)
		}
	}
}

func TestSourceFilesAreNot(t *testing.T) {
	for _, path := range []string{
		"/proj/src/main.go",
		"/proj/Taskfile.yml",
		"/proj/backend/internal/api/api.go",
	} {
		if isNoise("/proj", path) {
			t.Errorf("%s should not be ignored", path)
		}
	}
}

// One save is several events; firing on the first would build a half-written file.
func TestABurstOfEventsSettlesBeforeFiring(t *testing.T) {
	dir := t.TempDir()
	w, err := Start(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	w.Settle = 120 * time.Millisecond

	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("one"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "a.go"), []byte("two"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Give the platform watcher a moment to deliver.
	deadline := time.Now().Add(5 * time.Second)
	fired := ""
	for time.Now().Before(deadline) {
		if path, ok := w.Poll(); ok {
			fired = path
			break
		}
		time.Sleep(20 * time.Millisecond)
	}

	if fired == "" {
		t.Fatal("the change never fired")
	}
	if _, ok := w.Poll(); ok {
		t.Error("and it should only fire once")
	}
}

// fire waits for a watch to settle on something, or gives up.
func fire(t *testing.T, w *Watch) string {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if path, ok := w.Poll(); ok {
			return path
		}
		time.Sleep(20 * time.Millisecond)
	}
	return ""
}

// A named watch answers for its own files and nothing else — the Taskfile at the top of a
// project whose build output churns is the whole reason it is not a tree walk.
func TestANamedWatchOnlyFiresForItsOwnFiles(t *testing.T) {
	dir := t.TempDir()
	taskfile := filepath.Join(dir, "Taskfile.yml")
	for _, name := range []string{"Taskfile.yml", "main.go"} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte("one"), 0o600); err != nil {
			t.Fatal(err)
		}
	}

	w, err := Files([]string{taskfile})
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	w.Settle = 120 * time.Millisecond

	// Something else in the same directory is not this watch's business.
	if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("two"), 0o600); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	if _, ok := w.Poll(); ok {
		t.Error("a file it was not asked about fired it")
	}

	if err := os.WriteFile(taskfile, []byte("two"), 0o600); err != nil {
		t.Fatal(err)
	}
	if fired := fire(t, w); filepath.Base(fired) != "Taskfile.yml" {
		t.Errorf("fired on %q, want the Taskfile", fired)
	}
	if _, ok := w.Poll(); ok {
		t.Error("and it should only fire once")
	}
}

func TestANamedWatchWithNothingToWatchSaysSo(t *testing.T) {
	if _, err := Files(nil); err == nil {
		t.Error("watching nothing succeeded")
	}
}

// Something that never stops writing — a log in the tree — must not hold a re-run off
// forever: past MaxWait it fires with the writes still coming.
func TestAChangeFiresEvenWhileWritesKeepComing(t *testing.T) {
	dir := t.TempDir()
	w, err := Start(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	w.Settle = 200 * time.Millisecond
	w.MaxWait = 600 * time.Millisecond

	deadline := time.Now().Add(5 * time.Second)
	for i := 0; time.Now().Before(deadline); i++ {
		// Faster than Settle, so it never settles.
		if err := os.WriteFile(filepath.Join(dir, "server.log"), []byte(strconv.Itoa(i)), 0o600); err != nil {
			t.Fatal(err)
		}
		if _, ok := w.Poll(); ok {
			return
		}
		time.Sleep(50 * time.Millisecond)
	}
	t.Fatal("a steady stream of writes held the watch off for good")
}

func TestAMetadataChangeIsNotAnEdit(t *testing.T) {
	dir := t.TempDir()
	file := filepath.Join(dir, "a.go")
	if err := os.WriteFile(file, []byte("x"), 0o600); err != nil {
		t.Fatal(err)
	}
	w, err := Start(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer w.Close()
	w.Settle = 50 * time.Millisecond

	if err := os.Chmod(file, 0o644); err != nil {
		t.Fatal(err)
	}
	deadline := time.Now().Add(500 * time.Millisecond)
	for time.Now().Before(deadline) {
		if path, ok := w.Poll(); ok {
			t.Fatalf("a chmod fired the watch: %s", path)
		}
		time.Sleep(20 * time.Millisecond)
	}
}
