package languages

import "time"

// SetParseBudget sets the parse time budget for a test and returns a
// function that restores it.
func SetParseBudget(d time.Duration) (restore func()) {
	old := parseBudget
	parseBudget = d
	return func() { parseBudget = old }
}
