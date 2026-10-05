package app

func (a *App) BeginInput() {
	if !a.RunInFlight() {
		a.Status = "nothing is running to type at"
		return
	}
	a.SendingInput = true
	a.Following = true
	a.Status = ""
}

func (a *App) EndInput() { a.SendingInput = false }

// SendInput forwards keystrokes to the task's terminal.
func (a *App) SendInput(bytes []byte) {
	ok := a.Run != nil && a.Run.SendInput(bytes)
	if !ok {
		// Silence after a keystroke is ambiguous enough already; a write that failed must
		// not look the same as one that landed.
		a.Status = "that keystroke went nowhere — the task has finished or closed its input"
		a.SendingInput = false
	}
}

// AwaitingInput reports whether the task is sitting on an unanswered question.
func (a *App) AwaitingInput() bool {
	return a.Run != nil && !a.Run.Finished() && a.Run.LooksLikeAPrompt()
}
