package logger

import (
	"fmt"
	"log/slog"
	"os"
)

// Logger wraps slog.Logger and implements investgo.Logger interface.
type Logger struct {
	*slog.Logger
}

func New(level string) *Logger {
	var lvl slog.Level
	switch level {
	case "debug":
		lvl = slog.LevelDebug
	case "warn":
		lvl = slog.LevelWarn
	case "error":
		lvl = slog.LevelError
	default:
		lvl = slog.LevelInfo
	}

	handler := slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{Level: lvl})
	return &Logger{Logger: slog.New(handler)}
}

// investgo.Logger interface methods

func (l *Logger) Infof(format string, args ...any) {
	l.Info(fmt.Sprintf(format, args...))
}

func (l *Logger) Errorf(format string, args ...any) {
	l.Error(fmt.Sprintf(format, args...))
}

func (l *Logger) Fatalf(format string, args ...any) {
	l.Error(fmt.Sprintf(format, args...))
	os.Exit(1)
}
