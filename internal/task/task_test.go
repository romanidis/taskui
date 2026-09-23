package task

import (
	"reflect"
	"testing"
)

func TestParsesAPlainEntry(t *testing.T) {
	got, ok := parseEntry("* all:                           Everything: format, lint, test, build")
	if !ok {
		t.Fatal("expected an entry")
	}
	if got.Name != "all" {
		t.Errorf("name = %q", got.Name)
	}
	if got.Desc != "Everything: format, lint, test, build" {
		t.Errorf("desc = %q", got.Desc)
	}
	if len(got.Aliases) != 0 {
		t.Errorf("aliases = %v", got.Aliases)
	}
}

func TestParsesAliasesOffTheEnd(t *testing.T) {
	got, _ := parseEntry("* build:      Build all components        (aliases: b)")
	if got.Name != "build" || got.Desc != "Build all components" {
		t.Fatalf("got %+v", got)
	}
	if !reflect.DeepEqual(got.Aliases, []string{"b"}) {
		t.Errorf("aliases = %v", got.Aliases)
	}
}

func TestParsesNamespacedNames(t *testing.T) {
	got, _ := parseEntry("* backend:migrate:down:   Roll back the most recent migration")
	if got.Name != "backend:migrate:down" {
		t.Errorf("name = %q", got.Name)
	}
	if got.Desc != "Roll back the most recent migration" {
		t.Errorf("desc = %q", got.Desc)
	}
}

// A description ending in a parenthesis is not an alias list.
func TestAParenthesisedDescriptionIsNotEaten(t *testing.T) {
	got, _ := parseEntry("* setup:   Install tools and dependencies (safe to re-run)")
	if got.Desc != "Install tools and dependencies (safe to re-run)" {
		t.Errorf("desc = %q", got.Desc)
	}
	if len(got.Aliases) != 0 {
		t.Errorf("aliases = %v", got.Aliases)
	}
}

// `--list-all` includes tasks with no description at all.
func TestHandlesAMissingDescription(t *testing.T) {
	got, _ := parseEntry("* sec:secrets:dir:")
	if got.Name != "sec:secrets:dir" || got.Desc != "" {
		t.Errorf("got %+v", got)
	}
}

func TestIgnoresTheHeaderAndBlankLines(t *testing.T) {
	if _, ok := parseEntry("task: Available tasks for this project:"); ok {
		t.Error("header parsed as a task")
	}
	if _, ok := parseEntry(""); ok {
		t.Error("blank line parsed as a task")
	}
}

func taskWith(name, desc string) Task {
	return Task{Name: name, Desc: desc}
}

func TestMinesAUsageHintFromTheDescription(t *testing.T) {
	x := taskWith("backend:test", "Run Rust workspace tests (task backend:test -- -p ingest for one crate)")
	hint, _ := x.ArgsHint()
	if hint != "-- -p ingest for one crate" {
		t.Errorf("hint = %q", hint)
	}
}

// Included tasks describe themselves by their local name.
func TestMinesAHintWrittenWithTheBareName(t *testing.T) {
	x := taskWith("site:new", `New post — usage: task new -- "My Post Title" (title is slugified)`)
	hint, _ := x.ArgsHint()
	if hint != `-- "My Post Title"` {
		t.Errorf("hint = %q", hint)
	}
}

// The commentary after the arguments is not part of them.
func TestTrimsTrailingProseFromAHint(t *testing.T) {
	for _, c := range []struct{ name, desc, want string }{
		{"wt:rm", "Remove an agent's worktree: task wt:rm NAME=backend (the BRANCH survives)", "NAME=backend"},
		{"deploy:local", "Full deploy. Pass a target with `task deploy:local -- infra:deploy:plan`.", "-- infra:deploy:plan"},
		{"dev:run", "Run the backend once: task dev:run -- <args>, default `serve`", "-- <args>"},
	} {
		hint, _ := taskWith(c.name, c.desc).ArgsHint()
		if hint != c.want {
			t.Errorf("%s: hint = %q, want %q", c.name, hint, c.want)
		}
	}
}

// One description, several examples: the first is what the hint has room for, the rest are
// what ⇥ completes against.
func TestMinesEveryExampleADescriptionSpellsOut(t *testing.T) {
	x := taskWith("backend:test",
		"Run tests: task backend:test -- -p ingest, or task backend:test -- -p api --nocapture")
	want := []string{"-- -p ingest", "-- -p api --nocapture"}
	if got := x.ArgsHints(); !reflect.DeepEqual(got, want) {
		t.Errorf("hints = %q, want %q", got, want)
	}
	// The first of them is still exactly what the hint was before.
	if hint, _ := x.ArgsHint(); hint != want[0] {
		t.Errorf("hint = %q, want %q", hint, want[0])
	}
}

// The same example written twice is one example.
func TestTheSameExampleTwiceIsOfferedOnce(t *testing.T) {
	x := taskWith("site:new", `usage: task site:new -- "A Title"; or task new -- "A Title"`)
	if got := x.ArgsHints(); len(got) != 1 {
		t.Errorf("hints = %q, want one", got)
	}
}

func TestMinesBareAssignmentConventions(t *testing.T) {
	x := taskWith("backend:gen:migration", "Scaffold a migration and register it (NAME=add_x)")
	hint, _ := x.ArgsHint()
	if hint != "NAME=add_x" {
		t.Errorf("hint = %q", hint)
	}
}

// A parenthetical that is just prose is not a usage hint.
func TestDoesNotInventAHintFromOrdinaryProse(t *testing.T) {
	x := taskWith("setup", "Install tools and dependencies (safe to re-run)")
	if hint, ok := x.ArgsHint(); ok {
		t.Errorf("invented %q", hint)
	}
}

// A hint of the `KEY=value` shape tells us the key even when the task does not declare
// `requires:`.
func TestExtractsKeysFromAnAssignmentHint(t *testing.T) {
	for _, c := range []struct {
		in   string
		want []string
	}{
		{"NAME=backend", []string{"NAME"}},
		{"WORD=адрес", []string{"WORD"}},
		{"A=1 B=2", []string{"A", "B"}},
	} {
		if got := KeysInHint(c.in); !reflect.DeepEqual(got, c.want) {
			t.Errorf("KeysInHint(%q) = %v, want %v", c.in, got, c.want)
		}
	}
}

// `--` style arguments carry no key to pre-fill.
func TestExtractsNoKeysFromADashDashHint(t *testing.T) {
	for _, in := range []string{`-- "My Post Title"`, "-- -p ingest"} {
		if got := KeysInHint(in); len(got) != 0 {
			t.Errorf("KeysInHint(%q) = %v", in, got)
		}
	}
}

func TestGlobsMatchColonPaths(t *testing.T) {
	for _, c := range []struct {
		pattern, name string
		want          bool
	}{
		{"deploy:*", "deploy:backend", true},
		{"deploy:*", "deploy:logs:archive", true},
		{"deploy:*", "dev:up", false},
		{"backend:migrate:prod", "backend:migrate:prod", true},
		{"backend:migrate:prod", "backend:migrate:down", false},
		{"*:prod", "backend:promo:prod", true},
		{"*", "anything", true},
	} {
		if got := GlobMatch(c.pattern, c.name); got != c.want {
			t.Errorf("GlobMatch(%q, %q) = %v", c.pattern, c.name, got)
		}
	}
}

// Production-touching tasks are flagged so they are not one fuzzy keypress away.
func TestFlagsProductionTasks(t *testing.T) {
	for _, c := range []struct {
		line string
		want bool
	}{
		{"* deploy:backend:   Deploy the backend", true},
		{"* backend:migrate:prod:   Apply migrations", true},
		{"* backend:lint:   Rust lints", false},
	} {
		got, _ := parseEntry(c.line)
		if got.Dangerous != c.want {
			t.Errorf("%q dangerous = %v", c.line, got.Dangerous)
		}
	}
}

// --- talking to go-task ----------------------------------------------------------------

// go-task colours its output whenever the environment asks it to, and on a CI runner that
// is the ordinary case. A listing line that arrives coloured starts with an escape rather
// than with `* `, so the parser matches nothing and a project with forty tasks in it is
// reported as having none — no error, no empty output, nothing to notice.
//
// This is what that looked like: it cost a release, because the failure reached CI as a
// panic three packages away, in a test asserting on the first task of an empty list.
func TestAskTurnsTheColourOff(t *testing.T) {
	cmd := Ask(t.TempDir(), "--list-all")
	found := false
	for _, kv := range cmd.Env {
		if kv == "NO_COLOR=1" {
			found = true
		}
	}
	if !found {
		t.Errorf("every question we parse has to be asked in plain text: %v", cmd.Env)
	}
	if cmd.Args[0] != "task" || cmd.Args[1] != "--list-all" {
		t.Errorf("args = %v", cmd.Args)
	}
}

// And the parser is not left relying on that alone: NO_COLOR is a request to a program we
// do not control, and the cost of it being ignored is a silent empty list rather than an
// error. Stripping is the half we can guarantee.
func TestAColouredListingStillParses(t *testing.T) {
	coloured := "\x1b[33m* \x1b[0m\x1b[32mbuild\x1b[0m\x1b[0m:       Compile it\x1b[0m"
	got, ok := parseEntry(coloured)
	if !ok {
		t.Fatalf("a coloured line parsed as nothing: %q", coloured)
	}
	if got.Name != "build" {
		t.Errorf("name = %q", got.Name)
	}
	if got.Desc != "Compile it" {
		t.Errorf("desc = %q", got.Desc)
	}
}

func TestNamespaceDefaultIsNamedByItsNamespace(t *testing.T) {
	got, ok := canonical(Task{Name: "dev:default", Aliases: []string{"dev", "d"}})
	if !ok {
		t.Fatal("expected the task to be kept")
	}
	if got.Name != "dev" {
		t.Errorf("name = %q", got.Name)
	}
	// go-task adds the namespace itself as an alias of its default; that is the name now.
	if !reflect.DeepEqual(got.Aliases, []string{"d"}) {
		t.Errorf("aliases = %v", got.Aliases)
	}
}

func TestRootDefaultIsDropped(t *testing.T) {
	if _, ok := canonical(Task{Name: "default"}); ok {
		t.Error("the root default has no name to run it by")
	}
}

func TestOrdinaryNamesPassThrough(t *testing.T) {
	got, ok := canonical(Task{Name: "backend:migrate:down"})
	if !ok || got.Name != "backend:migrate:down" {
		t.Errorf("got %+v, %v", got, ok)
	}
}

func TestADeclaredPatternCoversTheDefaultItWasWrittenFor(t *testing.T) {
	// `deploy:default` is listed as `deploy`, which `deploy:*` does not match on its own.
	if !dangerous(Task{Name: "deploy"}, "deploy:default", []string{"deploy:*"}) {
		t.Error("deploy:* should still cover what `task deploy` runs")
	}
	if dangerous(Task{Name: "build"}, "build", []string{"deploy:*"}) {
		t.Error("a declared list is the whole answer")
	}
}

func TestTheHeuristicSeesTheNameTheDefaultIsShownBy(t *testing.T) {
	// Listed as `backend:prod:default`, which has no `:prod` suffix; shown as `backend:prod`.
	if !dangerous(Task{Name: "backend:prod"}, "backend:prod:default", nil) {
		t.Error("backend:prod should be flagged however go-task spells it")
	}
}

func TestNamesResolveEverySpellingToTheListedOne(t *testing.T) {
	n := namesOf([]Task{
		{Name: "build", Aliases: []string{"b"}},
		{Name: "dev:default", Aliases: []string{"dev:dd", "dev"}},
		{Name: "default"},
	})
	for spelled, want := range map[string]string{
		"b": "build", "build": "build",
		"dev:default": "dev", "dev:dd": "dev", "dev": "dev",
		// Never listed, so never renamed.
		"dev:inner": "dev:inner", "default": "default",
	} {
		if got := n.Canonical(spelled); got != want {
			t.Errorf("Canonical(%q) = %q, want %q", spelled, got, want)
		}
	}
}

func TestARootTaskKeepsItsNameAgainstAnIncludedDefault(t *testing.T) {
	n := namesOf([]Task{
		{Name: "dev"},
		{Name: "dev:default", Aliases: []string{"dev"}},
	})
	if got := n.Canonical("dev"); got != "dev" {
		t.Errorf("dev = %q", got)
	}
	// It lost the name to the root task, so it keeps its own rather than sharing a row.
	if got := n.Canonical("dev:default"); got != "dev:default" {
		t.Errorf("dev:default = %q", got)
	}
}

// go-task runs a task whose name has a space in it, and lists it with one.
func TestANameWithASpaceIsOneName(t *testing.T) {
	got, ok := parseEntry("* weird name:       spaced: out")
	if !ok || got.Name != "weird name" || got.Desc != "spaced: out" {
		t.Errorf("got %+v", got)
	}
}

// Lines as go-task 3.53.1 lists them. A real alias list stands in its own column, padded
// away from the description; a description that only ends in something like one does not.
func TestAnAliasListIsOnlyTheColumnGoTaskPadded(t *testing.T) {
	for _, c := range []struct {
		line    string
		desc    string
		aliases []string
	}{
		{"* trailing:         has trailing (aliases: q)", "has trailing (aliases: q)", nil},
		{"* real:             real one                    (aliases: r, re)", "real one", []string{"r", "re"}},
		{"* both:             tricky (aliases: fake)      (aliases: b)", "tricky (aliases: fake)", []string{"b"}},
		{"* c:             (aliases: cc)", "", []string{"cc"}},
	} {
		got, _ := parseEntry(c.line)
		if got.Desc != c.desc || !reflect.DeepEqual(got.Aliases, c.aliases) {
			t.Errorf("%q: desc %q aliases %v, want %q %v", c.line, got.Desc, got.Aliases, c.desc, c.aliases)
		}
	}
}

// The text form can only guess at aliases; the JSON listing knows. Its answer wins, an
// empty one included.
func TestTheJSONListingsAliasesWin(t *testing.T) {
	x := Task{Name: "trailing", Desc: "has trailing", Aliases: []string{"q"}}
	x = x.With(Detail{Aliases: []string{}})
	if len(x.Aliases) != 0 {
		t.Errorf("aliases = %v", x.Aliases)
	}
}
