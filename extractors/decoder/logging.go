package decoder

import (
	"context"
	"log/slog"
	"sync/atomic"
)

var packageLogger atomic.Pointer[slog.Logger]

func init() {
	slog.SetLogLoggerLevel(slog.LevelDebug)
	packageLogger.Store(slog.Default())
}

func logger() *slog.Logger {
	if l := packageLogger.Load(); l != nil {
		return l
	}
	return slog.Default()
}

// SetLogger overrides the logger used by this package.
func SetLogger(l *slog.Logger) {
	if l == nil {
		l = slog.Default()
	}
	packageLogger.Store(l)
	logDebug("decoder logger configured")
}

func logDebug(msg string, attrs ...any) {
	l := logger()
	if l.Enabled(context.Background(), slog.LevelDebug) {
		l.Debug(msg, attrs...)
	}
}

func logError(msg string, attrs ...any) {
	l := logger()
	if l.Enabled(context.Background(), slog.LevelError) {
		l.Error(msg, attrs...)
	}
}
