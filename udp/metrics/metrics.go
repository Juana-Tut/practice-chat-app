// Package metrics provides functionality for collecting and reporting UDP server and client metrics
package metrics

import (
	"fmt"
	"sync"
	"sync/atomic"
	"time"
)

// ServerMetrics holds all server-side metrics
type ServerMetrics struct {
	ActiveClients        int64
	TotalClients         int64
	MessagesSent         int64
	MessagesReceived     int64
	StartTime            time.Time
	TotalLatency         int64
	ErrorCount           int64
	ClientDisconnects    int64
	AbnormalDisconnects  int64
	MaxConcurrentClients int64
	MessageSizeTotal     int64
	MessageCount         int64
	PacketLoss           int64
	DatagramsSent        int64
	DatagramsReceived    int64
	LatencyMeasurements  int64
	mutex                sync.Mutex
}

// NewServerMetrics creates and initializes a new ServerMetrics instance
func NewServerMetrics() *ServerMetrics {
	return &ServerMetrics{
		StartTime: time.Now(),
	}
}

// ClientConnected increments connection counters when a new client connects
func (m *ServerMetrics) ClientConnected() {
	atomic.AddInt64(&m.TotalClients, 1)
	current := atomic.AddInt64(&m.ActiveClients, 1)
	
	// Update max concurrent clients if needed
	m.mutex.Lock()
	if current > m.MaxConcurrentClients {
		m.MaxConcurrentClients = current
	}
	m.mutex.Unlock()
}

// ClientDisconnected decrements active connection counter when a client disconnects
func (m *ServerMetrics) ClientDisconnected(abnormal bool) {
	atomic.AddInt64(&m.ActiveClients, -1)
	atomic.AddInt64(&m.ClientDisconnects, 1)
	if abnormal {
		atomic.AddInt64(&m.AbnormalDisconnects, 1)
	}
}

// MessageSent increments the sent messages counter
func (m *ServerMetrics) MessageSent() {
	atomic.AddInt64(&m.MessagesSent, 1)
	atomic.AddInt64(&m.DatagramsSent, 1)
}

// MessageReceived increments the received messages counter and tracks message size
func (m *ServerMetrics) MessageReceived(size int) {
	atomic.AddInt64(&m.MessagesReceived, 1)
	atomic.AddInt64(&m.MessageSizeTotal, int64(size))
	atomic.AddInt64(&m.MessageCount, 1)
	atomic.AddInt64(&m.DatagramsReceived, 1)
}

// AddLatency adds a latency measurement to the total
func (m *ServerMetrics) AddLatency(latencyNs int64) {
	atomic.AddInt64(&m.TotalLatency, latencyNs)
	atomic.AddInt64(&m.LatencyMeasurements, 1)
}

// RecordError increments the error counter
func (m *ServerMetrics) RecordError() {
	atomic.AddInt64(&m.ErrorCount, 1)
}

// RecordPacketLoss increments the packet loss counter
func (m *ServerMetrics) RecordPacketLoss() {
	atomic.AddInt64(&m.PacketLoss, 1)
}

// Report generates a human-readable report of current metrics
func (m *ServerMetrics) Report() string {
	duration := time.Since(m.StartTime)
	activeClients := atomic.LoadInt64(&m.ActiveClients)
	totalClients := atomic.LoadInt64(&m.TotalClients)
	sent := atomic.LoadInt64(&m.MessagesSent)
	received := atomic.LoadInt64(&m.MessagesReceived)
	errors := atomic.LoadInt64(&m.ErrorCount)
	disconnects := atomic.LoadInt64(&m.ClientDisconnects)
	abnormalDisconnects := atomic.LoadInt64(&m.AbnormalDisconnects)
	maxClients := m.MaxConcurrentClients
	messageSize := atomic.LoadInt64(&m.MessageSizeTotal)
	messageCount := atomic.LoadInt64(&m.MessageCount)
	packetLoss := atomic.LoadInt64(&m.PacketLoss)
	datagramsSent := atomic.LoadInt64(&m.DatagramsSent)
	datagramsReceived := atomic.LoadInt64(&m.DatagramsReceived)
	
	var avgLatency, avgMessageSize, throughput float64
	
	latencyMeasurements := atomic.LoadInt64(&m.LatencyMeasurements)
	if latencyMeasurements > 0 {
		avgLatency = float64(atomic.LoadInt64(&m.TotalLatency)) / float64(latencyMeasurements) / 1e6 // Convert to ms
	}
	
	if messageCount > 0 {
		avgMessageSize = float64(messageSize) / float64(messageCount)
	}
	
	if duration.Seconds() > 0 {
		throughput = float64(received) / duration.Seconds()
	}
	
	// Calculate packet loss rate
	var packetLossRate float64
	if datagramsSent > 0 {
		packetLossRate = float64(packetLoss) / float64(datagramsSent) * 100
	}
	
	report := fmt.Sprintf("\n--- UDP Server Metrics ---\n")
	report += fmt.Sprintf("Uptime: %s\n", duration.Round(time.Second))
	report += fmt.Sprintf("Active Clients: %d\n", activeClients)
	report += fmt.Sprintf("Total Clients: %d\n", totalClients)
	report += fmt.Sprintf("Max Concurrent Clients: %d\n", maxClients)
	report += fmt.Sprintf("Messages Received: %d\n", received)
	report += fmt.Sprintf("Messages Sent: %d\n", sent)
	report += fmt.Sprintf("Datagrams Received: %d\n", datagramsReceived)
	report += fmt.Sprintf("Datagrams Sent: %d\n", datagramsSent)
	report += fmt.Sprintf("Average Message Size: %.2f bytes\n", avgMessageSize)
	report += fmt.Sprintf("Throughput: %.2f messages/sec\n", throughput)
	report += fmt.Sprintf("Average Latency: %.2f ms\n", avgLatency)
	report += fmt.Sprintf("Packet Loss Rate: %.2f%%\n", packetLossRate)
	report += fmt.Sprintf("Client Disconnects: %d (Abnormal: %d)\n", disconnects, abnormalDisconnects)
	report += fmt.Sprintf("Errors: %d\n", errors)
	
	return report
}

// ClientMetrics holds all client-side metrics
type ClientMetrics struct {
	MessagesSent        int64
	MessagesReceived    int64
	StartTime           time.Time
	TotalLatency        int64
	LatencyMeasurements int64
	ErrorCount          int64
	ReconnectAttempts   int64
	PacketsDropped      int64
	DatagramsSent       int64
	DatagramsReceived   int64
}

// NewClientMetrics creates and initializes a new ClientMetrics instance
func NewClientMetrics() *ClientMetrics {
	return &ClientMetrics{
		StartTime: time.Now(),
	}
}

// MessageSent increments the sent messages counter
func (m *ClientMetrics) MessageSent() {
	atomic.AddInt64(&m.MessagesSent, 1)
	atomic.AddInt64(&m.DatagramsSent, 1)
}

// MessageReceived increments the received messages counter
func (m *ClientMetrics) MessageReceived() {
	atomic.AddInt64(&m.MessagesReceived, 1)
	atomic.AddInt64(&m.DatagramsReceived, 1)
}

// AddLatency adds a latency measurement to the total
func (m *ClientMetrics) AddLatency(latencyNs int64) {
	atomic.AddInt64(&m.TotalLatency, latencyNs)
	atomic.AddInt64(&m.LatencyMeasurements, 1)
}

// RecordError increments the error counter
func (m *ClientMetrics) RecordError() {
	atomic.AddInt64(&m.ErrorCount, 1)
}

// RecordReconnect increments the reconnection attempts counter
func (m *ClientMetrics) RecordReconnect() {
	atomic.AddInt64(&m.ReconnectAttempts, 1)
}

// RecordPacketDrop increments the packet drop counter
func (m *ClientMetrics) RecordPacketDrop() {
	atomic.AddInt64(&m.PacketsDropped, 1)
}

// Report generates a human-readable report of current metrics
func (m *ClientMetrics) Report() string {
	duration := time.Since(m.StartTime).Seconds()
	sent := atomic.LoadInt64(&m.MessagesSent)
	received := atomic.LoadInt64(&m.MessagesReceived)
	errors := atomic.LoadInt64(&m.ErrorCount)
	reconnects := atomic.LoadInt64(&m.ReconnectAttempts)
	packetsDropped := atomic.LoadInt64(&m.PacketsDropped)
	datagramsSent := atomic.LoadInt64(&m.DatagramsSent)
	datagramsReceived := atomic.LoadInt64(&m.DatagramsReceived)
	
	var avgLatency, throughput float64
	var packetLoss string
	
	latencyMeasurements := atomic.LoadInt64(&m.LatencyMeasurements)
	if latencyMeasurements > 0 {
		avgLatency = float64(atomic.LoadInt64(&m.TotalLatency)) / float64(latencyMeasurements) / 1e6 // Convert to ms
	}
	
	if duration > 0 {
		throughput = float64(received) / duration
	}
	
	if sent > 0 {
		packetLossPercentage := float64(packetsDropped) / float64(sent) * 100
		packetLoss = fmt.Sprintf("%.2f%%", packetLossPercentage)
	} else {
		packetLoss = "N/A"
	}
	
	report := fmt.Sprintf("\n--- UDP Client Metrics ---\n")
	report += fmt.Sprintf("Session Duration: %.1f seconds\n", duration)
	report += fmt.Sprintf("Messages Sent: %d\n", sent)
	report += fmt.Sprintf("Messages Received: %d\n", received)
	report += fmt.Sprintf("Datagrams Sent: %d\n", datagramsSent)
	report += fmt.Sprintf("Datagrams Received: %d\n", datagramsReceived)
	report += fmt.Sprintf("Packet Loss: %s\n", packetLoss)
	report += fmt.Sprintf("Throughput: %.2f messages/sec\n", throughput)
	report += fmt.Sprintf("Average Latency: %.2f ms\n", avgLatency)
	report += fmt.Sprintf("Errors: %d\n", errors)
	report += fmt.Sprintf("Reconnection Attempts: %d\n", reconnects)
	
	return report
}