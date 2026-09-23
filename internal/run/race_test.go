//go:build race

package run

// raceDetector says whether this test binary was built with -race, which slows everything
// in it several times over.
const raceDetector = true
