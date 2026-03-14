package logging

import (
	"fmt"
	"io"
	"os"
	"strings"
	"sync"
)

type Level int

const (
	LevelDebug Level = iota
	LevelInfo
	LevelWarn
	LevelError
)

var levelNames = map[string]Level{
	"debug": LevelDebug,
	"info":  LevelInfo,
	"warn":  LevelWarn,
	"error": LevelError,
}

type Logger struct {
	mu     sync.Mutex
	level  Level
	output io.Writer
}

var defaultLogger = &Logger{
	level:  LevelInfo,
	output: os.Stdout,
}

func ParseLevel(s string) Level {
	if l, ok := levelNames[strings.ToLower(s)]; ok {
		return l
	}
	return LevelInfo
}

func SetLevel(level string) {
	defaultLogger.mu.Lock()
	defer defaultLogger.mu.Unlock()
	defaultLogger.level = ParseLevel(level)
}

func SetOutput(w io.Writer) {
	defaultLogger.mu.Lock()
	defer defaultLogger.mu.Unlock()
	defaultLogger.output = w
}

func (l *Logger) log(level Level, format string, args ...interface{}) {
	l.mu.Lock()
	defer l.mu.Unlock()

	if level < l.level {
		return
	}

	msg := fmt.Sprintf(format, args...)
	prefix := levelPrefixes[level]
	fmt.Fprintf(l.output, "[%s] %s\n", prefix, msg)
}

var levelPrefixes = map[Level]string{
	LevelDebug: "DEBUG",
	LevelInfo:  "INFO",
	LevelWarn:  "WARN",
	LevelError: "ERROR",
}

func Debug(format string, args ...interface{}) {
	defaultLogger.log(LevelDebug, format, args...)
}

func Info(format string, args ...interface{}) {
	defaultLogger.log(LevelInfo, format, args...)
}

func Warn(format string, args ...interface{}) {
	defaultLogger.log(LevelWarn, format, args...)
}

func Error(format string, args ...interface{}) {
	defaultLogger.log(LevelError, format, args...)
}
