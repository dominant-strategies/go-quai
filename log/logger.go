package log

import "github.com/sirupsen/logrus"

var logger Logger

func init() {
	entry := logrus.NewEntry(logrus.StandardLogger())
	logger = &LogWrapper{
		entry: entry,
	}
	ConfigureLogger(WithLevel("info"))
}

func ConfigureLogger(opts ...Options) {
	for _, opt := range opts {
		opt(logger.(*LogWrapper))
	}
}

func Trace(keyvals ...interface{}) {
	logger.Trace(keyvals...)
}

func Tracef(msg string, args ...interface{}) {
	logger.Tracef(msg, args...)
}

func Debug(keyvals ...interface{}) {
	logger.Debug(keyvals...)
}

func Debugf(msg string, args ...interface{}) {
	logger.Debugf(msg, args...)
}

func Info(keyvals ...interface{}) {
	logger.Info(keyvals...)
}

func Infof(msg string, args ...interface{}) {
	logger.Infof(msg, args...)
}

func Warn(keyvals ...interface{}) {
	logger.Warn(keyvals...)
}

func Warnf(msg string, args ...interface{}) {
	logger.Warnf(msg, args...)
}

func Error(keyvals ...interface{}) {
	logger.Error(keyvals...)
}

func Errorf(msg string, args ...interface{}) {
	logger.Errorf(msg, args...)
}

func Fatal(keyvals ...interface{}) {
	logger.Fatal(keyvals...)
}

func Fatalf(msg string, args ...interface{}) {
	logger.Fatalf(msg, args...)
}

func WithField(key string, val interface{}) Logger {
	return logger.WithField(key, val)
}
