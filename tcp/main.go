// Entry point for the TCP chat server
package main

// Import the custom TCP server package
import "github.com/Juana-Tut/practice-chat-app/tcp/server"

// main is the entry point of the application.
// It starts the server and begins listening on port 4000.
func main() {
	// Start the TCP server on localhost port 4000
	server.Start(":4000")
}
