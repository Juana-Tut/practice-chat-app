// Package main implements a TCP chat client
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

	"github.com/Juana-Tut/practice-chat-app/tcp/metrics"
	"github.com/Juana-Tut/practice-chat-app/shared"
)

const (
	serverAddress   = "localhost:4000"
	reconnectDelay  = 3 * time.Second
	reconnectAttempts = 5
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
	
	// Connect to server with reconnect logic
	conn, err := connectWithRetry(ctx, serverAddress, reconnectAttempts, clientMetrics)
	if err != nil {
		fmt.Printf("Failed to connect after %d attempts: %v\n", reconnectAttempts, err)
		return
	}
	defer conn.Close()
	
	// Send nickname to server
	_, err = conn.Write([]byte("/name " + nickname + "\n"))
	if err != nil {
		fmt.Println("Error sending nickname:", err)
		return
	}
	
	fmt.Println("Connected to chat server as", nickname)
	
	// Create channels for coordinating goroutines
	errChan := make(chan error, 2)
	
	// Start goroutine to read from server
	go readFromServer(ctx, conn, clientMetrics, errChan)
	
	// Start goroutine to send messages to server
	go writeToServer(ctx, conn, clientMetrics, errChan)
	
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
	}
	
	// Print final metrics before exiting
	fmt.Println(clientMetrics.Report())
}

// connectWithRetry attempts to connect to the server with retry logic
func connectWithRetry(ctx context.Context, address string, maxAttempts int, metrics *metrics.ClientMetrics) (net.Conn, error) {
	var (
		conn net.Conn
		err  error
	)
	
	for attempt := 1; attempt <= maxAttempts; attempt++ {
		select {
		case <-ctx.Done():
			return nil, fmt.Errorf("connection cancelled")
		default:
			fmt.Printf("Connecting to %s (attempt %d/%d)...\n", address, attempt, maxAttempts)
			
			// Set a connection timeout
			d := net.Dialer{Timeout: 5 * time.Second}
			conn, err = d.DialContext(ctx, "tcp", address)
			
			if err == nil {
				return conn, nil
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

// readFromServer handles messages coming from the server
func readFromServer(ctx context.Context, conn net.Conn, metrics *metrics.ClientMetrics, errChan chan<- error) {
	scanner := bufio.NewScanner(conn)
	
	for {
		select {
		case <-ctx.Done():
			return
		default:
			// Set read deadline to detect server disconnection
			conn.SetReadDeadline(time.Now().Add(30 * time.Second))
			
			if !scanner.Scan() {
				if err := scanner.Err(); err != nil {
					errChan <- fmt.Errorf("read error: %v", err)
				} else {
					errChan <- fmt.Errorf("connection closed by server")
				}
				return
			}
			
			// Reset read deadline
			conn.SetReadDeadline(time.Time{})
			
			// Process the message
			receivedTime := time.Now()
			message := scanner.Text()
			
			// Check if this is a ping from server, respond with pong
			if message == "/ping" {
				_, err := conn.Write([]byte("/pong\n"))
				if err != nil {
					errChan <- fmt.Errorf("failed to send pong: %v", err)
					return
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
			
			// Display the message with new line to clear the input prompt
			fmt.Printf("\r%s\n", message)
			fmt.Print("You: ")
		}
	}
}

// writeToServer handles sending messages to the server
func writeToServer(ctx context.Context, conn net.Conn, metrics *metrics.ClientMetrics, errChan chan<- error) {
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
			
			// Format message with timestamp for latency calculation
			timestamp := time.Now().Format(time.RFC3339Nano)
			message := fmt.Sprintf("%s|%s", timestamp, text)
			
			// Set write deadline
			conn.SetWriteDeadline(time.Now().Add(5 * time.Second))
			
			// Send the message
			_, err = conn.Write([]byte(message + "\n"))
			
			// Reset write deadline
			conn.SetWriteDeadline(time.Time{})
			
			if err != nil {
				errChan <- fmt.Errorf("send error: %v", err)
				return
			}
			
			metrics.MessageSent()
		}
	}
}