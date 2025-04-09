// client.go
package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt" // Needed for io.EOF and ReadFull
	"io"
	"log"
	"net"
	"os"
	"strings"
)

const maxMessageSize uint32 = 1024 * 4

// Use the same constant as the server for consistency (optional but good)

// encode sends a message using the length-prefixed protocol.
// (This is basically the same as server's client.msg)
func encodeAndSend(conn net.Conn, msg string) error {
	msgBytes := []byte(msg)
	msgLen := uint32(len(msgBytes))

	if msgLen == 0 {
		return nil // Don't send empty messages
	}
	if msgLen > maxMessageSize {
		return fmt.Errorf("message too large: %d bytes (max %d)", msgLen, maxMessageSize)
	}

	buf := new(bytes.Buffer)

	// Write length prefix using binary.Write to ensure correct endianness
	err := binary.Write(buf, binary.BigEndian, msgLen)
	if err != nil {
		return fmt.Errorf("failed to encode message length: %w", err)
	}

	// Write message bytes to buffer
	_, err = buf.Write(msgBytes)
	if err != nil {
		return fmt.Errorf("failed to write message to buffer: %w", err)
	}

	// Send to connection - ensure we send the entire buffer
	n, err := conn.Write(buf.Bytes())
	if err != nil {
		return fmt.Errorf("failed to write message to connection: %w", err)
	}

	// Verify complete send
	if n != buf.Len() {
		log.Printf("WARNING: Short write. Sent %d of %d bytes", n, buf.Len())
	}

	return nil // Success
}

// readFromServer reads messages from the server connection and prints them.
func readFromServer(conn net.Conn) {
	log.Println("Reader: Goroutine started. Waiting for messages from server...")

	defer func() {
		log.Println("Reader: Exiting reader goroutine")
	}()

	for {
		// 1. Prepare buffer for the 4-byte length prefix
		lenBuf := make([]byte, 4)

		// 2. Read exactly 4 bytes for the length directly from the connection.
		_, err := io.ReadFull(conn, lenBuf) // Use conn directly
		if err != nil {
			if err == io.EOF {
				log.Println("Reader: Server closed the connection (EOF).")
			} else {
				// Don't log OpError "use of closed network connection" if we closed it intentionally
				// This check is a bit noisy, might refine later.
				if !strings.Contains(err.Error(), "use of closed network connection") {
					log.Printf("Reader: Error reading length prefix: %v", err)
				}
			}
			return // Exit goroutine on any error reading length
		}

		// 3. Decode the 4 bytes into a uint32.
		var msgLen uint32
		err = binary.Read(bytes.NewReader(lenBuf), binary.BigEndian, &msgLen)
		if err != nil {
			log.Printf("Reader: Error decoding message length: %v. Disconnecting.", err)
			return // Fatal error for this message stream
		}

		// 4. Validate the message length (using same constant as server is good practice).
		if msgLen == 0 {
			log.Println("Reader: Server sent message with zero length. Ignoring.")
			continue // Wait for the next message
		}
		if msgLen > maxMessageSize {
			log.Printf("Reader: Message length %d exceeds maximum size %d. Disconnecting.", msgLen, maxMessageSize)
			return
		}

		// 5. Prepare buffer for the actual message body.
		msgBuf := make([]byte, msgLen)

		// 6. Read exactly msgLen bytes for the message body directly from the connection.
		_, err = io.ReadFull(conn, msgBuf) // Use conn directly
		if err != nil {
			if err == io.EOF {
				log.Printf("Reader: Server closed connection unexpectedly after sending length %d (EOF).", msgLen)
			} else {
				log.Printf("Reader: Error reading message body: %v", err)
			}
			return // Exit goroutine on any error reading body
		}

		// 7. Convert message bytes to string.
		msgString := string(msgBuf)

		// 8. Print the received message to the console.
		// Using Println adds the newline automatically. Adding ">" prefix.
		msgString = strings.TrimSpace(msgString) // Trim whitespace
		if msgString == "" {
			log.Println("Reader: Received empty message. Ignoring.")
			continue // Skip empty messages
		}

		fmt.Print("> ")
		fmt.Println(msgString) // Print the message

	}
}

func main() {
	log.SetFlags(log.LstdFlags | log.Lshortfile)

	serverAddress := ":8080"
	log.Printf("Attempting to connect to %s...", serverAddress)

	// 1. Connect to the server
	conn, err := net.Dial("tcp", serverAddress)
	if err != nil {
		log.Fatalf("Failed to connect to server: %v", err)
	}
	log.Printf("Connection established to %s. Starting message reader...", serverAddress)
	// Ensure the connection is closed when main exits
	defer func() {
		log.Println("Closing connection.")
		conn.Close()
	}()

	// 2. Start a goroutine to read messages FROM the server
	go readFromServer(conn)

	// 3. Read input from the user (stdin) and send it TO the server (main loop)
	log.Println("Enter messages to send (Ctrl+C to exit):")
	scanner := bufio.NewScanner(os.Stdin) // Use scanner for simpler line reading

	for scanner.Scan() { // Loop reads lines from stdin until EOF (Ctrl+D) or error
		text := scanner.Text() // Get the line text
		text = strings.TrimSpace(text)

		if text == "" {
			continue // Skip empty lines
		}

		// Send the message using our protocol function
		err := encodeAndSend(conn, text)
		if err != nil {
			log.Printf("Error sending message: %v\n", err)
			// If we can't send, the connection is likely broken, exit the loop.
			break
		}

	}

	// Check if the scanner stopped due to an error
	if err := scanner.Err(); err != nil {
		log.Printf("Error reading from stdin: %v", err)
	}

	log.Println("Client exiting.")
	// The defer conn.Close() will run now.
}
