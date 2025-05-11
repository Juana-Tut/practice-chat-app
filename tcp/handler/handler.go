// Package server implements a TCP chat server
package server

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/2016114132/chat-app-final-project/shared"
)

// clients is a concurrent-safe map of all connected clients and their nicknames.
// The key is the TCP connection, and the value is the client's name.
var (
	clients       = make(map[net.Conn]string)
	clientsMutex  sync.RWMutex // Used to prevent race conditions when accessing the clients map
	timeoutPeriod = 60 * time.Second // Timeout period for detecting dead connections
)

// handleConnection is called whenever a new client connects.
// It reads the client's nickname, listens for messages, and broadcasts them to other clients.
func handleConnection(ctx context.Context, conn net.Conn) {
	// Ensure connection is closed when done
	defer func() {
		conn.Close()
		
		// Record disconnect in metrics
		clientsMutex.Lock()
		if _, exists := clients[conn]; exists {
			delete(clients, conn)
			serverMetrics.ClientDisconnected(false) // Normal disconnect
		} else {
			// This shouldn't happen but just in case
			serverMetrics.ClientDisconnected(true) // Abnormal disconnect
		}
		clientsMutex.Unlock()
	}()
	
	// Set initial read deadline for getting the client's name
	conn.SetReadDeadline(time.Now().Add(timeoutPeriod))
	
	// Get the client's network address as a default identifier
	addr := conn.RemoteAddr().String()
	
	// Default name is the client's address
	name := addr
	
	// Create a buffered reader to read incoming data
	reader := bufio.NewReader(conn)
	
	// Wait for the client to send their nickname
	success := false
	for attempts := 0; attempts < 3; attempts++ {
		// Read the first line sent by the client
		line, err := reader.ReadString('\n')
		if err != nil {
			// If client drops before naming, log it and exit
			fmt.Printf("Client %s disconnected before sending name: %v\n", addr, err)
			serverMetrics.ClientDisconnected(true) // Abnormal disconnect
			return
		}
		
		serverMetrics.MessageReceived(len(line))
		
		// If the line starts with "/name"
		if shared.IsCommand(line, "name") {
			// Extract and clean the nickname
			name = shared.FormatName(strings.TrimPrefix(line, "/name "))
			success = true
			break
		} else {
			// Send an error message if the client didn't send a proper name command
			message := "Please use '/name YourNickname' to join the chat.\n"
			conn.Write([]byte(message))
			serverMetrics.MessageSent()
		}
	}
	
	// If the client failed to provide a valid name after 3 attempts, close the connection
	if !success {
		fmt.Printf("Client %s failed to provide a valid name after 3 attempts.\n", addr)
		conn.Write([]byte("Failed to provide a valid name. Disconnecting.\n"))
		serverMetrics.MessageSent()
		return
	}
	
	// Store the new client connection and nickname
	clientsMutex.Lock()
	clients[conn] = name
	activeUsers := len(clients)
	clientsMutex.Unlock()
	
	// Log the connection to the server console
	fmt.Printf("[+] %s connected from %s (Active users: %d)\n", name, addr, activeUsers)
	
	// Notify all clients that a new user has joined
	broadcastMessage(conn, fmt.Sprintf("Server: %s has joined the chat.", name))
	
	// Send a welcome message to the new client
	welcomeMsg := fmt.Sprintf("Welcome to the chat, %s! There are %d other users online.", name, activeUsers-1)
	conn.Write([]byte(welcomeMsg + "\n"))
	serverMetrics.MessageSent()
	
	// Create a done channel to signal when client disconnects
	done := make(chan struct{})
	
	// Start a goroutine for heartbeat checks
	go func() {
		heartbeatTicker := time.NewTicker(15 * time.Second)
		defer heartbeatTicker.Stop()
		
		for {
			select {
			case <-ctx.Done():
				// Server is shutting down
				return
			case <-done:
				// Client has disconnected
				return
			case <-heartbeatTicker.C:
				// Send a ping and expect a pong
				clientsMutex.RLock()
				if _, exists := clients[conn]; !exists {
					clientsMutex.RUnlock()
					return // Client already disconnected
				}
				clientsMutex.RUnlock()
				
				// Set a temporary write deadline
				conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
				_, err := conn.Write([]byte("/ping\n"))
				// Reset the write deadline
				conn.SetWriteDeadline(time.Time{})
				
				if err != nil {
					fmt.Printf("[-] Heartbeat failed for %s: %v\n", name, err)
					closeConnection(conn, name, true)
					return
				}
				
				serverMetrics.MessageSent()
			}
		}
	}()
	
	// Listen for messages from this client
	for {
		select {
		case <-ctx.Done():
			// Server is shutting down
			welcomeMsg := "Server is shutting down. Thank you for chatting!"
			conn.Write([]byte(welcomeMsg + "\n"))
			serverMetrics.MessageSent()
			return
		default:
			// Set read deadline to detect dead connections
			conn.SetReadDeadline(time.Now().Add(timeoutPeriod))
			
			// Read full message until newline
			message, err := reader.ReadString('\n')
			if err != nil {
				// If an error occurs (likely disconnect), log it
				closeConnection(conn, name, true)
				close(done) // Signal the heartbeat goroutine to stop
				return
			}
			
			// Reset the read deadline
			conn.SetReadDeadline(time.Time{})
			
			// Track message size
			serverMetrics.MessageReceived(len(message))
			
			// Check if this is a ping response
			if strings.TrimSpace(message) == "/pong" {
				continue // Skip further processing for pong responses
			}
			
			// Check if this is a ping, respond with pong
			if strings.TrimSpace(message) == "/ping" {
				conn.Write([]byte("/pong\n"))
				serverMetrics.MessageSent()
				continue
			}

			// Parse the message for timestamp to calculate latency
			parts := strings.SplitN(message, "|", 2)
			if len(parts) == 2 {
				// Parse the timestamp and calculate latency
				sentTime, err := time.Parse(time.RFC3339Nano, parts[0])
				if err == nil {
					latency := time.Since(sentTime).Nanoseconds()
					serverMetrics.AddLatency(latency)
				}
				message = parts[1] // Keep just the message part
			}
			
			// Format the message and broadcast it
			broadcastMessage(conn, shared.FormatMessage(name, message))
		}
	}
}

// closeConnection handles client disconnection cleanly
func closeConnection(conn net.Conn, name string, abnormal bool) {
	clientsMutex.Lock()
	defer clientsMutex.Unlock()
	
	if _, exists := clients[conn]; exists {
		fmt.Printf("[-] %s disconnected\n", name)
		delete(clients, conn)
		serverMetrics.ClientDisconnected(abnormal)
		
		// Notify other clients about the disconnection
		for client, clientName := range clients {
			if client != conn {
				client.Write([]byte(fmt.Sprintf("Server: %s has left the chat.\n", name)))
				serverMetrics.MessageSent()
			}
		}
	}
}

// broadcastMessage sends a given message to all connected clients except the sender.
func broadcastMessage(sender net.Conn, message string) {
	clientsMutex.RLock()
	defer clientsMutex.RUnlock()
	
	for client := range clients {
		if client != sender {
			// Set a temporary write deadline
			client.SetWriteDeadline(time.Now().Add(5 * time.Second))
			
			// Send the message
			_, err := client.Write([]byte(message + "\n"))
			
			// Reset the write deadline
			client.SetWriteDeadline(time.Time{})
			
			// Log any issues
			if err != nil {
				serverMetrics.RecordError()
				fmt.Println("Error broadcasting:", err)
			} else {
				serverMetrics.MessageSent()
			}
		}
	}
}

// GetActiveUserCount returns the number of currently connected clients
func GetActiveUserCount() int {
	clientsMutex.RLock()
	defer clientsMutex.RUnlock()
	return len(clients)
}