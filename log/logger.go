package log

import (
	"fmt"
	"path"
	"runtime"
	"strings"

	"github.com/natefinch/lumberjack"
	"github.com/sirupsen/logrus"
	"gopkg.in/urfave/cli.v1"
)

type Logger = *logrus.Logger

var Log Logger = logrus.New()

func init() {
	Log.Formatter = &logrus.TextFormatter{
		ForceColors:      true,
		PadLevelText:     true,
		FullTimestamp:    true,
		TimestampFormat:  "01-02|15:04:05",
		CallerPrettyfier: callerPrettyfier,
	}
}

func ConfigureLogger(ctx *cli.Context) {
	logLevel := logrus.Level(ctx.GlobalInt("verbosity"))
	Log.SetLevel(logLevel)

	if logLevel >= logrus.DebugLevel {
		Log.SetReportCaller(true)
	}

	log_filename := "nodelogs/"
	regionNum := ctx.GlobalString("region")

	if ctx.GlobalIsSet("zone") {
		zoneNum := ctx.GlobalString("zone")
		log_filename += "zone-" + regionNum + "-" + zoneNum
	} else if ctx.GlobalIsSet("region") {
		log_filename += "region-" + regionNum
	} else {
		log_filename += "prime"
	}
	log_filename += ".log"

	Log.SetOutput(&lumberjack.Logger{
		Filename:   log_filename,
		MaxSize:    5, // megabytes
		MaxBackups: 3,
		MaxAge:     28, //days
	})
}

func SetLevelInt(level int) {
	Log.SetLevel(logrus.Level(level))
}

func SetLevelString(level string) {
	logLevel, err := logrus.ParseLevel(level)
	if err != nil {
		Log.Error("Invalid log level: ", level)
		return
	}
	Log.SetLevel(logLevel)
}

func New(out_path string) Logger {
	logger := logrus.New()
	logger.SetOutput(&lumberjack.Logger{
		Filename:   out_path,
		MaxSize:    500, // megabytes
		MaxBackups: 3,
		MaxAge:     28, //days
	})
	return logger
}

func Trace(msg string, args ...interface{}) {
	Log.Trace(constructLogMessage(msg, args...))
}

func Debug(msg string, args ...interface{}) {
	Log.Debug(constructLogMessage(msg, args...))
}

func Info(msg string, args ...interface{}) {
	Log.Info(constructLogMessage(msg, args...))
}

func Warn(msg string, args ...interface{}) {
	Log.Warn(constructLogMessage(msg, args...))
}

func Error(msg string, args ...interface{}) {
	Log.Error(constructLogMessage(msg, args...))
}

func Fatal(msg string, args ...interface{}) {
	Log.Fatal(constructLogMessage(msg, args...))
}

func Panic(msg string, args ...interface{}) {
	Log.Panic(constructLogMessage(msg, args...))
}

func constructLogMessage(msg string, fields ...interface{}) string {
	var pairs []string

	for i := 0; i < len(fields); i += 2 {
		key := fields[i]
		value := fields[i+1]
		pairs = append(pairs, fmt.Sprintf("%v=%v", key, value))
	}

	return fmt.Sprintf("%-40s %s", msg, strings.Join(pairs, " "))
}

func callerPrettyfier(f *runtime.Frame) (string, string) {
	filename := path.Base(f.File)
	dir := path.Base(path.Dir(f.File))

	filepath := path.Join(dir, filename)
	return "", fmt.Sprintf("%s:%d", filepath, f.Line)
}
