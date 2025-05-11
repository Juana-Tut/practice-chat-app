// Package main provides tools for stress testing the chat server
package main

import (
	"bufio"
	"context"
	"flag"
	"fmt"
	"math/rand"
	"net"
	"os"
	"os/signal"
	"sync"
	"syscall"
	"time"

	"github.com/2016114132/chat-app-final-project/shared"
)

// Test configuration flags
var (
	numClients   = flag.Int("clients", 10, "Number of simulated clients")
	serverAddr   = flag.String("server", "localhost:4000", "Server address")
	duration     = flag.Duration("duration", 1*time.Minute, "Test duration")
	messageRate  = flag.Float64("rate", 0.5, "Messages per second per client")
	messageSize  = flag.Int("size", 20, "Average message size in words")
	bursty       = flag.Bool("bursty", false, "Use bursty traffic pattern")
	dropRate     = flag.Float64("drop", 0.05, "Client random disconnect rate")
	reconnect    = flag.Bool("reconnect", true, "Reconnect dropped clients")
	randomDelay  = flag.Bool("delay", true, "Add random delays between messages")
	verboseMode  = flag.Bool("verbose", false, "Verbose output")
)

// Client metrics
type clientStats struct {
	MessagesSent     int
	MessagesReceived int
	Errors           int
	Reconnects       int
	mu               sync.Mutex
}

// Test metrics
type testStats struct {
	TotalClients     int
	ActiveClients    int
	TotalMessagesSent int
	TotalMessagesRecv int
	TotalErrors      int
	TotalReconnects  int
	StartTime        time.Time
	mu               sync.Mutex
}

// Global test statistics
var stats = testStats{StartTime: time.Now()}
var statsMutex sync.Mutex

// List of words for random message generation
var wordList = []string{
	"hello", "chat", "test", "server", "client", "message", "random", "word", 
	"network", "protocol", "TCP", "packet", "connection", "socket", "buffer",
	"latency", "throughput", "bandwidth", "performance", "system", "communication",
	"program", "golang", "application", "metrics", "monitoring", "logging",
	"timeout", "error", "success", "failure", "retry", "disconnect", "reconnect",
}

func main() {
	// Parse command-line flags
	flag.Parse()
	
	// Set up context for graceful shutdown
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	
	// Setup signal handling
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		fmt.Println("\nShutting down test...")
		cancel()
	}()
	
	// Create a WaitGroup to track all goroutines
	var wg sync.WaitGroup
	
	// Start metrics reporter
	go reportMetrics(ctx)
	
	// Start timer for test duration
	timer := time.NewTimer(*duration)
	
	// Track active clients
	activeClients := make(map[int]context.CancelFunc)
	var activeMutex sync.Mutex
	
	fmt.Printf("Starting stress test with %d clients for %v\n", *numClients, *duration)
	fmt.Printf("Server: %s, Message rate: %.2f msg/sec/client\n", *serverAddr, *messageRate)
	
	// Launch initial clients
	for i := 0; i < *numClients; i++ {
		clientID := i + 1
		clientCtx, clientCancel := context.WithCancel(ctx)
		
		activeMutex.Lock()
		activeClients[clientID] = clientCancel
		activeMutex.Unlock()
		
		wg.Add(1)
		go runClient(clientCtx, clientID, &wg)
		
		// Stagger client starts to avoid all connecting at once
		time.Sleep(time.Duration(rand.Intn(100)) * time.Millisecond)
	}
	
	// Main event loop
	eventTicker := time.NewTicker(1 * time.Second)
	defer eventTicker.Stop()
	
	for {
		select {
		case <-timer.C:
			// Test duration has elapsed
			fmt.Println("Test duration completed")
			cancel()
			break
			
		case <-eventTicker.C:
			if *dropRate > 0 && rand.Float64() < *dropRate {
				// Randomly disconnect a client
				activeMutex.Lock()
				if len(activeClients) > 0 {
					// Select a random client to disconnect
					var clients []int
					for id := range activeClients {
						clients = append(clients, id)
					}
					if len(clients) > 0 {
						victimID := clients[rand.Intn(len(clients))]
						clientCancel := activeClients[victimID]
						delete(activeClients, victimID)
						
						if *verboseMode {
							fmt.Printf("Randomly disconnecting client %d\n", victimID)
						}
						
						// Cancel the client's context to trigger disconnect
						clientCancel()
						
						// Optionally reconnect the client after a delay
						if *reconnect {
							time.AfterFunc(time.Duration(1+rand.Intn(5))*time.Second, func() {
								if ctx.Err() != nil {
									return // Don't reconnect if main context is done
								}
								
								newCtx, newCancel := context.WithCancel(ctx)
								activeMutex.Lock()
								activeClients[victimID] = newCancel
								activeMutex.Unlock()
								
								wg.Add(1)
								go runClient(newCtx, victimID, &wg)
								
								if *verboseMode {
									fmt.Printf("Reconnecting client %d\n", victimID)
								}
							})
						}
					}
				}
				activeMutex.Unlock()
			}
			
		case <-ctx.Done():
			// Wait for all clients to finish
			wg.Wait()
			fmt.Println("\nTest completed!")
			printFinalStats()
			return
		}
	}
}

// runClient simulates a single chat client
func runClient(ctx context.Context, id int, wg *sync.WaitGroup) {
	defer wg.Done()
	
	// Track client-specific stats
	cstats := clientStats{}
	
	// Update global stats
	statsMutex.Lock()
	stats.TotalClients++
	stats.ActiveClients++
	statsMutex.Unlock()
	
	defer func() {
		statsMutex.Lock()
		stats.ActiveClients--
		statsMutex.Unlock()
	}()
	
	// Connect to the server
	conn, err := net.Dial("tcp", *serverAddr)
	if err != nil {
		fmt.Printf("Client %d: Error connecting: %v\n", id, err)
		
		cstats.mu.Lock()
		cstats.Errors++
		cstats.mu.Unlock()
		
		statsMutex.Lock()
		stats.TotalErrors++
		statsMutex.Unlock()
		
		return
	}
	defer conn.Close()
	
	// Generate a nickname for this client
	nickname := fmt.Sprintf("TestClient%d", id)
	
	// Send the nickname to the server
	_, err = conn.Write([]byte("/name " + nickname + "\n"))
	if err != nil {
		if *verboseMode {
			fmt.Printf("Client %d: Error sending nickname: %v\n", id, err)
		}
		return
	}
	
	// Create a reader for server messages
	reader := bufio.NewReader(conn)
	
	// Set up message channel and done signal
	msgChan := make(chan string, 10)
	done := make(chan struct{})
	
	// Start reader goroutine
	go func() {
		defer close(done)
		
		for {
			// Set a read deadline to detect server failures
			conn.SetReadDeadline(time.Now().Add(10 * time.Second))
			
			// Read a message from the server
			message, err := reader.ReadString('\n')
			if err != nil {
				if ctx.Err() == nil { // Only log if not due to context cancellation
					if *verboseMode {
						fmt.Printf("Client %d: Read error: %v\n", id, err)
					}
					
					cstats.mu.Lock()
					cstats.Errors++
					cstats.mu.Unlock()
					
					statsMutex.Lock()
					stats.TotalErrors++
					statsMutex.Unlock()
				}
				return
			}
			
			// Reset the read deadline
			conn.SetReadDeadline(time.Time{})
			
			// Process the message
			message = strings.TrimSpace(message)
			
			// Handle server ping
			if message == "/ping" {
				conn.Write([]byte("/pong\n"))
				continue
			}
			
			cstats.mu.Lock()
			cstats.MessagesReceived++
			cstats.mu.Unlock()
			
			statsMutex.Lock()
			stats.TotalMessagesRecv++
			statsMutex.Unlock()
			
			select {
			case msgChan <- message:
				// Message sent to channel
			case <-ctx.Done():
				// Context cancelled
				return
			default:
				// Channel buffer full, just drop the message
			}
		}
	}()
	
	// Calculate message interval based on rate
	var messageInterval time.Duration
	if *messageRate > 0 {
		messageInterval = time.Duration(1000 / *messageRate) * time.Millisecond
	} else {
		messageInterval = 10 * time.Second // Very slow rate as fallback
	}
	
	// Create a ticker for sending messages
	ticker := time.NewTicker(messageInterval)
	defer ticker.Stop()
	
	// Metrics reporting ticker
	reportTicker := time.NewTicker(30 * time.Second)
	defer reportTicker.Stop()
	
	// Main client loop
	for {
		select {
		case <-ctx.Done():
			// Wait for reader to finish
			<-done
			return
			
		case <-ticker.C:
			// Should we send a message?
			if !*bursty || rand.Float64() < 0.3 {
				// Generate random message
				message := generateRandomMessage(*messageSize)
				
				// Add timestamp for latency measurement
				timestamp := time.Now().Format(time.RFC3339Nano)
				formattedMsg := fmt.Sprintf("%s|%s", timestamp, message)
				
				// Send the message
				_, err := conn.Write([]byte(formattedMsg + "\n"))
				if err != nil {
					if *verboseMode {
						fmt.Printf("Client %d: Write error: %v\n", id, err)
					}
					
					cstats.mu.Lock()
					cstats.Errors++
					cstats.mu.Unlock()
					
					statsMutex.Lock()
					stats.TotalErrors++
					statsMutex.Unlock()
					
					return
				}
				
				cstats.mu.Lock()
				cstats.MessagesSent++
				cstats.mu.Unlock()
				
				statsMutex.Lock()
				stats.TotalMessagesSent++
				statsMutex.Unlock()
				
				// Introduce random delay if enabled
				if *randomDelay && rand.Float64() < 0.2 {
					delay := time.Duration(rand.Intn(500)) * time.Millisecond
					time.Sleep(delay)
				}
				
				// If bursty mode, occasionally send a burst of messages
				if *bursty && rand.Float64() < 0.1 {
					burstCount := 3 + rand.Intn(7) // 3-10 messages
					for i := 0; i < burstCount; i++ {
						burstMsg := generateRandomMessage(*messageSize / 2)
						timestamp := time.Now().Format(time.RFC3339Nano)
						formattedMsg := fmt.Sprintf("%s|%s", timestamp, burstMsg)
						
						conn.Write([]byte(formattedMsg + "\n"))
						
						cstats.mu.Lock()
						cstats.MessagesSent++
						cstats.mu.Unlock()
						
						statsMutex.Lock()
						stats.TotalMessagesSent++
						statsMutex.Unlock()
						
						time.Sleep(50 * time.Millisecond)
					}
				}
			}
			
		case <-reportTicker.C:
			if *verboseMode {
				cstats.mu.Lock()
				fmt.Printf("Client %d: Sent: %d, Received: %d, Errors: %d\n", 
					id, cstats.MessagesSent, cstats.MessagesReceived, cstats.Errors)
				cstats.mu.Unlock()
			}
			
		case msg := <-msgChan:
			// Process received message if needed
			_ = msg // Currently just consuming messages
		}
	}
}

// generateRandomMessage creates a random message with the specified word count
func generateRandomMessage(wordCount int) string {
	// Randomize actual word count slightly
	actualCount := wordCount
	if wordCount > 3 {
		// Vary by up to 30% of requested size
		variation := int(float64(wordCount) * 0.3)
		actualCount = wordCount - variation + rand.Intn(variation*2)
	}
	
	if actualCount < 1 {
		actualCount = 1
	}
	
	words := make([]string, actualCount)
	for i := 0; i < actualCount; i++ {
		words[i] = wordList[rand.Intn(len(wordList))]
	}
	
	return strings.Join(words, " ")
}

// reportMetrics periodically prints test statistics
func reportMetrics(ctx context.Context) {
	ticker := time.NewTicker(10 * time.Second)
	defer ticker.Stop()
	
	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			printStats()
		}
	}
}

// printStats displays current test statistics
func printStats() {
	statsMutex.Lock()
	defer statsMutex.Unlock()
	
	elapsed := time.Since(stats.StartTime).Seconds()
	var sendRate, recvRate float64
	
	if elapsed > 0 {
		sendRate = float64(stats.TotalMessagesSent) / elapsed
		recvRate = float64(stats.TotalMessagesRecv) / elapsed
	}
	
	fmt.Printf("\n--- Stress Test Stats (%.1f sec) ---\n", elapsed)
	fmt.Printf("Active/Total Clients: %d/%d\n", stats.ActiveClients, stats.TotalClients)
	fmt.Printf("Messages Sent: %d (%.2f msgs/sec)\n", stats.TotalMessagesSent, sendRate)
	fmt.Printf("Messages Received: %d (%.2f msgs/sec)\n", stats.TotalMessagesRecv, recvRate)
	fmt.Printf("Errors: %d\n", stats.TotalErrors)
	
	// Calculate and display packet loss
	var lossRate float64
	if stats.TotalMessagesSent > 0 {
		expected := stats.TotalMessagesSent * (stats.ActiveClients - 1) // Each message should be received by all other clients
		if expected > 0 {
			lossRate = 100 * (1 - float64(stats.TotalMessagesRecv)/float64(expected))
		}
	}
	fmt.Printf("Estimated Packet Loss: %.2f%%\n", lossRate)
}

// printFinalStats displays final test statistics
func printFinalStats() {
	printStats()
	
	statsMutex.Lock()
	defer statsMutex.Unlock()
	
	elapsed := time.Since(stats.StartTime).Seconds()
	
	// Additional final stats
	fmt.Printf("\n--- Final Test Results ---\n")
	fmt.Printf("Test Duration: %.1f seconds\n", elapsed)
	
	if stats.TotalClients > stats.TotalClients-stats.ActiveClients {
		fmt.Printf("Clients that disconnected: %d (%.1f%%)\n", 
			stats.TotalClients-stats.ActiveClients,
			100*float64(stats.TotalClients-stats.ActiveClients)/float64(stats.TotalClients))
	}
	
	// Per-client averages
	if stats.TotalClients > 0 {
		fmt.Printf("Avg messages sent per client: %.1f\n", 
			float64(stats.TotalMessagesSent)/float64(stats.TotalClients))
	}
	
	fmt.Println("\nStress test completed!")
}

func init() {
	// Seed the random number generator
	rand.Seed(time.Now().UnixNano())
}