// Package main implements a UDP chat client
package main

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"strings"
	"syscall"
	"time"

	"github.com/Juana-Tut/practice-chat-app/udp/metrics"
	"github.com/Juana-Tut/practice-chat-app/shared"
)

const (
	serverAddress   = "localhost:4000"
	reconnectDelay  = 3 * time.Second
	reconnectAttempts = 5
	maxMessageSize  = 1024 // UDP datagram size limit
	heartbeatInterval = 5 * time.Second
	readTimeout     = 15 * time.Second
)

func main() {
	// Set up cancellable context for clean shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	
	// Initialize metrics
	clientMetrics := metrics.NewClientMetrics()
	
	// Set up signal handler for graceful shutdown
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		fmt.Println("\nDisconnecting from chat...")
		fmt.Println(clientMetrics.Report())
		cancel()
		os.Exit(0)
	}()
	
	// Prompt for nickname before attempting connection
	reader := bufio.NewReader(os.Stdin)
	fmt.Print("Enter your nickname: ")
	nickname, _ := reader.ReadString('\n')
	nickname = shared.FormatName(nickname)
	
	// Create UDP address
	serverAddr, err := net.ResolveUDPAddr("udp", serverAddress)
	if err != nil {
		fmt.Printf("Error resolving server address: %v\n", err)
		return
	}
	
	// Connect to server with retry logic
	conn, err := connectWithRetry(ctx, serverAddr, reconnectAttempts, clientMetrics)
	if err != nil {
		fmt.Printf("Failed to connect after %d attempts: %v\n", reconnectAttempts, err)
		return
	}
	defer conn.Close()
	
	// Create a session ID to identify this client to the server
	sessionID := fmt.Sprintf("%d", time.Now().UnixNano())
	
	// Send nickname to server
	sendRegistration(conn, serverAddr, nickname, sessionID, clientMetrics)
	
	fmt.Println("Connected to chat server as", nickname)
	
	// Create channels for coordinating goroutines
	errChan := make(chan error, 2)
	
	// Create a message buffer for incoming messages
	msgChan := make(chan string, 50)
	
	// Start goroutine to read from server
	go readFromServer(ctx, conn, clientMetrics, errChan, msgChan)
	
	// Start goroutine to send messages to server
	go writeToServer(ctx, conn, serverAddr, nickname, sessionID, clientMetrics, errChan)
	
	// Start heartbeat goroutine
	go sendHeartbeat(ctx, conn, serverAddr, sessionID, clientMetrics, errChan)
	
	// Goroutine to process and display messages
	go func() {
		for {
			select {
			case <-ctx.Done():
				return
			case msg := <-msgChan:
				fmt.Printf("\r%s\n", msg)
				fmt.Print("You: ")
			}
		}
	}()
	
	// Periodically report metrics
	go func() {
		ticker := time.NewTicker(1 * time.Minute)
		defer ticker.Stop()
		
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				fmt.Println(clientMetrics.Report())
			}
		}
	}()
	
	// Wait for error or context cancellation
	select {
	case err := <-errChan:
		if err != nil {
			fmt.Println("Connection error:", err)
			clientMetrics.RecordError()
		}
	case <-ctx.Done():
		// Context was cancelled (likely by signal handler)
		// Send a leaving message
		msg := fmt.Sprintf("CMD:LEAVE|ID:%s|NAME:%s", sessionID, nickname)
		conn.WriteToUDP([]byte(msg), serverAddr)
	}
	
	// Print final metrics before exiting
	fmt.Println(clientMetrics.Report())
}

// connectWithRetry attempts to connect to the server with retry logic
func connectWithRetry(ctx context.Context, serverAddr *net.UDPAddr, maxAttempts int, metrics *metrics.ClientMetrics) (*net.UDPConn, error) {
	var (
		conn *net.UDPConn
		err  error
	)
	
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("connection cancelled")
		default:
			fmt.Printf("Connecting to %s (attempt %d/%d)...\n", serverAddr, attempt, maxAttempts)
			
			// Create local address to bind to any port
			localAddr, err := net.ResolveUDPAddr("udp", "0.0.0.0:0")
			if err != nil {
				return nil, fmt.Errorf("failed to resolve local address: %v", err)
			}
			
			// Create UDP connection
			conn, err = net.DialUDP("udp", localAddr, serverAddr)
			if err == nil {
				// Send a ping to check if server is responsive
				_, err = conn.Write([]byte("CMD:PING"))
				if err == nil {
					// Set a read deadline
					conn.SetReadDeadline(time.Now().Add(5 * time.Second))
					
					// Try to read a response
					response := make([]byte, 1024)
					_, _, err = conn.ReadFromUDP(response)
					if err == nil {
						// Reset read deadline
						conn.SetReadDeadline(time.Time{})
						return conn, nil
					}
				}
				
				// Close connection on failure
				conn.Close()
			}
			
			metrics.RecordReconnect()
			fmt.Printf("Connection attempt failed: %v\n", err)
			
			if attempt < maxAttempts {
				fmt.Printf("Retrying in %v...\n", reconnectDelay)
				select {
				case <-time.After(reconnectDelay):
					// Wait before next attempt
				case <-ctx.Done():
					return nil, fmt.Errorf("connection cancelled")
				}
			}
		}
	}
	
	return nil, fmt.Errorf("failed after %d attempts: %v", maxAttempts, err)
}

// sendRegistration sends registration message to server
func sendRegistration(conn *net.UDPConn, serverAddr *net.UDPAddr, nickname, sessionID string, metrics *metrics.ClientMetrics) {
	// Format: CMD:JOIN|ID:sessionID|NAME:nickname
	registrationMsg := fmt.Sprintf("CMD:JOIN|ID:%s|NAME:%s", sessionID, nickname)
	_, err := conn.WriteToUDP([]byte(registrationMsg), serverAddr)
	if err != nil {
		fmt.Printf("Error sending registration: %v\n", err)
		return
	}
	metrics.MessageSent()
}

// readFromServer handles messages coming from the server
func readFromServer(ctx context.Context, conn *net.UDPConn, metrics *metrics.ClientMetrics, errChan chan<- error, msgChan chan<- string) {
	buffer := make([]byte, maxMessageSize)
	
	for {
		select {
		case <-ctx.Done():
			return
		default:
			// Set read deadline to detect server disconnection
			conn.SetReadDeadline(time.Now().Add(readTimeout))
			
			n, _, err := conn.ReadFromUDP(buffer)
			if err != nil {
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					// Just a timeout, continue
					continue
				}
				
				errChan <- fmt.Errorf("read error: %v", err)
				return
			}
			
			// Reset read deadline
			conn.SetReadDeadline(time.Time{})
			
			// Process the message
			receivedTime := time.Now()
			message := string(buffer[:n])
			
			// Handle special messages
			if strings.HasPrefix(message, "CMD:") {
				// Handle commands from server
				if message == "CMD:PING" {
					// Respond with pong
					conn.Write([]byte("CMD:PONG"))
					metrics.MessageSent()
					continue
				} else if message == "CMD:PONG" {
					// This is a response to our ping, ignore
					continue
				}
				continue
			}
			
			// Parse latency information if present
			parts := strings.SplitN(message, "|", 2)
			if len(parts) == 2 {
				sentTime, err := time.Parse(time.RFC3339Nano, parts[0])
				if err == nil {
					// Calculate latency
					latency := receivedTime.Sub(sentTime).Nanoseconds()
					metrics.AddLatency(latency)
					
					// Keep just the message part
					message = parts[1]
				}
			}
			
			metrics.MessageReceived()
			
			// Send message to display goroutine
			select {
			case msgChan <- message:
				// Message sent successfully
			default:
				// Channel buffer full, drop message
			}
		}
	}
}

// writeToServer handles sending messages to the server
func writeToServer(ctx context.Context, conn *net.UDPConn, serverAddr *net.UDPAddr, nickname, sessionID string, metrics *metrics.ClientMetrics, errChan chan<- error) {
	reader := bufio.NewReader(os.Stdin)
	
	for {
		select {
		case <-ctx.Done():
			return
		default:
			// Show prompt
			fmt.Print("You: ")
			
			// Read message from user (blocking)
			text, err := reader.ReadString('\n')
			if err != nil {
				errChan <- fmt.Errorf("input error: %v", err)
				return
			}
			
			// Clean the input
			text = shared.SanitizeInput(text)
			
			// Skip empty messages
			if text == "" {
				continue
			}
			
			// Check for client-side commands
			if strings.HasPrefix(text, "/") {
				cmd := strings.SplitN(text, " ", 2)[0]
				switch cmd {
				case "/quit":
					// Send leave message and exit
					leaveMsg := fmt.Sprintf("CMD:LEAVE|ID:%s|NAME:%s", sessionID, nickname)
					conn.WriteToUDP([]byte(leaveMsg), serverAddr)
					metrics.MessageSent()
					errChan <- nil // Signal clean exit
					return
				default:
					// Pass command to server
					cmdMsg := fmt.Sprintf("CMD:%s|ID:%s|NAME:%s", text[1:], sessionID, nickname)
					_, err = conn.WriteToUDP([]byte(cmdMsg), serverAddr)
				}
			} else {
				// Format message with timestamp for latency calculation
				timestamp := time.Now().Format(time.RFC3339Nano)
				// Format: ID:sessionID|TIME:timestamp|MSG:text
				message := fmt.Sprintf("ID:%s|TIME:%s|MSG:%s", sessionID, timestamp, text)
				
				// Send the message
				_, err = conn.WriteToUDP([]byte(message), serverAddr)
			}
			
			if err != nil {
				errChan <- fmt.Errorf("send error: %v", err)
				return
			}
			
			metrics.MessageSent()
		}
	}
}

// sendHeartbeat periodically sends heartbeat to the server
func sendHeartbeat(ctx context.Context, conn *net.UDPConn, serverAddr *net.UDPAddr, sessionID string, metrics *metrics.ClientMetrics, errChan chan<- error) {
	ticker := time.NewTicker(heartbeatInterval)
	defer ticker.Stop()
	
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			// Send heartbeat message
			heartbeatMsg := fmt.Sprintf("CMD:HEARTBEAT|ID:%s", sessionID)
			_, err := conn.WriteToUDP([]byte(heartbeatMsg), serverAddr)
			if err != nil {
				errChan <- fmt.Errorf("heartbeat error: %v", err)
				return
			}
			metrics.MessageSent()
		}
	}
}