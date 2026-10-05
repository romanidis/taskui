// Package graph reconstructs the execution graph: which tasks a task invokes.
//
// `task --list-all --json` does not carry dependencies, and parsing the Taskfiles
// ourselves would mean reimplementing `includes:` resolution and then tracking go-task's
// semantics forever. `task --summary <name>` already reports a task's direct edges using
// go-task's own resolver, so recursing that from whatever was invoked reconstructs the
// graph for the cost of a few process spawns.
//
// Note that `--summary` also prints the resolved environment, which for a Taskfile with
// `dotenv:` means real credentials. That output is parsed in memory and dropped; it must
// never be persisted or displayed.
package graph

import (
	"slices"
	"sort"
	"strings"
	"sync"

	taskpkg "github.com/romanidis/taskui/internal/task"
)

// Graph maps a task name to the tasks it invokes, in the order it invokes them.
type Graph struct {
	Edges map[string][]string
	// Deps is the subset of Edges that came from `deps:` rather than from a `task:` command.
	//
	// Kept apart because go-task runs `deps:` concurrently and commands in order, and the
	// run parser cannot tell from the output which it is watching: with `--output prefixed`
	// two concurrent deps interleave their lines, and a parser that reads "someone else
	// spoke" as "the last one finished" closes a task that is still going. Nothing persists
	// this — a stored run is already finished, so the distinction only matters live.
	Deps map[string][]string
}

func New() Graph {
	return Graph{Edges: map[string][]string{}, Deps: map[string][]string{}}
}

// Concurrent reports whether two tasks can be running at once: siblings under one parent's
// `deps:`, which go-task starts together — or anything beneath two such siblings. A task
// inside `backend` and one inside `web`, both deps of `all`, interleave their lines exactly
// as `backend` and `web` do, and reading one's output as the other having finished marked
// half a parallel build done while it was still printing.
func (g Graph) Concurrent(a, b string) bool {
	if a == b {
		return false
	}
	lineA, lineB := g.lineage(a), g.lineage(b)
	// One running inside the other is nesting, not two things at once.
	if lineA[b] || lineB[a] {
		return false
	}
	for _, deps := range g.Deps {
		var seenA, seenB bool
		for _, d := range deps {
			// Where the two lines part: a dep above one and not the other. A dep above both
			// is a common ancestor, which says nothing about how they run beneath it.
			seenA = seenA || (lineA[d] && !lineB[d])
			seenB = seenB || (lineB[d] && !lineA[d])
		}
		if seenA && seenB {
			return true
		}
	}
	return false
}

// lineage is name and everything that invokes it, at any depth.
func (g Graph) lineage(name string) map[string]bool {
	out := map[string]bool{name: true}
	for grew := true; grew; {
		grew = false
		for parent, children := range g.Edges {
			if out[parent] {
				continue
			}
			for _, c := range children {
				if out[c] {
					out[parent] = true
					grew = true
					break
				}
			}
		}
	}
	return out
}

// Renamed is g with every task spelled the way name spells it. Two spellings of one task —
// an edge to the alias `b` and another to `build` — become one node.
func (g Graph) Renamed(name func(string) string) Graph {
	out := New()
	rename := func(from, to map[string][]string) {
		for parent, children := range from {
			p := name(parent)
			kept := to[p]
			for _, c := range children {
				if c = name(c); !slices.Contains(kept, c) {
					kept = append(kept, c)
				}
			}
			to[p] = kept
		}
	}
	rename(g.Edges, out.Edges)
	rename(g.Deps, out.Deps)
	return out
}

func (g Graph) Children(task string) []string {
	return g.Edges[task]
}

// Names lists every task with an entry, sorted — the deterministic stand-in for Rust's
// ordered map, so anything that iterates the graph renders the same way twice.
func (g Graph) Names() []string {
	out := make([]string, 0, len(g.Edges))
	for k := range g.Edges {
		out = append(out, k)
	}
	sort.Strings(out)
	return out
}

// Walk visits root and everything under it, depth first and in invocation order. A task
// reached a second time is visited again with repeat set and not descended into, so a
// diamond is shown in full once and a cycle ends. Returning false from visit stops the walk.
func (g Graph) Walk(root string, visit func(name string, depth int, repeat bool) bool) {
	type frame struct {
		name  string
		depth int
	}
	seen := map[string]bool{}
	stack := []frame{{root, 0}}
	for len(stack) > 0 {
		top := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		repeat := seen[top.name]
		seen[top.name] = true
		if !visit(top.name, top.depth, repeat) {
			return
		}
		if repeat {
			continue
		}
		for _, c := range slices.Backward(g.Children(top.name)) {
			stack = append(stack, frame{c, top.depth + 1})
		}
	}
}

// Reachable lists every task reachable from root, including root, depth first.
func (g Graph) Reachable(root string) []string {
	var out []string
	seen := map[string]bool{}
	stack := []string{root}
	for len(stack) > 0 {
		t := stack[len(stack)-1]
		stack = stack[:len(stack)-1]
		if seen[t] {
			continue
		}
		seen[t] = true
		children := g.Children(t)
		for _, c := range slices.Backward(children) {
			stack = append(stack, c)
		}
		out = append(out, t)
	}
	return out
}

// varsHeading is the sub-heading `requires:` nests its names under; it is also a
// top-level section of its own, which is exactly why both parsers have to step over it
// rather than treat it as a boundary.
const varsHeading = "vars:"

type section int

const (
	sectionNone section = iota
	sectionDeps
)

// commandsHeading opens the last section `--summary` prints. Nothing follows it, which is
// what lets everything after it be read as commands however it looks.
const commandsHeading = "commands:"

// summaryCommands splits what follows `commands:` into one entry per command.
//
// Each command is a ` - ` item, but a multi-line one carries on with its continuation lines
// printed at the margin — ` - echo one`, then `echo two` — so a line that is not an item is
// the command before it going on, not the end of the section. Reading it as the end is what
// lost every `Task:` edge after the first multi-line command: the run tree went without
// them and `--lint` reported gaps that were not there.
//
// A blank line inside a block is part of it. The ones at the end of a block are the block's
// own trailing newline and go-task's separator, and are dropped.
//
// The text form cannot say everything. A continuation line that itself starts with ` - `
// reads as a new item, and nothing here can tell it apart; this is the case the shape of the
// output leaves ambiguous, not one the parser gets wrong.
func summaryCommands(lines []string) []string {
	var out []string
	for _, line := range lines {
		line = strings.TrimRight(line, " \t\r")
		if item, ok := strings.CutPrefix(line, " - "); ok {
			out = append(out, item)
			continue
		}
		if len(out) > 0 {
			out[len(out)-1] += "\n" + line
		}
	}
	for i := range out {
		out[i] = strings.TrimRight(out[i], "\n")
	}
	return out
}

// taskCall is the task a command calls, when it is `Task: <name>` rather than shell. A
// call is always one line, so a multi-line command is shell whatever its first line says.
func taskCall(command string) (string, bool) {
	name, ok := strings.CutPrefix(command, "Task: ")
	if !ok || strings.Contains(name, "\n") {
		return "", false
	}
	return strings.TrimSpace(name), true
}

// parseSummary returns a task's direct edges, dependencies first (go-task runs those
// before the commands), and separately the dependencies on their own.
//
// Both, because the two are run differently — `deps:` concurrently, commands in order — and
// the run parser has to know which it is watching. Flattening them into one list, which is
// all the graph used to keep, is why a parallel build reported half of itself finished the
// moment the other half printed anything.
func parseSummary(text string) ([]string, []string) {
	at := sectionNone
	deps := []string{}
	var cmds []string

	lines := strings.Split(text, "\n")
	for i, line := range lines {
		trimmed := strings.TrimRight(line, " \t\r")
		switch strings.TrimSpace(trimmed) {
		case "dependencies:":
			at = sectionDeps
			continue
		case commandsHeading:
			// Under `commands:` only `Task: x` entries are edges; the rest are shell.
			for _, c := range summaryCommands(lines[i+1:]) {
				if name, ok := taskCall(c); ok {
					cmds = append(cmds, name)
				}
			}
			return append(append([]string{}, deps...), cmds...), deps
		}

		// Items are ` - <thing>`. Anything else — the env dump, the description, blank
		// lines — ends the section we were in.
		item, ok := strings.CutPrefix(trimmed, " - ")
		if !ok {
			if trimmed != "" {
				at = sectionNone
			}
			continue
		}
		// An item outside `dependencies:` is not an edge.
		if at == sectionDeps {
			deps = append(deps, strings.TrimSpace(item))
		}
	}

	// Copied rather than appended in place: the caller keeps both, and `append(deps, …)`
	// would hand it two slices over one array.
	return append(append([]string{}, deps...), cmds...), deps
}

// RequiredVars lists the variables a task declares with `requires: { vars: [NAME] }`.
//
// This is a fact rather than a guess mined from prose, so the args prompt can pre-fill
// `NAME=` and know it is asking for something real. One `--summary` call, ~40ms, made only
// when the prompt opens.
func RequiredVars(dir, task string) []string {
	return parseRequires(summaryOf(dir, task))
}

func parseRequires(text string) []string {
	var out []string
	inside := false
	for line := range strings.SplitSeq(text, "\n") {
		trimmed := strings.TrimSpace(line)
		if trimmed == "requires:" {
			inside = true
			continue
		}
		if !inside {
			continue
		}
		if name, ok := strings.CutPrefix(trimmed, "- "); ok {
			out = append(out, strings.TrimSpace(name))
			continue
		}
		// The block nests — the names sit under a `vars:` sub-heading — so that one line
		// is stepped over rather than treated as the end of the section.
		if trimmed == varsHeading || trimmed == "" {
			continue
		}
		break
	}
	return out
}

// Detail is what a task actually is: what it says it does, what it needs, and what it will
// run.
type Detail struct {
	Summary      []string
	Requires     []string
	Dependencies []string
	Commands     []string
}

type detailSection int

const (
	detailDescription detailSection = iota
	detailSecrets
	detailRequires
	detailDependencies
)

// parseDetail turns `task --summary` into something showable.
//
// The `vars:` and `env:` blocks are dropped on the floor. That is not tidiness: `env:` is
// the resolved environment, so for a Taskfile with `dotenv:` it contains live credentials,
// and this output goes on screen.
func parseDetail(text string) Detail {
	var d Detail
	at := detailDescription

	lines := strings.Split(text, "\n")
	// The first line is `task: <name>`, which the UI already knows.
	if len(lines) > 0 {
		lines = lines[1:]
	}

	for i, line := range lines {
		trimmed := strings.TrimSpace(line)
		// Inside `requires:` the names nest under their own `vars:` sub-heading, which
		// must not be mistaken for the top-level one.
		if at == detailRequires && trimmed == varsHeading {
			continue
		}
		switch trimmed {
		case varsHeading, "env:":
			at = detailSecrets
			continue
		case "requires:":
			at = detailRequires
			continue
		case "dependencies:":
			at = detailDependencies
			continue
		case commandsHeading:
			// Commands keep their shape: a multi-line shell block is one command, and
			// reflowing it would misrepresent what runs.
			d.Commands = summaryCommands(lines[i+1:])
			return d
		}

		switch at {
		case detailSecrets:
			// Dropped on the floor, deliberately.
		case detailDescription:
			// go-task's stand-in for "no description" is noise here.
			if trimmed != "" && !strings.HasPrefix(trimmed, "(task does not have") {
				d.Summary = append(d.Summary, trimmed)
			}
		case detailRequires:
			if name, ok := strings.CutPrefix(trimmed, "- "); ok {
				d.Requires = append(d.Requires, strings.TrimSpace(name))
			}
		case detailDependencies:
			if name, ok := strings.CutPrefix(trimmed, "- "); ok {
				d.Dependencies = append(d.Dependencies, strings.TrimSpace(name))
			}
		}
	}
	return d
}

// DescribeTask makes one `--summary` call, when the detail panel opens.
func DescribeTask(dir, task string) Detail {
	return parseDetail(summaryOf(dir, task))
}

func summaryOf(dir, task string) string {
	// Through task.Ask, so the summary arrives plain: this output is parsed, and go-task
	// colours it whenever something in the environment asks it to.
	cmd := taskpkg.Ask(dir, "--summary", task)
	// A task that cannot be summarised is not fatal — it just has no known children.
	out, _ := cmd.Output()
	return string(out)
}

// Resolve walks outward from root, one `--summary` per distinct task.
func Resolve(dir, root string) Graph {
	g, _ := ResolveDetailed(dir, root)
	return g
}

// ResolveAll is the graph of everything roots reach, one `--summary` per distinct task
// however many of the roots reach it.
//
// For asking about every task at once. Resolving them one at a time repeats the part their
// graphs share — every aggregate's walk summarised `backend:lint` again — and the tasks of
// one Taskfile share most of theirs.
func ResolveAll(dir string, roots []string) Graph {
	g, _ := resolveParallel(roots, summarising(dir))
	return g
}

// ResolveDetailed is Resolve, but also hands back the raw `--summary` text of every task
// in the graph.
//
// That text contains each task's environment — which is exactly what the redactor needs
// in order to know what to mask. Every task's, not only the root's: `all` calling a
// `deploy` whose own `env:` holds the token prints that token from `deploy`, and the root's
// summary never mentions it. It is returned rather than stored so the caller is forced
// to decide what happens to it; it must not be persisted or displayed.
func ResolveDetailed(dir, root string) (Graph, string) {
	return resolveParallel([]string{root}, summarising(dir))
}

// summarising fetches a task's `--summary` from dir. A parameter of the walk rather than
// called by it, so a test can hand the real walk a Taskfile's worth of summaries without
// go-task installed.
func summarising(dir string) func(string) string {
	return func(task string) string { return summaryOf(dir, task) }
}

// lanes is enough to hide the latency without spawning a process per task in a wide graph.
const lanes = 8

// resolveParallel resolves one frontier at a time, fetching each frontier's tasks
// concurrently.
//
// One `--summary` is a process spawn — around 40ms — and a big aggregate has dozens of
// them. Serially that was over a second of dead time before an aggregate produced a single
// line. The graph is a level-order walk anyway, so each level's calls are independent.
//
// Tasks are memoised and revisits short-circuit, so a diamond (`all` reaching `lint` and
// `check`, both reaching `backend:*`) costs one call per node, and a cycle terminates
// instead of spinning.
func resolveParallel(roots []string, fetch func(string) string) (Graph, string) {
	g := New()
	var summaries strings.Builder
	var frontier []string
	for _, root := range roots {
		if !contains(frontier, root) {
			frontier = append(frontier, root)
		}
	}

	for len(frontier) > 0 {
		var next []string

		for start := 0; start < len(frontier); start += lanes {
			end := min(start+lanes, len(frontier))
			batch := frontier[start:end]

			results := make([][]string, len(batch))
			depResults := make([][]string, len(batch))
			texts := make([]string, len(batch))
			var wg sync.WaitGroup
			for i, task := range batch {
				wg.Go(func() {
					texts[i] = fetch(task)
					results[i], depResults[i] = parseSummary(texts[i])
				})
			}
			wg.Wait()

			for i, task := range batch {
				summaries.WriteString(texts[i])
				summaries.WriteString("\n")
				for _, c := range results[i] {
					if _, known := g.Edges[c]; !known && !contains(next, c) {
						next = append(next, c)
					}
				}
				g.Edges[task] = results[i]
				if len(depResults[i]) > 0 {
					g.Deps[task] = depResults[i]
				}
			}
		}

		// Anything already resolved by an earlier level is not revisited, so a diamond
		// costs one call and a cycle terminates.
		frontier = frontier[:0]
		for _, t := range next {
			if _, known := g.Edges[t]; !known {
				frontier = append(frontier, t)
			}
		}
	}

	return g, summaries.String()
}

func contains(haystack []string, needle string) bool {
	return slices.Contains(haystack, needle)
}
