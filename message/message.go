package message

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
)

const MaxSize = 1024 * 1024 // 1MB

// Read reads a length-prefixed message from the stream.
// Wire format: <length>:<message>
// Example: "13:hello, world!"
func Read(r *bufio.Reader) (string, error) {
	lengthStr, err := r.ReadString(':')
	if err != nil {
		return "", err
	}
	length, err := strconv.Atoi(lengthStr[:len(lengthStr)-1])
	if err != nil {
		return "", fmt.Errorf("invalid length prefix: %w", err)
	}
	if length > MaxSize {
		return "", fmt.Errorf("message too large: %d bytes", length)
	}
	buf := make([]byte, length)
	if _, err := io.ReadFull(r, buf); err != nil {
		return "", err
	}
	return string(buf), nil
}

// Write writes a length-prefixed message to the stream.
// Wire format: <length>:<message>
func Write(w io.Writer, msg string) error {
	header := strconv.Itoa(len(msg)) + ":"
	if _, err := w.Write([]byte(header)); err != nil {
		return err
	}
	_, err := w.Write([]byte(msg))
	return err
}
