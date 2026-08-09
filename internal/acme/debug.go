package acme

import (
	"context"
	"fmt"
	"log/slog"
	"os"

	legolog "github.com/go-acme/lego/v4/log"
)

// enableDebugLogging makes BRAMBLEGATE_LOG_LEVEL=debug alone sufficient to see
// everything lego does — no separate LEGO_DEBUG_* env vars to discover and set.
// It only takes effect when log is at Debug level, so normal (info) operation is
// unchanged: lego keeps its own default stderr logger, which is already quiet.
func enableDebugLogging(log *slog.Logger) {
	if !log.Enabled(context.Background(), slog.LevelDebug) {
		return
	}
	legolog.Logger = &slogAdapter{log: log.With("component", "lego")}
	// Read directly by lego's DNS provider constructors
	// (providers/dns/internal/clientdebug) to dump the raw DNS-01 provider
	// API request/response — there's no programmatic hook for this, only env.
	_ = os.Setenv("LEGO_DEBUG_DNS_API_HTTP_CLIENT", "true")
}

// slogAdapter implements lego's log.StdLogger by forwarding to a slog.Logger at
// Debug level. Only installed when we're already at Debug, so no level mapping
// is needed for Print/Printf; Fatal still aborts the process like lego's default.
type slogAdapter struct{ log *slog.Logger }

func (a *slogAdapter) Print(args ...any)                 { a.log.Debug(fmt.Sprint(args...)) }
func (a *slogAdapter) Println(args ...any)               { a.log.Debug(fmt.Sprint(args...)) }
func (a *slogAdapter) Printf(format string, args ...any) { a.log.Debug(fmt.Sprintf(format, args...)) }
func (a *slogAdapter) Fatal(args ...any)                 { a.log.Error(fmt.Sprint(args...)); os.Exit(1) }
func (a *slogAdapter) Fatalln(args ...any)               { a.log.Error(fmt.Sprint(args...)); os.Exit(1) }
func (a *slogAdapter) Fatalf(format string, args ...any) {
	a.log.Error(fmt.Sprintf(format, args...))
	os.Exit(1)
}
