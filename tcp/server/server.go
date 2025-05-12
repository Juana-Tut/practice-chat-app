// Package server implements a TCP chat server
package server

import (
	"context"
	"fmt"
	"net"
	"os"
	"os/signal"
	//"sync"
	"syscall"
	"time"

	"github.com/Juana-Tut/practice-chat-app/tcp/metrics"
)

// Global server metrics instance
var (
	serverMetrics *metrics.ServerMetrics
	ctx           context.Context
	cancel        context.CancelFunc
)

// Start initializes and runs the TCP server on the specified address
func Start(address string) {
	// Initialize metrics
	serverMetrics = metrics.NewServerMetrics()
	
	// Create a cancellable context for graceful shutdown
	ctx, cancel = context.WithCancel(context.Background())
	
	// Create a TCP listener on the given address
	listener, err := net.Listen("tcp", address)
	if err != nil {
		fmt.Println("Error starting server:", err)
		os.Exit(1)
	}
	defer listener.Close()
	
	fmt.Println("Server listening on", address)
	
	// Set up signal handling for graceful shutdown
	setupSignalHandler(listener)
	
	// Start metrics reporter
	go reportMetrics(ctx)
	
	// Main server loop
	for {
		select {
		case <-ctx.Done():
			// Server is shutting down
			return
		default:
			// Set accept timeout to make the loop responsive to shutdown signals
			listener.(*net.TCPListener).SetDeadline(time.Now().Add(time.Second))
			
			// Wait for and accept a new connection
			conn, err := listener.Accept()
			if err != nil {
				if netErr, ok := err.(net.Error); ok && netErr.Timeout() {
					// This is just a timeout from our deadline, continue the loop
					continue
				}
				fmt.Println("Error accepting connection:", err)
				serverMetrics.RecordError()
				continue
			}
			
			// Record connection in metrics
			serverMetrics.ClientConnected()
			
			// Launch a goroutine to handle this client
			go handleConnection(ctx, conn)
		}
	}
}

// setupSignalHandler configures handlers for OS signals
func setupSignalHandler(listener net.Listener) {
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	
	go func() {
		<-sig
		fmt.Println("\nShutting down server...")
		
		// Print final metrics
		fmt.Println(serverMetrics.Report())
		
		// Cancel the context to notify all handlers
		cancel()
		
		// Close the listener
		listener.Close()
		
		// Give connections time to close gracefully
		time.Sleep(500 * time.Millisecond)
		
		os.Exit(0)
	}()
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