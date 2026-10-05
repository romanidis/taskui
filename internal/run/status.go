package run

type Status int

const (
	// Pending means in the graph but not reached yet.
	Pending Status = iota
	Running
	Ok
	Failed
	// Skipped means the run ended before this task was reached — usually because
	// something upstream failed.
	Skipped
)

func (s Status) Glyph() string {
	switch s {
	case Running:
		return "▶"
	case Ok:
		return "✓"
	case Failed:
		return "✗"
	case Skipped:
		return "⏸"
	default:
		return "·"
	}
}

// Settled reports whether a task in this status has finished, one way or another: a status
// it does not come back from.
func (s Status) Settled() bool { return s == Ok || s == Failed || s == Skipped }

// String is the name persisted in a manifest, and parsed back by the store.
func (s Status) String() string {
	switch s {
	case Running:
		return "Running"
	case Ok:
		return "Ok"
	case Failed:
		return "Failed"
	case Skipped:
		return "Skipped"
	default:
		return "Pending"
	}
}

// MarshalText writes a status by its name, which is what the archive has always stored:
// manifests read `"status": "Ok"`, and a Status that marshalled as its number would be a
// format change made by accident.
func (s Status) MarshalText() ([]byte, error) { return []byte(s.String()), nil }

// UnmarshalText reads a name back. One this build does not know reads as Pending, as
// StatusFromString always has, so a manifest a newer build wrote still loads.
func (s *Status) UnmarshalText(text []byte) error {
	*s = StatusFromString(string(text))
	return nil
}

func StatusFromString(s string) Status {
	switch s {
	case "Ok":
		return Ok
	case "Failed":
		return Failed
	case "Running":
		return Running
	case "Skipped":
		return Skipped
	default:
		return Pending
	}
}
