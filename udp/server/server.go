// Package server implements a UDP chat server
package server

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/Juana-Tut/practice-chat-app/udp/metrics"
)

// Global server metrics instance
var (
	serverMetrics *metrics.ServerMetrics
	ctx           context.Context
	cancel        context.CancelFunc
)

// Config contains server configuration parameters
type Config struct {
	Address         string
	BufferSize      int
	HeartbeatPeriod time.Duration
	ClientTimeout   time.Duration
}

// DefaultConfig provides sensible defaults for UDP server
func DefaultConfig() Config {
	return Config{
		Address:         "0.0.0.0:4000",
		BufferSize:      1024,
		HeartbeatPeriod: 15 * time.Second,
		ClientTimeout:   60 * time.Second,
	}
}

// Start initializes and runs the UDP server on the specified address
func Start(address string) {
	config := DefaultConfig()
	config.Address = address
	StartWithConfig(config)
}

// StartWithConfig initializes and runs the UDP server with the specified configuration
func StartWithConfig(config Config) {
	// Initialize metrics
	serverMetrics = metrics.NewServerMetrics()
	
	// Create a cancellable context for graceful shutdown
	ctx, cancel = context.WithCancel(context.Background())
	
	// Resolve UDP address
	addr, err := net.ResolveUDPAddr("udp", config.Address)
	if err != nil {
		fmt.Println("Error resolving address:", err)
		os.Exit(1)
	}
	
	// Create a UDP connection
	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		fmt.Println("Error starting server:", err)
		os.Exit(1)
	}
	defer conn.Close()
	
	fmt.Println("Server listening on", config.Address)
	
	// Set up signal handling for graceful shutdown
	setupSignalHandler(conn)
	
	// Start metrics reporter
	go reportMetrics(ctx)
	
	// Initialize the client handler
	InitHandler(ctx, conn, config.ClientTimeout, config.BufferSize)
	
	// Start heartbeat mechanism
	go sendHeartbeats(ctx, conn, config.HeartbeatPeriod)
	
	// Start the receive loop
	go receiveLoop(ctx, conn, config.BufferSize)
	
	// Block until context is cancelled
	<-ctx.Done()
}

// setupSignalHandler configures handlers for OS signals
func setupSignalHandler(conn *net.UDPConn) {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	
	go func() {
		<-sig
		fmt.Println("\nShutting down server...")
		
		// Notify clients of shutdown
		notifyClientsOfShutdown(conn)
		
		// Print final metrics
		fmt.Println(serverMetrics.Report())
		
		// Cancel the context to notify all handlers
		cancel()
		
		// Close the connection
		conn.Close()
		
		// Give time for cleanup
		time.Sleep(500 * time.Millisecond)
		
		os.Exit(0)
	}()
}

// notifyClientsOfShutdown sends a shutdown message to all connected clients
func notifyClientsOfShutdown(conn *net.UDPConn) {
	// Prepare shutdown message packet
	shutdownMsg := createMessagePacket(MessageTypeServerShutdown, 0, 0, "Server is shutting down")
	
	// Send to all active clients
	for _, client := range GetAllClients() {
		_, err := conn.WriteToUDP(shutdownMsg, client.Addr)
		if err != nil {
			fmt.Println("Error sending shutdown notification:", err)
		}
	}
}

// reportMetrics periodically prints server metrics
func reportMetrics(ctx context.Context) {
	ticker := time.NewTicker(30 * time.Second)
	defer ticker.Stop()
	
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			fmt.Println(serverMetrics.Report())
		}
	}
}

// receiveLoop continuously receives and processes incoming UDP packets
func receiveLoop(ctx context.Context, conn *net.UDPConn, bufferSize int) {
	buffer := make([]byte, bufferSize)
	
	for {
		select {
		case <-ctx.Done():
			return
		default:
			// Set read deadline to make loop responsive to context cancellation
			conn.SetReadDeadline(time.Now().Add(time.Second))
			
			n, addr, err := conn.ReadFromUDP(buffer)
			if err != nil {
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					// This is just a timeout from our deadline, continue
					continue
				}
				
				fmt.Println("Error reading UDP packet:", err)
				serverMetrics.RecordError()
				continue
			}
			
			// Process the received packet
			serverMetrics.MessageReceived(n)
			handlePacket(conn, addr, buffer[:n])
		}
	}
}

// sendHeartbeats periodically sends heartbeat packets to all connected clients
func sendHeartbeats(ctx context.Context, conn *net.UDPConn, period time.Duration) {
	ticker := time.NewTicker(period)
	defer ticker.Stop()
	
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			now := time.Now()
			pingPacket := createMessagePacket(MessageTypePing, 0, 0, "")
			
			// Send ping to all clients and check for timeouts
			for _, client := range GetAllClients() {
				// If client hasn't responded for too long, mark as disconnected
				if now.Sub(client.LastSeen) > getClientTimeout() {
					HandleClientDisconnect(client, true) // Timed out
					continue
				}
				
				// Send ping to active client
				_, err := conn.WriteToUDP(pingPacket, client.Addr)
				if err != nil {
					fmt.Printf("Error sending ping to %s: %v\n", client.Addr.String(), err)
					serverMetrics.RecordError()
				} else {
					serverMetrics.MessageSent()
				}
			}
		}
	}
}

// Gracefully shut down connections
func GracefulShutdown() {
	if cancel != nil {
		cancel()
	}
	
	// Print final metrics before exit
	if serverMetrics != nil {
		fmt.Println(serverMetrics.Report())
	}
}