// Package server implements a UDP chat server
package server

import (
	"context"
	"encoding/binary"
	"fmt"
	"net"
	"strings"
	"sync"
	"time"

	"github.com/Juana-Tut/practice-chat-app/shared"
)

// MessageType represents different types of protocol messages
type MessageType byte

const (
	MessageTypeChat          MessageType = 1
	MessageTypeJoin          MessageType = 2
	MessageTypeLeave         MessageType = 3
	MessageTypePing          MessageType = 4
	MessageTypePong          MessageType = 5
	MessageTypeNameRequest   MessageType = 6
	MessageTypeNameResponse  MessageType = 7
	MessageTypeAck           MessageType = 8
	MessageTypeServerShutdown MessageType = 9
)

// Client represents a connected UDP client
type Client struct {
	ID        uint32    // Unique client ID
	Name      string    // Client nickname
	Addr      *net.UDPAddr // Client address
	LastSeen  time.Time // Time of last activity
	SeqNum    uint32    // Last seen sequence number
	Connected bool      // Whether client is considered connected
}

var (
	clients      = make(map[string]*Client) // Map of client address string to client struct
	clientsMutex sync.RWMutex               // For thread-safe access to clients map
	
	nextClientID uint32 = 1                 // For generating unique client IDs
	idMutex      sync.Mutex                 // For thread-safe access to nextClientID
	
	clientTimeout time.Duration             // Timeout for disconnecting inactive clients
	
	conn         *net.UDPConn               // Server's UDP socket
	mainContext  context.Context            // Main server context
	bufferSize   int                        // Buffer size for UDP packets
)

// InitHandler initializes the handler with the server context and connection
func InitHandler(ctx context.Context, c *net.UDPConn, timeout time.Duration, bSize int) {
	mainContext = ctx
	conn = c
	clientTimeout = timeout
	bufferSize = bSize
}

// getClientTimeout returns the current client timeout duration
func getClientTimeout() time.Duration {
	return clientTimeout
}

// getNextClientID generates a new unique client ID
func getNextClientID() uint32 {
	idMutex.Lock()
	defer idMutex.Unlock()
	id := nextClientID
	nextClientID++
	return id
}

// handlePacket processes a received UDP packet
func handlePacket(conn *net.UDPConn, addr *net.UDPAddr, data []byte) {
	// Ensure packet is at least large enough for header
	if len(data) < 10 { // Type (1) + ClientID (4) + SequenceNum (4) + Content length (1)
		fmt.Println("Received malformed packet (too short)")
		return
	}
	
	// Parse packet header
	msgType := MessageType(data[0])
	clientID := binary.BigEndian.Uint32(data[1:5])
	seqNum := binary.BigEndian.Uint32(data[5:9])
	
	// Get or create client
	client := getOrCreateClient(addr, clientID)
	
	// Update last seen time
	client.LastSeen = time.Now()
	
	// Process different message types
	switch msgType {
	case MessageTypeNameResponse:
		// Client is sending their nickname
		if len(data) <= 10 {
			// Empty name, reject
			return
		}
		nameLen := int(data[9])
		if 10+nameLen > len(data) {
			// Invalid packet
			return
		}
		name := string(data[10 : 10+nameLen])
		handleNameResponse(conn, client, name, seqNum)
		
	case MessageTypeChat:
		// Chat message from client
		if len(data) <= 10 {
			// Empty message
			return
		}
		contentLen := int(data[9])
		if 10+contentLen > len(data) {
			// Invalid packet
			return
		}
		message := string(data[10 : 10+contentLen])
		handleChatMessage(conn, client, seqNum, message)
		
	case MessageTypePing:
		// Respond with pong
		handlePing(conn, client, seqNum)
		
	case MessageTypePong:
		// Client responding to our ping
		// Just update LastSeen time which was done above
		
	case MessageTypeLeave:
		// Client intentionally disconnecting
		HandleClientDisconnect(client, false)
		
	case MessageTypeAck:
		// Client acknowledging a message
		// We could implement message retransmission here
		
	default:
		fmt.Printf("Unknown message type: %d from %s\n", msgType, addr.String())
	}
}

// getOrCreateClient returns existing client or creates a new one
func getOrCreateClient(addr *net.UDPAddr, clientID uint32) *Client {
	addrStr := addr.String()
	
	clientsMutex.RLock()
	client, exists := clients[addrStr]
	clientsMutex.RUnlock()
	
	if exists {
		return client
	}
	
	// Create new client if not found
	clientsMutex.Lock()
	defer clientsMutex.Unlock()
	
	// Check again within lock to avoid race condition
	if client, exists = clients[addrStr]; exists {
		return client
	}
	
	// Create new client
	newID := getNextClientID()
	if clientID > 0 {
		// Use client's provided ID if available (reconnection case)
		newID = clientID
	}
	
	client = &Client{
		ID:        newID,
		Name:      addrStr, // Use address as temporary name
		Addr:      addr,
		LastSeen:  time.Now(),
		Connected: false, // Not fully connected until we have a name
	}
	
	clients[addrStr] = client
	
	// Request client's name
	sendNameRequest(conn, client)
	
	return client
}

// handleNameResponse processes a client's nickname response
func handleNameResponse(conn *net.UDPConn, client *Client, name string, seqNum uint32) {
	// Process the name
	cleanName := shared.FormatName(name)
	if cleanName == "" {
		// Invalid name, send request again
		sendNameRequest(conn, client)
		return
	}
	
	wasConnected := client.Connected
	oldName := client.Name
	
	// Update client name
	client.Name = cleanName
	client.Connected = true
	
	// Send ACK for the name
	sendAck(conn, client, seqNum)
	
	if !wasConnected {
		// New client joined
		serverMetrics.ClientConnected()
		fmt.Printf("[+] %s connected from %s\n", client.Name, client.Addr.String())
		
		// Notify other clients about new user
		broadcastMessage(conn, client, fmt.Sprintf("Server: %s has joined the chat.", client.Name))
		
		// Send welcome message to client
		clientCount := countConnectedClients()
		welcomeMsg := fmt.Sprintf("Welcome to the chat, %s! There are %d other users online.", 
			client.Name, clientCount-1)
		sendDirectMessage(conn, client, welcomeMsg)
	} else if oldName != cleanName {
		// Client changed name
		fmt.Printf("[*] Client %s renamed to %s\n", oldName, cleanName)
		broadcastMessage(conn, client, fmt.Sprintf("Server: %s is now known as %s.", oldName, cleanName))
	}
}

// handleChatMessage processes a chat message from a client
func handleChatMessage(conn *net.UDPConn, client *Client, seqNum uint32, message string) {
	// Ignore messages from non-connected clients
	if !client.Connected {
		return
	}
	
	// Send ACK for this message
	sendAck(conn, client, seqNum)
	
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
	broadcastMessage(conn, client, shared.FormatMessage(client.Name, message))
}

// handlePing responds to a client ping
func handlePing(conn *net.UDPConn, client *Client, seqNum uint32) {
	// Send pong response
	pongPacket := createMessagePacket(MessageTypePong, client.ID, seqNum, "")
	_, err := conn.WriteToUDP(pongPacket, client.Addr)
	if err != nil {
		fmt.Printf("Error sending pong to %s: %v\n", client.Addr.String(), err)
		serverMetrics.RecordError()
	} else {
		serverMetrics.MessageSent()
	}
}

// sendNameRequest asks a client for their nickname
func sendNameRequest(conn *net.UDPConn, client *Client) {
	packet := createMessagePacket(MessageTypeNameRequest, client.ID, 0, 
		"Please send your nickname using /name YourNickname")
	_, err := conn.WriteToUDP(packet, client.Addr)
	if err != nil {
		fmt.Printf("Error sending name request to %s: %v\n", client.Addr.String(), err)
		serverMetrics.RecordError()
	} else {
		serverMetrics.MessageSent()
	}
}

// sendAck sends an acknowledgment for a specific message sequence number
func sendAck(conn *net.UDPConn, client *Client, seqNum uint32) {
	ackPacket := createMessagePacket(MessageTypeAck, client.ID, seqNum, "")
	_, err := conn.WriteToUDP(ackPacket, client.Addr)
	if err != nil {
		fmt.Printf("Error sending ACK to %s: %v\n", client.Addr.String(), err)
		serverMetrics.RecordError()
	} else {
		serverMetrics.MessageSent()
	}
}

// HandleClientDisconnect processes a client disconnection
func HandleClientDisconnect(client *Client, timeout bool) {
	addrStr := client.Addr.String()
	
	clientsMutex.Lock()
	defer clientsMutex.Unlock()
	
	if _, exists := clients[addrStr]; !exists || !client.Connected {
		// Client already disconnected or never fully connected
		return
	}
	
	// Log the disconnection
	reason := "left"
	if timeout {
		reason = "timed out"
	}
	
	fmt.Printf("[-] %s %s from %s\n", client.Name, reason, client.Addr.String())
	
	client.Connected = false
	serverMetrics.ClientDisconnected(timeout)
	
	// Remove client after a brief delay to allow for possible reconnection
	time.AfterFunc(30*time.Second, func() {
		clientsMutex.Lock()
		defer clientsMutex.Unlock()
		
		// Check if this client is still disconnected after the delay
		if storedClient, exists := clients[addrStr]; exists && !storedClient.Connected {
			delete(clients, addrStr)
		}
	})
	
	// Notify other clients
	msg := fmt.Sprintf("Server: %s has left the chat.", client.Name)
	for _, c := range clients {
		if c.Connected && c.Addr.String() != addrStr {
			packet := createMessagePacket(MessageTypeChat, c.ID, 0, msg)
			conn.WriteToUDP(packet, c.Addr)
			serverMetrics.MessageSent()
		}
	}
}

// sendDirectMessage sends a message directly to a specific client
func sendDirectMessage(conn *net.UDPConn, client *Client, message string) {
	packet := createMessagePacket(MessageTypeChat, client.ID, 0, message)
	_, err := conn.WriteToUDP(packet, client.Addr)
	if err != nil {
		fmt.Printf("Error sending direct message to %s: %v\n", client.Addr.String(), err)
		serverMetrics.RecordError()
	} else {
		serverMetrics.MessageSent()
	}
}

// broadcastMessage sends a message to all connected clients except the sender
func broadcastMessage(conn *net.UDPConn, sender *Client, message string) {
	clientsMutex.RLock()
	defer clientsMutex.RUnlock()
	
	senderAddr := ""
	if sender != nil {
		senderAddr = sender.Addr.String()
	}
	
	for addrStr, client := range clients {
		if client.Connected && addrStr != senderAddr {
			packet := createMessagePacket(MessageTypeChat, client.ID, 0, message)
			_, err := conn.WriteToUDP(packet, client.Addr)
			if err != nil {
				fmt.Printf("Error broadcasting to %s: %v\n", addrStr, err)
				serverMetrics.RecordError()
			} else {
				serverMetrics.MessageSent()
			}
		}
	}
}

// countConnectedClients returns the number of clients that are fully connected
func countConnectedClients() int {
	clientsMutex.RLock()
	defer clientsMutex.RUnlock()
	
	count := 0
	for _, client := range clients {
		if client.Connected {
			count++
		}
	}
	return count
}

// GetAllClients returns a slice of all clients (for metrics and heartbeats)
func GetAllClients() []*Client {
	clientsMutex.RLock()
	defer clientsMutex.RUnlock()
	
	result := make([]*Client, 0, len(clients))
	for _, client := range clients {
		result = append(result, client)
	}
	return result
}

// GetActiveUserCount returns the number of currently connected clients
func GetActiveUserCount() int {
	return countConnectedClients()
}

// createMessagePacket creates a binary packet for the UDP protocol
func createMessagePacket(msgType MessageType, clientID, seqNum uint32, content string) []byte {
	contentBytes := []byte(content)
	contentLen := len(contentBytes)
	
	// Ensure content fits in the protocol limit (255 bytes max for simplicity)
	if contentLen > 255 {
		contentLen = 255
		contentBytes = contentBytes[:255]
	}
	
	// Allocate buffer: Type(1) + ClientID(4) + SeqNum(4) + ContentLen(1) + Content(N)
	packet := make([]byte, 10+contentLen)
	
	// Fill header
	packet[0] = byte(msgType)
	binary.BigEndian.PutUint32(packet[1:5], clientID)
	binary.BigEndian.PutUint32(packet[5:9], seqNum)
	packet[9] = byte(contentLen)
	
	// Copy content if any
	if contentLen > 0 {
		copy(packet[10:], contentBytes)
	}
	
	return packet
}