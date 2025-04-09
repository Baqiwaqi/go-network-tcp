package main

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
	"time"
)

const maxMessageSize uint32 = 1024 * 4

type client struct {
	conn          net.Conn
	name          string
	serverMessage chan<- message
	disconnect    chan<- net.Addr // Add disconnection channel
}

func (c *client) readInput() {
	defer func() {
		log.Printf("Closing connection %s\n", c.conn.RemoteAddr().String())
		// Notify server this client is disconnecting
		c.disconnect <- c.conn.RemoteAddr()
		c.conn.Close()
	}()

	for {
		// 1. Read the 4-byte length prefix
		lenBuf := make([]byte, 4)
		_, err := io.ReadFull(c.conn, lenBuf)
		if err != nil {
			if err == io.EOF {
				log.Printf("Client %s (%s) closed while reading length of buffer\n", c.name, c.conn.RemoteAddr().String())
			} else {
				log.Printf("Error reading length from %s (%s): %v\n", c.name, c.conn.RemoteAddr().String(), err)
			}
			return
		}

		// Log the raw bytes for debugging
		log.Printf("Server received length bytes: %v from %s", lenBuf, c.name)

		// 2. Decode the length prefix
		var msgLen uint32
		err = binary.Read(bytes.NewReader(lenBuf), binary.BigEndian, &msgLen)
		if err != nil {
			log.Printf("Error decoding message length from %s: %s\n", c.name, err)
			return
		}

		log.Printf("Server decoded message length: %d from %s", msgLen, c.name)

		// 3. Validate the message length
		if msgLen == 0 {
			log.Printf("Client %s (%s) sent message with zero length. Ignoring.\n", c.name, c.conn.RemoteAddr().String())
			continue
		}
		if msgLen > maxMessageSize {
			log.Printf("Client %s (%s) message length %d exceeds limit %d. Disconnecting.\n", c.name, c.conn.RemoteAddr().String(), msgLen, maxMessageSize)
			return
		}

		// 4. Read the message body
		msgBuf := make([]byte, msgLen)
		_, connErr := io.ReadFull(c.conn, msgBuf)
		if connErr != nil {
			if connErr == io.EOF {
				log.Printf("Client %s (%s) closed while reading message body\n", c.name, c.conn.RemoteAddr().String())
			} else {
				log.Printf("Error reading message body from %s (%s): %v\n", c.name, c.conn.RemoteAddr().String(), connErr)
			}
			return
		}

		// 5. Process the message
		msgString := string(msgBuf)
		msgString = strings.TrimSpace(msgString)

		log.Printf("Server received message: '%s' from %s", msgString, c.name)

		// Send to server channel for broadcasting
		c.serverMessage <- message{
			client: c,
			msg:    msgString,
		}
	}
}

// msg sends a message to the client using the length-prefixed protocol.
func (c *client) msg(msg string) {
	msgBytes := []byte(msg)
	msgLen := uint32(len(msgBytes))

	if msgLen == 0 {
		log.Printf("Skipping send of zero-length message to %s", c.name)
		return
	}
	if msgLen > maxMessageSize {
		log.Printf("ERROR: Trying to send message of size %d to %s, which exceeds max %d", msgLen, c.name, maxMessageSize)
		return
	}

	buf := new(bytes.Buffer)
	err := binary.Write(buf, binary.BigEndian, &msgLen)
	if err != nil {
		log.Printf("Error encoding message length for client %s (%s): %v", c.name, c.conn.RemoteAddr().String(), err)
		return
	}

	_, err = buf.Write(msgBytes)
	if err != nil {
		log.Printf("Error writing message bytes to buffer for client %s (%s): %v", c.name, c.conn.RemoteAddr().String(), err)
		return
	}

	n, err := c.conn.Write(buf.Bytes())
	if err != nil {
		log.Printf("Error writing message to client %s (%s): %v", c.name, c.conn.RemoteAddr().String(), err)

	} else {
		if n != buf.Len() {
			log.Printf("WARN: Short write sending to %s (%s). Wrote %d bytes, expected %d", c.name, c.conn.RemoteAddr().String(), n, buf.Len())
		}
	}

}

type message struct {
	client *client
	msg    string
}

type server struct {
	members    map[net.Addr]*client
	messages   chan message
	disconnect chan net.Addr // Channel to handle client disconnection
}

func (s *server) run() {
	for {
		select {
		case msg := <-s.messages:
			s.msg(msg.client, msg.msg)
		case addr := <-s.disconnect:
			// Handle client disconnection
			if client, ok := s.members[addr]; ok {
				s.broadcast(client, fmt.Sprintf("%s left the room", client.name))
				delete(s.members, addr)
			}
		}
	}
}

func (s *server) newClient(conn net.Conn) *client {
	return &client{
		conn:          conn,
		name:          fmt.Sprintf("user%d", time.Now().UnixNano()%10000),
		serverMessage: s.messages, // Give the client access to the server channel
		disconnect:    s.disconnect,
	}
}

func (s *server) msg(c *client, msg string) {
	// Send the message directly to all clients
	chatMsg := fmt.Sprintf("%s: %s", c.name, msg)
	s.broadcast(c, chatMsg)
}

func (s *server) broadcast(sender *client, msg string) {
	log.Printf("Broadcasting: '%s' (originated from %s)", msg, sender.name) // Verbose Log
	count := 0
	for addr, m := range s.members {
		// Don't send back to sender
		if sender.conn.RemoteAddr() != addr {
			m.msg(msg) // Use the client's msg method
			count++
		}
	}
	if count > 0 {
		log.Printf("Broadcast sent to %d clients.", count) // Verbose Log
	}
}

func newServer() *server {
	return &server{
		members:    make(map[net.Addr]*client),
		messages:   make(chan message),
		disconnect: make(chan net.Addr),
	}
}

func main() {
	// Initialize a new server instance
	s := newServer()
	go s.run()

	// Set log flags to include file and line number
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	ln, err := net.Listen("tcp", ":8080")
	if err != nil {
		log.Fatalf("unable to start server: %s", err.Error())
	}

	log.Printf("Server started and listening on port 8080")
	defer ln.Close()

	for {
		conn, err := ln.Accept()
		if err != nil {
			log.Printf("unable to accept connections: %s", err.Error())
			// This should not be Fatal as it would terminate the server
			continue
		}

		c := s.newClient(conn)
		s.members[conn.RemoteAddr()] = c

		s.broadcast(c, fmt.Sprintf("%s joined the room", c.name))

		// Log the client address
		println("Client connected:", conn.RemoteAddr().String())

		go c.readInput()
	}

}
