// Command brambledns is the single entrypoint binary. It starts the DNS engine
// goroutine and the GUI goroutine under one shared shutdown context.
//
// main stays deliberately thin: it delegates to internal/cli so the real
// startup/wiring logic is testable without an OS process. Engine and GUI wiring
// arrive in Phase 1 (see docs/roadmap.md).
package main

import (
	"os"

	"github.com/mallardduck/BrambleDNS/internal/cli"
)

func main() {
	os.Exit(cli.Run(os.Args[1:]))
}
