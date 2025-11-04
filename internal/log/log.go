package log

import (
	"fmt"
	"log/slog"
)

func Debug(format string, a ...any) {
	slog.Debug(fmt.Sprintf(format, a...))
}

func Info(format string, a ...any) {
	slog.Info(fmt.Sprintf(format, a...))
}

func Warn(format string, a ...any) {
	slog.Warn(fmt.Sprintf(format, a...))
}

func Error(format string, a ...any) {
	slog.Error(fmt.Sprintf(format, a...))
}
