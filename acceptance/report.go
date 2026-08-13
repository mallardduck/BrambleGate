package main

import (
	"fmt"
	"io"
	"time"

	"github.com/mallardduck/BrambleGate/acceptance/checks"
)

// WriteTable renders results as a testing-guide.md-style markdown table, so a
// run's output can be pasted straight into that doc's results-log sections.
func WriteTable(w io.Writer, results []checks.Result) {
	fmt.Fprintln(w, "| Date | Check | Scope | Tier | Status | Detail |")
	fmt.Fprintln(w, "| --- | --- | --- | --- | --- | --- |")
	date := time.Now().Format("2006-01-02")
	for _, r := range results {
		fmt.Fprintf(w, "| %s | %s | %s | %s | %s | %s |\n", date, r.Check, r.Scope, r.Tier, r.Status, r.Detail)
	}
}

// Summary counts results by status.
func Summary(results []checks.Result) map[checks.Status]int {
	counts := map[checks.Status]int{}
	for _, r := range results {
		counts[r.Status]++
	}
	return counts
}
