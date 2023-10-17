package log

import (
	"github.com/sirupsen/logrus"
)

type Logger interface {
	Trace(keyvals ...interface{})
	Tracef(msg string, args ...interface{})
	Debug(keyvals ...interface{})
	Debugf(msg string, args ...interface{})
	Info(keyvals ...interface{})
	Infof(msg string, args ...interface{})
	Warn(keyvals ...interface{})
	Warnf(msg string, args ...interface{})
	Error(keyvals ...interface{})
	Errorf(msg string, args ...interface{})
	Fatal(keyvals ...interface{})
	Fatalf(msg string, args ...interface{})

	WithField(key string, val interface{}) Logger
}

type LogWrapper struct {
	entry *logrus.Entry
}

// Interface assertion
var _ Logger = (*LogWrapper)(nil)

func (l *LogWrapper) Trace(keyvals ...interface{}) {
	l.entry.Trace(keyvals...)
}

func (l *LogWrapper) Tracef(msg string, args ...interface{}) {
	l.entry.Tracef(msg, args...)
}

func (l *LogWrapper) Debug(keyvals ...interface{}) {
	l.entry.Debug(keyvals...)
}

func (l *LogWrapper) Debugf(msg string, args ...interface{}) {
	l.entry.Debugf(msg, args...)
}

func (l *LogWrapper) Info(keyvals ...interface{}) {
	l.entry.Info(keyvals...)
}

func (l *LogWrapper) Infof(msg string, args ...interface{}) {
	l.entry.Infof(msg, args...)
}

func (l *LogWrapper) Warn(keyvals ...interface{}) {
	l.entry.Warn(keyvals...)
}

func (l *LogWrapper) Warnf(msg string, args ...interface{}) {
	l.entry.Warnf(msg, args...)
}

func (l *LogWrapper) Error(keyvals ...interface{}) {
	l.entry.Error(keyvals...)
}

func (l *LogWrapper) Errorf(msg string, args ...interface{}) {
	l.entry.Errorf(msg, args...)
}

func (l *LogWrapper) Fatal(keyvals ...interface{}) {
	l.entry.Fatal(keyvals...)
}

func (l *LogWrapper) Fatalf(msg string, args ...interface{}) {
	l.entry.Fatalf(msg, args...)
}

func (l *LogWrapper) WithField(key string, val interface{}) Logger {
	return &LogWrapper{entry: l.entry.WithField(key, val)}
}
