package logger

import (
	"fmt"
	"os"
	"path/filepath"
	"time"
)

// LogLevel represents the severity of log messages
type LogLevel int

const (
	DEBUG LogLevel = iota
	INFO
	WARN
	ERROR
)

// Logger represents the application logger
type Logger struct {
	file     *os.File
	logLevel LogLevel
}

var logger *Logger

// Init initializes the logger with log file in user directory
func Init() error {
	// Get user home directory
	homeDir, err := os.UserHomeDir()
	if err != nil {
		return fmt.Errorf("failed to get user home directory: %v", err)
	}

	// Create chat-agent directory if it doesn't exist
	logDir := filepath.Join(homeDir, ".chat-agent")
	if err := os.MkdirAll(logDir, 0755); err != nil {
		return fmt.Errorf("failed to create log directory: %v", err)
	}

	// Create log file path
	logPath := filepath.Join(logDir, "chat-agent.log")

	// Open log file (append mode, create if doesn't exist)
	file, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		return fmt.Errorf("failed to open log file: %v", err)
	}

	logger = &Logger{
		file:     file,
		logLevel: DEBUG, // Log everything during debugging
	}

	// Write startup message
	logger.debug(INFO, "LOGGER", "chat-agent CLI logging initialized")
	logger.debug(INFO, "LOGGER", fmt.Sprintf("Log file: %s", logPath))

	return nil
}

// Close closes the log file
func Close() error {
	if logger != nil && logger.file != nil {
		logger.debug(INFO, "LOGGER", "chat-agent CLI logging shutdown")
		return logger.file.Close()
	}
	return nil
}

// Debug logs a debug message
func Debug(category, message string) {
	logger.debug(DEBUG, category, message)
}

// Info logs an info message
func Info(category, message string) {
	logger.debug(INFO, category, message)
}

// Warn logs a warning message
func Warn(category, message string) {
	logger.debug(WARN, category, message)
}

// Error logs an error message
func Error(category, message string) {
	logger.debug(ERROR, category, message)
}

// debug is the internal logging function (renamed to avoid conflict)
func (l *Logger) debug(level LogLevel, category, message string) {
	if l == nil {
		// Fallback to console if logger not initialized
		fmt.Printf("[%s] %s: %s\n", levelString(level), category, message)
		return
	}

	if level < l.logLevel {
		return
	}

	timestamp := time.Now().Format("2006-01-02 15:04:05.000")
	logLine := fmt.Sprintf("[%s] [%s] %s: %s\n", timestamp, levelString(level), category, message)

	l.file.WriteString(logLine)
	l.file.Sync() // Ensure immediate write to disk
}

// levelString converts LogLevel to string
func levelString(level LogLevel) string {
	switch level {
	case DEBUG:
		return "DEBUG"
	case INFO:
		return "INFO"
	case WARN:
		return "WARN"
	case ERROR:
		return "ERROR"
	default:
		return "UNKNOWN"
	}
}

// GetLogPath returns the current log file path
func GetLogPath() string {
	if logger == nil {
		return ""
	}
	return logger.file.Name()
}
