package main

import (
	"fmt"
	"io"
	"time"

	"github.com/fenixstarlord/replicant/internal/scanner"
)

// Every tape operation is visible even when stderr is a pipe to the app.
// Waiting means the operation has not returned, not that bytes are flowing.
func printTapeProgress(w io.Writer, p scanner.Progress) {
	operation := p.Extractor
	if operation == "" {
		operation = p.Stage
	}
	status := p.Event
	if p.Elapsed > 0 {
		status += " " + p.Elapsed.Round(time.Second).String()
	}
	if p.Detail != "" {
		status += ": " + p.Detail
	}
	fmt.Fprintf(w, "tape %s %d/%d: %s %q — %s\n",
		p.Stage, p.Done, p.Total, operation, p.Path, status)
}
