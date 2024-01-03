package log

import (
	"io"
	"os"

	"github.com/natefinch/lumberjack"
	"github.com/sirupsen/logrus"
)

const (
	// default log level
	defaultLogLevel = logrus.InfoLevel

	// log file name
	globalLogFileName = "global.log"
	// default log directory
	logDir = "nodelogs"
	// default log file params
	defaultLogMaxSize    = 100  // maximum file size before rotation, in MB
	defaultLogMaxBackups = 3    // maximum number of old log files to keep
	defaultLogMaxAge     = 28   // maximum number of days to retain old log files
	defaultLogCompress   = true // whether to compress the rotated log files using gzip
)

var (
	// logger instance used by the application
	logger *logrus.Logger

	// TODO: consider refactoring to dinamically read the app name (i.e. "go-quai") ?
	// default logfile path
	defaultLogFilePath = "./" + logDir + "/" + globalLogFileName
)

func init() {
	logger = createStandardLogger(defaultLogFilePath, defaultLogLevel.String(), true)
}

func SetGlobalLogger(logFilename string, logLevel string) {
	// Change global logger's output
	output := &lumberjack.Logger{
		Filename:   logFilename,
		MaxSize:    500, // megabytes
		MaxBackups: 3,
		MaxAge:     28, //days
	}
	logger.SetOutput(io.MultiWriter(output, os.Stdout))

	// Change global logger's level
	level, err := logrus.ParseLevel(logLevel)
	if err != nil {
		level = defaultLogLevel
	}
	logger.SetLevel(level)
}

func NewLogger(logFilename string, logLevel string) *logrus.Logger {
	if logFilename == "" {
		logFilename = defaultLogFilePath
	}
	shardLogger := createStandardLogger(logFilename, logLevel, false)
	shardLogger.WithFields(logrus.Fields{
		"path":  logFilename,
		"level": logLevel,
	}).Info("Shard logger started")
	return shardLogger
}

func createStandardLogger(logFilename string, logLevel string, stdOut bool) *logrus.Logger {
	logger := logrus.New()
	output := &lumberjack.Logger{
		Filename:   logFilename,
		MaxSize:    500, // megabytes
		MaxBackups: 3,
		MaxAge:     28, //days
	}

	if stdOut {
		logger.SetOutput(io.MultiWriter(output, os.Stdout))
	} else {
		logger.SetOutput(output)
	}

	logger.SetFormatter(&logrus.TextFormatter{
		ForceColors:     true,
		PadLevelText:    true,
		FullTimestamp:   true,
		TimestampFormat: "01-02|15:04:05.000",
	})
	level, err := logrus.ParseLevel(logLevel)
	if err != nil {
		level = defaultLogLevel
	}
	logger.SetLevel(level)
	return logger
}

func WithField(key string, val interface{}) *logrus.Entry {
	return logger.WithField(key, val)
}

func WithFields(fields logrus.Fields) *logrus.Entry {
	return logger.WithFields(fields)
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
