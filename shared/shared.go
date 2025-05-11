// Package shared contains utilities shared between client and server
package shared

import (
	"fmt"
	"strings"
	"time"
)

// Common command prefixes
const (
	CmdName  = "/name"
	CmdQuit  = "/quit"
	CmdPing  = "/ping"
	CmdPong  = "/pong"
	CmdUsers = "/users"
	CmdHelp  = "/help"
)

// IsCommand checks if a message is a specific command
func IsCommand(msg, cmd string) bool {
	return strings.HasPrefix(strings.TrimSpace(msg), "/"+cmd)
}

// FormatName cleans up and formats a user nickname
func FormatName(name string) string {
	// Trim whitespace and newlines
	name = strings.TrimSpace(name)
	
	// Replace problematic characters
	name = strings.Map(func(r rune) rune {
		if strings.ContainsRune(" \t\n\r", r) {
			return '_'
		}
		return r
	}, name)
	
	// Set a reasonable length limit
	if len(name) > 20 {
		name = name[0:20]
	}
	
	// Set a default name if empty
	if name == "" {
		name = fmt.Sprintf("User%d", time.Now().UnixNano()%10000)
	}
	
	return name
}

// SanitizeInput removes unwanted characters from user input
func SanitizeInput(input string) string {
	// Trim whitespace and newlines
	return strings.TrimSpace(input)
}

// FormatMessage creates a formatted message from a username and content
func FormatMessage(username, message string) string {
	timestamp := time.Now().Format("15:04:05")
	return fmt.Sprintf("[%s] %s: %s", timestamp, username, strings.TrimSpace(message))
}

// GetHelpText returns the help message for available commands
func GetHelpText() string {
	help := "Available commands:\n"
	help += "  /help       - Show this help message\n"
	help += "  /name NAME  - Change your nickname\n"
	help += "  /users      - List connected users\n"
	help += "  /quit       - Disconnect from the chat\n"
	return help
}