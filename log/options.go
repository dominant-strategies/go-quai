package log

import (
	"fmt"
	"io"
	"runtime"
	"strings"
	"time"

	"github.com/sirupsen/logrus"
)

const (
	// skip is the number of stack frames to skip when reporting caller
	skip = 10
)

// Options is a function type that can be used to configure the logger
type Options func(*LogWrapper)

// WithLevel configures the log level. If level is not specified, default to InfoLevel
// If level is debug or trace, report caller is enabled
func WithLevel(level string) Options {
	return func(lw *LogWrapper) {
		l, err := logrus.ParseLevel(level)
		if err != nil {
			lw.entry.Logger.SetLevel(logrus.InfoLevel)
		} else {
			lw.entry.Logger.SetLevel(l)
		}
		formatter := &logrus.TextFormatter{
			FullTimestamp:          false,
			DisableLevelTruncation: true,
			ForceColors:            true,
			PadLevelText:           false,
			DisableColors:          false,
		}

		if l == logrus.DebugLevel || l == logrus.TraceLevel {
			formatter = &logrus.TextFormatter{
				TimestampFormat: time.RFC3339,
				FullTimestamp:   true,
				CallerPrettyfier: func(f *runtime.Frame) (string, string) {
					if pc, file, line, ok := runtime.Caller(skip); ok {
						fName := runtime.FuncForPC(pc).Name()
						return fmt.Sprintf("func: %s : ", formatFilePath(fName, 1)), fmt.Sprintf(" src: %s:%d -", formatFilePath(file, 2), line)
					}
					return fmt.Sprintf("func: %s : ", formatFilePath(f.Function, 1)), fmt.Sprintf(" src: %s:%d -", formatFilePath(f.File, 2), f.Line)
				},
			}
			lw.entry.Logger.SetReportCaller(true)
		}
		lw.entry.Logger.SetFormatter(formatter)
	}
}

// WithOutput configures the output destination
func WithOutput(output io.Writer) Options {
	return func(lw *LogWrapper) {
		lw.entry.Logger.SetOutput(output)
	}
}

// WithFormatter configures the log formatter
func WithFormatter(formatter logrus.Formatter) Options {
	return func(lw *LogWrapper) {
		lw.entry.Logger.SetFormatter(formatter)
	}
}

// WithReportCaller configures the log to report caller
func WithReportCaller(reportCaller bool) Options {
	return func(lw *LogWrapper) {
		lw.entry.Logger.SetReportCaller(reportCaller)
	}
}

// WithNullLogger sets the logger to discard all output
func WithNullLogger() Options {
	return func(lw *LogWrapper) {
		lw.entry.Logger.SetOutput(io.Discard)
	}
}

// formatFilePath receives a string representing a path and returns the last part of it
// The 2nd argument indicates the number of parts to return
func formatFilePath(path string, parts int) string {
	arr := strings.Split(path, "/")
	return strings.Join(arr[len(arr)-parts:], "/")
}
