// Custom logger with level
package logger

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"time"
)

// LogLevel represents the severity of a log message.
type LogLevel int

const (
	FATAL LogLevel = iota // 0
	ERROR                 // 1
	WARN                  // 2
	INFO                  // 3
	DEBUG                 // 4
)

// String implements the fmt.Stringer interface for LogLevel.
func (l LogLevel) String() string {
	switch l {
	case FATAL:
		return "FATAL"
	case ERROR:
		return "ERROR"
	case WARN:
		return "WARN"
	case INFO:
		return "INFO"
	case DEBUG:
		return "DEBUG"
	default:
		return "UNKNOWN"
	}
}

// Default log level we start with for filtering
var (
	currentLogLevel LogLevel = WARN
	projectRoot     string
)

func init() {
	// Find project root once at startup by looking for go.mod.
	dir, err := os.Getwd()
	if err != nil {
		// If we can't get the working directory, we can't find the root.
		// We'll fall back to the full path in the log message.
		return
	}

	// Walk up the directory tree from the current working directory.
	for {
		// Check if go.mod exists in the current directory.
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			projectRoot = dir
			return // Found it!
		}

		// If we've reached the filesystem root, stop.
		parent := filepath.Dir(dir)
		if parent == dir {
			break
		}
		dir = parent
	}
}

// SetLogLevel allows external packages to change the log level filter.
func SetLogLevel(level LogLevel) {
	currentLogLevel = level
}

// logWithLevel is the internal logging function that handles formatting.
func logWithLevel(level LogLevel, format string, v ...any) {
	if level <= currentLogLevel {
		// Create the final log message string.

		// Create a timestamp with an explicit timezone.
		// https://pkg.go.dev/time#pkg-constants
		// RFC822Z     = "02 Jan 06 15:04 -0700"
		// RFC1123Z    = "Mon, 02 Jan 2006 15:04:05 -0700"
		// RFC3339     = "2006-01-02T15:04:05Z07:00"
		timestamp := time.Now().Format(time.RFC822Z)
		fileTrace := ""

		// If log level is set to `DEBUG` or  incoming level is `FATAL`.
		// Locate file and line number that called it for easier debugging
		if currentLogLevel >= DEBUG || level == FATAL {
			// Look at stack frames and set 'skip' value to 2 because for this
			// applcation the caller of `logWithLevel` is at that stack frame level.
			// https://pkg.go.dev/runtime#Caller
			_, file, line, ok := runtime.Caller(2)
			if !ok {
				file = "???"
				line = 0
			}
			// Make path relative to the project root (where go.mod is) for cleaner output.
			if projectRoot != "" {
				if rel, err := filepath.Rel(projectRoot, file); err == nil {
					file = rel
				}
			}
			fileTrace = fmt.Sprintf("%s:%d -", file, line)
		}

		msg := fmt.Sprintf(
			"%s [%s] %s %s",
			timestamp,
			level,
			fileTrace,
			fmt.Sprintf(format, v...),
		)
		log.Println(msg)
	}
}

// Info logs a message at the INFO level.
func Info(format string, v ...any) {
	logWithLevel(INFO, format, v...)
}

// Warn logs a message at the WARN level.
func Warn(format string, v ...any) {
	logWithLevel(WARN, format, v...)
}

// Error logs a message at the ERROR level.
func Error(format string, v ...any) {
	logWithLevel(ERROR, format, v...)
}

// Fatal logs a message at the FATAL level and then exit
func Fatal(format string, v ...any) {
	logWithLevel(FATAL, format, v...)
	os.Exit(1)
}

// Debug logs a message at the DEBUG level.
func Debug(format string, v ...any) {
	logWithLevel(DEBUG, format, v...)
}
