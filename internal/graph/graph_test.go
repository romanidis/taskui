package graph

import (
	"fmt"
	"reflect"
	"sort"
	"strings"
	"testing"
)

// Real `task --summary` output: the env dump comes first and must not be mistaken for
// edges.
const lint = `task: lint

Lint all source code

env:
  TELEGRAM_BOT_USERNAME: "Atlasiobot"
  LOG_REDACT_PII: "false"

commands:
 - Task: api:check
 - Task: backend:lint
 - Task: app:lint
 - Task: infra:lint
`

const mid = `task: mid

(task does not have description or summary)

dependencies:
 - a

commands:
 - Task: b
`

const shellOnly = `task: fmt

commands:
 - cargo fmt --all
 - terraform fmt -recursive
`

func TestParsesTaskEdgesFromCommands(t *testing.T) {
	want := []string{"api:check", "backend:lint", "app:lint", "infra:lint"}
	if got, _ := parseSummary(lint); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

// Dependencies run before commands, so they come first in the child order.
func TestDependenciesPrecedeCommands(t *testing.T) {
	if got, _ := parseSummary(mid); !reflect.DeepEqual(got, []string{"a", "b"}) {
		t.Errorf("got %v", got)
	}
}

// Shell commands under `commands:` are not edges.
func TestShellCommandsAreNotEdges(t *testing.T) {
	if got, _ := parseSummary(shellOnly); len(got) != 0 {
		t.Errorf("got %v", got)
	}
}

// The env dump is the dangerous case: `KEY: "value"` lines look nothing like ` - x` items,
// but a sloppy parser that just scanned for colons would swallow them.
func TestEnvDumpYieldsNoEdges(t *testing.T) {
	envOnly := "task: x\n\nenv:\n  API_TOKEN: \"cfut_secret\"\n  OTHER: \"Task: nope\"\n"
	if got, _ := parseSummary(envOnly); len(got) != 0 {
		t.Errorf("got %v", got)
	}
}

// Real `--summary` output for a task declaring `requires: { vars: [NAME] }`.
const full = `task: wt:new

New worktree + branch for an agent: task wt:new NAME=backend

vars:

env:
  CLOUDFLARE_API_TOKEN: "cfut_realtoken"
  S3_REGION: "us-east-1"

requires:
  vars:
    - NAME

dependencies:
 - setup

commands:
 - Task: build
 - set -eu
dir=".worktrees/"
git worktree add "$dir"
`

// The env block is the resolved environment — live credentials for a Taskfile with
// `dotenv:` — and this output goes on screen.
func TestTheDetailNeverCarriesTheEnvironment(t *testing.T) {
	all := fmt.Sprintf("%+v", parseDetail(full))
	if strings.Contains(all, "cfut_realtoken") {
		t.Errorf("a secret leaked into the panel: %s", all)
	}
	if strings.Contains(all, "S3_REGION") {
		t.Errorf("and neither should the harmless ones: %s", all)
	}
}

func TestParsesWhatATaskIsAndDoes(t *testing.T) {
	d := parseDetail(full)
	wantSummary := []string{"New worktree + branch for an agent: task wt:new NAME=backend"}
	if !reflect.DeepEqual(d.Summary, wantSummary) {
		t.Errorf("summary = %v", d.Summary)
	}
	if !reflect.DeepEqual(d.Requires, []string{"NAME"}) {
		t.Errorf("requires = %v", d.Requires)
	}
	if !reflect.DeepEqual(d.Dependencies, []string{"setup"}) {
		t.Errorf("dependencies = %v", d.Dependencies)
	}
	// Multi-line shell keeps its shape: one command, not one per line.
	want := []string{"Task: build", "set -eu\ndir=\".worktrees/\"\ngit worktree add \"$dir\""}
	if !reflect.DeepEqual(d.Commands, want) {
		t.Errorf("commands = %q", d.Commands)
	}
}

// Real `--summary` output, go-task 3.53.1: a multi-line command's continuation lines come
// at the margin, with a blank line inside the block and another after it.
const multiline = `task: all

(task does not have description or summary)

dependencies:
 - dep1

commands:
 - echo one
echo two

echo after-blank

 - Task: child
 - echo cmd-form
 - echo start
- Task: fake

 - Task: last
`

// A continuation line is the command carrying on, not the end of the section: every edge
// after the first multi-line command used to be lost.
func TestEdgesAfterAMultiLineCommandAreKept(t *testing.T) {
	got, deps := parseSummary(multiline)
	if want := []string{"dep1", "child", "last"}; !reflect.DeepEqual(got, want) {
		t.Errorf("edges = %v, want %v", got, want)
	}
	if !reflect.DeepEqual(deps, []string{"dep1"}) {
		t.Errorf("deps = %v", deps)
	}
}

func TestAMultiLineCommandIsOneCommand(t *testing.T) {
	want := []string{
		"echo one\necho two\n\necho after-blank",
		"Task: child",
		"echo cmd-form",
		// A `- Task:` at the margin is inside the shell block; only ` - ` starts an item.
		"echo start\n- Task: fake",
		"Task: last",
	}
	if got := parseDetail(multiline).Commands; !reflect.DeepEqual(got, want) {
		t.Errorf("commands = %q", got)
	}
}

// "(task does not have description or summary)" is go-task's stand-in, not content.
func TestTheNoDescriptionPlaceholderIsDropped(t *testing.T) {
	d := parseDetail(mid)
	if len(d.Summary) != 0 {
		t.Errorf("summary = %v", d.Summary)
	}
	if !reflect.DeepEqual(d.Dependencies, []string{"a"}) {
		t.Errorf("dependencies = %v", d.Dependencies)
	}
}

func TestParsesRequiredVars(t *testing.T) {
	text := "task: wt:new\n\nNew worktree\n\nvars:\n\nenv:\n  S3_REGION: \"us-east-1\"\n\nrequires:\n    - NAME\n\ncommands:\n - set -eu\n"
	if got := parseRequires(text); !reflect.DeepEqual(got, []string{"NAME"}) {
		t.Errorf("got %v", got)
	}
}

func TestParsesSeveralRequiredVars(t *testing.T) {
	got := parseRequires("requires:\n    - NAME\n    - WORD\n\ncommands:\n")
	if !reflect.DeepEqual(got, []string{"NAME", "WORD"}) {
		t.Errorf("got %v", got)
	}
}

// A task with no `requires:` block asks for nothing.
func TestNoRequiresBlockYieldsNothing(t *testing.T) {
	if got := parseRequires(lint); len(got) != 0 {
		t.Errorf("got %v", got)
	}
}

// The dependency list must not be mistaken for required variables.
func TestDependenciesAreNotRequiredVars(t *testing.T) {
	if got := parseRequires(mid); len(got) != 0 {
		t.Errorf("got %v", got)
	}
}

// resolveWith is the test double for resolveParallel: the same walk, without the process
// spawns.
func resolveWith(root string, fetch func(string) string) Graph {
	g := New()
	queue := []string{root}
	for len(queue) > 0 {
		task := queue[len(queue)-1]
		queue = queue[:len(queue)-1]
		if _, known := g.Edges[task]; known {
			continue
		}
		children, _ := parseSummary(fetch(task))
		for _, c := range children {
			if _, known := g.Edges[c]; !known {
				queue = append(queue, c)
			}
		}
		g.Edges[task] = children
	}
	return g
}

func TestResolveWalksTheWholeGraphOncePerTask(t *testing.T) {
	var calls []string
	g := resolveWith("all", func(x string) string {
		calls = append(calls, x)
		switch x {
		case "all":
			return "commands:\n - Task: lint\n - Task: test\n"
		// A diamond: both sides reach backend:lint.
		case "lint", "test":
			return "commands:\n - Task: backend:lint\n"
		}
		return ""
	})

	if !reflect.DeepEqual(g.Children("all"), []string{"lint", "test"}) {
		t.Errorf("children(all) = %v", g.Children("all"))
	}
	if len(g.Children("backend:lint")) != 0 {
		t.Errorf("children(backend:lint) = %v", g.Children("backend:lint"))
	}
	sort.Strings(calls)
	if !reflect.DeepEqual(calls, []string{"all", "backend:lint", "lint", "test"}) {
		t.Errorf("one call each: %v", calls)
	}
}

func TestResolveTerminatesOnACycle(t *testing.T) {
	g := resolveWith("a", func(x string) string {
		switch x {
		case "a":
			return "commands:\n - Task: b\n"
		case "b":
			return "commands:\n - Task: a\n"
		}
		return ""
	})
	if !reflect.DeepEqual(g.Children("a"), []string{"b"}) {
		t.Errorf("children(a) = %v", g.Children("a"))
	}
	if !reflect.DeepEqual(g.Children("b"), []string{"a"}) {
		t.Errorf("children(b) = %v", g.Children("b"))
	}
}

func TestReachableListsTheSubtreeDepthFirst(t *testing.T) {
	g := resolveWith("all", func(x string) string {
		switch x {
		case "all":
			return "commands:\n - Task: lint\n - Task: test\n"
		case "lint":
			return "commands:\n - Task: backend:lint\n"
		}
		return ""
	})
	want := []string{"all", "lint", "backend:lint", "test"}
	if got := g.Reachable("all"); !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}
}

func TestRenamingMergesTwoSpellingsOfOneTask(t *testing.T) {
	g := New()
	g.Edges["all"] = []string{"b", "build", "dev"}
	g.Edges["b"] = nil
	g.Edges["dev"] = []string{"dev:inner"}
	g.Deps["dev"] = []string{"dev:inner"}
	names := map[string]string{"b": "build", "dev": "dev"}
	got := g.Renamed(func(n string) string {
		if c, ok := names[n]; ok {
			return c
		}
		return n
	})
	if !reflect.DeepEqual(got.Children("all"), []string{"build", "dev"}) {
		t.Errorf("children(all) = %v", got.Children("all"))
	}
	if _, ok := got.Edges["build"]; !ok {
		t.Error("a leaf keeps its entry under its new name")
	}
	if !reflect.DeepEqual(got.Deps["dev"], []string{"dev:inner"}) {
		t.Errorf("deps = %v", got.Deps)
	}
}
