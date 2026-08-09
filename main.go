// Command bramblegate is the single entrypoint binary. It starts the DNS engine
// goroutine and the GUI goroutine under one shared shutdown context.
//
// main stays deliberately thin: it delegates to internal/cli so the real
// startup/wiring logic is testable without an OS process. Engine and GUI wiring
// arrive in Phase 1 (see docs/roadmap.md).
package main

import (
	"os"

	"github.com/mallardduck/BrambleGate/internal/cli"
	"github.com/mallardduck/BrambleGate/internal/gui/ui"
	"github.com/mallardduck/BrambleGate/internal/version"
)

func main() {
	ui.AppVersion = version.String()
	os.Exit(cli.Run(os.Args[1:]))
}
