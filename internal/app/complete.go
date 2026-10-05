package app

import (
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
)

// Completion for the args prompt.
//
// Half a real Taskfile needs arguments, and the prompt already knows which ones: it opens
// pre-filled with the `requires: vars` the task declares. What it could not do was finish
// a *value* — the path you are pointing at, or the `ENV=staging` you typed last Tuesday —
// so a prompt that knew the shape of the answer still made you type all of it, exactly.
//
// Three sources, in the order they are worth offering:
//
//   - the variables the task asks for, which is a declaration and therefore certain;
//   - the arguments this same task was actually run with before, out of the archive —
//     the strongest signal there is, because you chose it once already;
//   - files and directories under the project, for the arguments that are paths.
//
// Cycling rather than a longest-common-prefix insert: this is one footer line, the
// candidate is shown in place as you tab through it, and a prefix insert that stops on an
// ambiguity would need somewhere else to say why it stopped.

// argsCompletion is one run of ⇥-completion: the input either side of the word being
// completed, and where in the candidates we are.
//
// The two halves are captured when the cycle is armed rather than recomputed per keypress,
// so tabbing past a candidate longer than the word cannot walk the boundaries.
type argsCompletion struct {
	head, tail string
	cands      []string
	idx        int
}

// maxArgCandidates caps what one ⇥ offers.
//
// A project root with two hundred files would otherwise arm a cycle you cannot tab out of,
// and the fix for a list that long is to type another letter, not to press ⇥ again.
const maxArgCandidates = 50

// CompleteArgs advances the args prompt's completion: +1 for ⇥, -1 for ⇧⇥.
//
// The first press gathers the candidates and shows the first; the rest walk the same list.
// Anything else typed into the prompt drops the state, so the list can never be stale.
func (a *App) CompleteArgs(delta int) {
	if a.argsComp == nil {
		head, word, tail := a.argsWordAtCursor()
		cands := a.argsCandidates(word)
		if len(cands) == 0 {
			// Said out loud: a ⇥ that silently does nothing is indistinguishable from a
			// prompt that does not complete at all, which is what this exists to fix.
			a.Status = "no completion for `" + word + "`"
			return
		}
		a.argsComp = &argsCompletion{head: head, tail: tail, cands: cands}
	} else {
		n := len(a.argsComp.cands)
		a.argsComp.idx = (a.argsComp.idx + delta + n) % n
	}
	c := a.argsComp
	pick := c.cands[c.idx]
	a.ArgsInput = c.head + pick + c.tail
	a.ArgsCursor = len([]rune(c.head)) + len([]rune(pick))
	a.Status = ""
}

// argsWordAtCursor splits the input around the word the cursor sits in.
//
// The word ends *at* the cursor, not at the next space: completing `ENV=pro|d` should offer
// what starts with `pro` and leave the `d` where you left it, the same way a shell does.
func (a *App) argsWordAtCursor() (string, string, string) {
	runes := []rune(a.ArgsInput)
	at := clamp(a.ArgsCursor, 0, len(runes))
	start := at
	for start > 0 && runes[start-1] != ' ' {
		start--
	}
	return string(runes[:start]), string(runes[start:at]), string(runes[at:])
}

// argsCandidates is what ⇥ offers for one word, in the order the sources are worth it.
func (a *App) argsCandidates(word string) []string {
	var out []string
	seen := map[string]bool{}
	// The first of each is kept, so the order the sources come in survives, up to the cap.
	offer := func(cand string) {
		if !seen[cand] && len(out) < maxArgCandidates {
			seen[cand] = true
			out = append(out, cand)
		}
	}

	// Past the `=` the key is settled and only its value is in question: the values this
	// variable was run with before, newest first, then paths.
	if key, value, ok := strings.Cut(word, "="); ok {
		for _, args := range a.argsHistory() {
			for _, arg := range args {
				if k, v, ok := strings.Cut(arg, "="); ok && k == key && strings.HasPrefix(v, value) {
					offer(key + "=" + v)
				}
			}
		}
		for _, p := range a.pathsMatching(value) {
			offer(key + "=" + p)
		}
		return out
	}

	// The variables the task asks for, as `NAME=`. Taken from what BeginArgs already looked
	// up: the prompt spends one `--summary` opening, and asking again on every ⇥ would spend
	// it forty times over for an answer that cannot have changed.
	var vars []string
	for _, v := range a.argsVars {
		if strings.HasPrefix(v, word) {
			vars = append(vars, v+"=")
		}
	}
	sort.Strings(vars)
	for _, v := range vars {
		offer(v)
	}

	// Every argument this task was run with before, newest run first.
	for _, args := range a.argsHistory() {
		for _, arg := range args {
			if strings.HasPrefix(arg, word) {
				offer(arg)
			}
		}
	}

	// The examples the description spells out, whole. They come last of the three that are
	// about *this* task because they are the author's guess where the other two are
	// declarations and decisions — but they are the only source that works on a Taskfile you
	// have never run, which is the first time you need one.
	for _, t := range a.Tasks {
		if t.Name != a.ArgsTarget {
			continue
		}
		for _, hint := range t.ArgsHints() {
			if strings.HasPrefix(hint, word) {
				offer(hint)
			}
		}
	}

	for _, p := range a.pathsMatching(word) {
		offer(p)
	}
	return out
}

// argsHistory is the archived argument lists for the task the prompt is aimed at, newest
// run first.
//
// Read at most once per prompt, and only when something needs it: when the prompt opens with
// nothing declared to pre-fill, because the last run's arguments are the best answer then,
// or on the first ⇥. It is a walk of every manifest in the archive, and a prompt whose
// variables were declared, closed without a ⇥, never pays for it.
func (a *App) argsHistory() [][]string {
	if a.argsPastRead {
		return a.argsPast
	}
	a.argsPastRead = true
	for _, m := range a.archive.Runs(a.Root) {
		if m.Root == a.ArgsTarget && len(m.Args) > 0 {
			a.argsPast = append(a.argsPast, m.Args)
		}
	}
	return a.argsPast
}

// pathsMatching completes a path against the project, directories keeping their slash so
// the next ⇥ walks into them.
//
// Relative to the project root rather than to the working directory, because that is where
// the task will run and therefore what its own paths mean. Hidden entries stay hidden
// unless you have typed the dot that asks for them — a project root is mostly dotfiles, and
// offering them all would bury the three files you meant.
func (a *App) pathsMatching(prefix string) []string {
	dir, stem := path.Split(prefix)
	entries, err := os.ReadDir(filepath.Join(a.Root, filepath.FromSlash(dir)))
	if err != nil {
		return nil
	}
	var out []string
	for _, e := range entries {
		name := e.Name()
		if !strings.HasPrefix(name, stem) {
			continue
		}
		if strings.HasPrefix(name, ".") && !strings.HasPrefix(stem, ".") {
			continue
		}
		if e.IsDir() {
			name += "/"
		}
		out = append(out, dir+name)
	}
	sort.Strings(out)
	return out
}
