// stress_test_udp.go
package main

import (
	"context"
	"flag"
	"fmt"
	"math/rand"
	"net"
	"os"
	"os/signal"
	"strings"
	"sync"
	"syscall"
	"time"
)

var (
	numClients   = flag.Int("clients", 10, "Number of simulated clients")
	serverAddr   = flag.String("server", "localhost:4000", "Server address")
	duration     = flag.Duration("duration", 1*time.Minute, "Test duration")
	messageRate  = flag.Float64("rate", 0.5, "Messages per second per client")
	messageSize  = flag.Int("size", 20, "Average message size in words")
	verboseMode  = flag.Bool("verbose", false, "Verbose output")
)

type clientStats struct {
	MessagesSent     int
	MessagesReceived int
	mu               sync.Mutex
}

type testStats struct {
	TotalMessagesSent int
	TotalMessagesRecv int
	StartTime         time.Time
	mu                sync.Mutex
}

var stats = testStats{StartTime: time.Now()}

var wordList = []string{
	"hello", "chat", "test", "server", "client", "message", "random", "word",
	"network", "protocol", "UDP", "packet", "connection", "socket", "buffer",
	"latency", "throughput", "bandwidth", "performance", "system", "communication",
	"program", "golang", "application", "metrics", "monitoring", "logging",
	"timeout", "error", "success", "failure", "retry", "disconnect", "reconnect",
}

func main() {
	flag.Parse()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// Handle interrupt signals
	sig := make(chan os.Signal, 1)
	signal.Notify(sig, syscall.SIGINT, syscall.SIGTERM)
	go func() {
		<-sig
		fmt.Println("\nShutting down test...")
		cancel()
	}()

	var wg sync.WaitGroup

	// Start UDP server
	wg.Add(1)
	go runUDPServer(ctx, &wg)

	// Start clients
	for i := 0; i < *numClients; i++ {
		wg.Add(1)
		go runUDPClient(ctx, i+1, &wg)
		time.Sleep(100 * time.Millisecond) // Stagger client starts
	}

	// Wait for the test duration
	select {
	case <-time.After(*duration):
		cancel()
	case <-ctx.Done():
	}

	wg.Wait()
	printFinalStats()
}

func runUDPServer(ctx context.Context, wg *sync.WaitGroup) {
	defer wg.Done()

	addr, err := net.ResolveUDPAddr("udp", *serverAddr)
	if err != nil {
		fmt.Printf("Server: Error resolving address: %v\n", err)
		return
	}

	conn, err := net.ListenUDP("udp", addr)
	if err != nil {
		fmt.Printf("Server: Error listening: %v\n", err)
		return
	}
	defer conn.Close()

	buffer := make([]byte, 1024)

	for {
		select {
		case <-ctx.Done():
			return
		default:
			conn.SetReadDeadline(time.Now().Add(1 * time.Second))
			n, clientAddr, err := conn.ReadFromUDP(buffer)
			if err != nil {
				if ne, ok := err.(net.Error); ok && ne.Timeout() {
					continue
				}
				fmt.Printf("Server: Read error: %v\n", err)
				continue
			}

			message := strings.TrimSpace(string(buffer[:n]))
			if *verboseMode {
				fmt.Printf("Server received from %v: %s\n", clientAddr, message)
			}

			// Echo the message back to the client
			_, err = conn.WriteToUDP([]byte(message), clientAddr)
			if err != nil && *verboseMode {
				fmt.Printf("Server: Write error: %v\n", err)
			}

			stats.mu.Lock()
			stats.TotalMessagesRecv++
			stats.mu.Unlock()
		}
	}
}

func runUDPClient(ctx context.Context, id int, wg *sync.WaitGroup) {
	defer wg.Done()

	serverUDPAddr, err := net.ResolveUDPAddr("udp", *serverAddr)
	if err != nil {
		fmt.Printf("Client %d: Error resolving server address: %v\n", id, err)
		return
	}

	localAddr := &net.UDPAddr{IP: net.IPv4zero, Port: 0}
	conn, err := net.DialUDP("udp", localAddr, serverUDPAddr)
	if err != nil {
		fmt.Printf("Client %d: Error dialing UDP: %v\n", id, err)
		return
	}
	defer conn.Close()

	cstats := clientStats{}
	messageInterval := time.Duration(1e9 / *messageRate)

	ticker := time.NewTicker(messageInterval)
	defer ticker.Stop()

	buffer := make([]byte, 1024)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ticker.C:
			message := generateRandomMessage(*messageSize)
			_, err := conn.Write([]byte(message))
			if err != nil {
				if *verboseMode {
					fmt.Printf("Client %d: Write error: %v\n", id, err)
				}
				continue
			}

			cstats.mu.Lock()
			cstats.MessagesSent++
			cstats.mu.Unlock()

			stats.mu.Lock()
			stats.TotalMessagesSent++
			stats.mu.Unlock()

			conn.SetReadDeadline(time.Now().Add(1 * time.Second))
			n, _, err := conn.ReadFromUDP(buffer)
			if err != nil {
				if ne, ok := err.(net.Error); ok && ne.Timeout() {
					if *verboseMode {
						fmt.Printf("Client %d: Read timeout\n", id)
					}
					continue
				}
				if *verboseMode {
					fmt.Printf("Client %d: Read error: %v\n", id, err)
				}
				continue
			}

			receivedMessage := strings.TrimSpace(string(buffer[:n]))
			if *verboseMode {
				fmt.Printf("Client %d received: %s\n", id, receivedMessage)
			}

			cstats.mu.Lock()
			cstats.MessagesReceived++
			cstats.mu.Unlock()
		}
	}
}

func generateRandomMessage(wordCount int) string {
	words := make([]string, wordCount)
	for i := 0; i < wordCount; i++ {
		words[i] = wordList[rand.Intn(len(wordList))]
	}
	return strings.Join(words, " ")
}

func printFinalStats() {
	stats.mu.Lock()
	defer stats.mu.Unlock()

	elapsed := time.Since(stats.StartTime).Seconds()
	sendRate := float64(stats.TotalMessagesSent) / elapsed
	recvRate := float64(stats.TotalMessagesRecv) / elapsed
	lossRate := 0.0
	if stats.TotalMessagesSent > 0 {
		lossRate = 100.0 * (1.0 - float64(stats.TotalMessagesRecv)/float64(stats.TotalMessagesSent))
	}

	fmt.Printf("\n--- Final Test Results ---\n")
	fmt.Printf("Test Duration: %.1f seconds\n", elapsed)
	fmt.Printf("Messages Sent: %d (%.2f msgs/sec)\n", stats.TotalMessagesSent, sendRate)
	fmt.Printf("Messages Received: %d (%.2f msgs/sec)\n", stats.TotalMessagesRecv, recvRate)
	fmt.Printf("Estimated Packet Loss: %.2f%%\n", lossRate)
}
