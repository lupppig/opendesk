package tickets

// Search is search, for tests that call it as a principal the HTTP harness can't sign in
// (a person acting through an OAuth grant).
var Search = search

// SetSummaryCap lowers the most tickets GET /v1/tickets/summary counts, until the returned
// function puts it back.
func SetSummaryCap(n int) func() {
	old := summaryCap
	summaryCap = n
	return func() { summaryCap = old }
}
