package logger

import (
	"log/slog"
	"os"

	"learn101/internal/config"
)

// New builds the application logger.
//
// Production gets JSON on stdout with UTC timestamps, because that is what log
// aggregators parse and what makes correlating across hosts and timezones
// possible. Development gets human-readable text with local timestamps, because
// that is what you actually want to read in a terminal.
//
// The caller decides what to do with the result. run() passes it to
// slog.SetDefault so packages that never receive a logger explicitly still emit
// in the configured format, but tests can just use the returned value.
func New(cfg *config.Config) *slog.Logger {
	production := cfg.IsProduction()

	opts := &slog.HandlerOptions{
		Level: cfg.LogLevel,

		// runtime.Caller on every line is not free, and it is only useful while
		// debugging, so gate it on the level rather than always paying for it.
		// Build with -trimpath or the recorded paths will be absolute.
		AddSource: cfg.LogLevel <= slog.LevelDebug,

		ReplaceAttr: func(_ []string, a slog.Attr) slog.Attr {
			if a.Key == slog.TimeKey {
				t := a.Value.Time()
				if production {
					return slog.Time(slog.TimeKey, t.UTC())
				}
				return slog.Time(slog.TimeKey, t.Local())
			}
			return a
		},
	}

	if production {
		return slog.New(slog.NewJSONHandler(os.Stdout, opts))
	}
	return slog.New(slog.NewTextHandler(os.Stdout, opts))
}
