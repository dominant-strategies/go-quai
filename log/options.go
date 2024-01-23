package log

import (
	"fmt"
	"io"
	"os"
	"runtime"
	"strings"

	"github.com/natefinch/lumberjack"
	"github.com/sirupsen/logrus"
)

// type of log format
type LogFormat string

const (
	// skip is the number of stack frames to skip when reporting caller
	skip = 10

	JSONFormat LogFormat = "json"
	TextFormat LogFormat = "text"
)

// Options is a function type that can be used to configure the logger
type Options func(*Logger)

// WithLevel configures the log level. If level is not specified, default to InfoLevel
// If level is debug or trace, report caller is enabled
func WithLevel(level string) Options {
	return func(logger *Logger) {
		l, err := logrus.ParseLevel(level)
		if err != nil {
			logger.SetLevel(logrus.InfoLevel)
		} else {
			logger.SetLevel(l)
		}
		formatter := &logrus.TextFormatter{
			ForceColors:     true,
			PadLevelText:    true,
			FullTimestamp:   true,
			TimestampFormat: "01-02|15:04:05.000",
		}
		logger.SetFormatter(formatter)
	}
}

// WithNullLogger sets the logger to discard all output
func WithNullLogger() Options {
	return func(logger *Logger) {
		logger.SetOutput(io.Discard)
	}
}

// WithOutput configures the output destination
func WithOutput(outputs ...io.Writer) Options {
	return func(logger *Logger) {
		for _, output := range outputs {
			switch v := output.(type) {
			// if output is a lumberjack logger, add a hook to write to file
			case *lumberjack.Logger:
				hook := FileLogHook{
					Writer: v,
					Formatter: &logrus.TextFormatter{
						ForceColors:     true,
						PadLevelText:    true,
						FullTimestamp:   true,
						TimestampFormat: "01-02|15:04:05.000",
					},
				}
				logger.AddHook(&hook)
			default:
				logger.SetOutput(output)
			}
		}
	}
}

// ToStdOut returns an io.Writer to configure WithOutput() option to write to standard out.
func ToStdOut() io.Writer {
	return os.Stdout
}

// ToLogFile returns an io.Writer to configure WithOutput() option to write to a file and manage log rotation using lumberjack.
func ToLogFile(filename string) io.Writer {
	return &lumberjack.Logger{
		Filename:   filename,
		MaxSize:    defaultLogMaxSize,    // maximum file size before rotation, in MB
		MaxBackups: defaultLogMaxBackups, // maximum number of old log files to keep
		MaxAge:     defaultLogMaxAge,     // maximum number of days to retain old log files
		Compress:   defaultLogCompress,   // whether to compress the rotated log files using gzip
	}
}

// ToNull returns an io.Writer to configure WithOutput() option to discard all output.
func ToNull() io.Writer {
	return io.Discard
}

// logrus hook to write log to file
type FileLogHook struct {
	Writer    io.Writer
	Formatter logrus.Formatter
}

func (hook *FileLogHook) Fire(entry *logrus.Entry) error {
	line, err := hook.Formatter.Format(entry)
	if err != nil {
		return err
	}
	_, err = hook.Writer.Write(line)
	return err
}

func (hook *FileLogHook) Levels() []logrus.Level {
	return logrus.AllLevels
}

// callerPrettyfier formats the caller function, file path and line number
func callerPrettyfier(f *runtime.Frame) (string, string) {
	// Start with the current call stack frame.
	pcs := make([]uintptr, 10)
	n := runtime.Callers(skip, pcs)
	frames := runtime.CallersFrames(pcs[:n])

	// Traverse frames to find the first frame outside the logger package.
	for {
		frame, more := frames.Next()
		if !strings.Contains(frame.File, "/log/") && !strings.Contains(frame.File, "logrus") {
			// This frame is outside the logger package. Use it.
			return fmt.Sprintf("func: %s : ", formatFilePath(frame.Function, 1)), fmt.Sprintf(" src: %s:%d -", formatFilePath(frame.File, 2), frame.Line)
		}

		if !more {
			break
		}
	}

	// If we can't find a suitable frame, fall back to the provided frame.
	return fmt.Sprintf("func: %s : ", formatFilePath(f.Function, 1)), fmt.Sprintf(" src: %s:%d -", formatFilePath(f.File, 2), f.Line)
}

// formatFilePath receives a string representing a path and returns the last part of it
// The 2nd argument indicates the number of parts to return
func formatFilePath(path string, parts int) string {
	arr := strings.Split(path, "/")
	return strings.Join(arr[len(arr)-parts:], "/")
}
