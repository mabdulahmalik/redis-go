package main

import (
	"bufio"
	"errors"
	"io"
	"net"
	"testing"
	"time"
)

// startTestServer starts a server on a free port and returns a client connection.
// t.Cleanup closes everything it opens when the calling test finishes.
func startTestServer(t *testing.T) net.Conn {
	t.Helper()

	listener, err := Listen(":0")
	if err != nil {
		t.Fatalf("failed to start listener: %v", err)
	}
	t.Cleanup(func() { listener.Close() })

	go Serve(listener, NewStore())

	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("failed to dial server: %v", err)
	}
	t.Cleanup(func() { conn.Close() })

	// If the server ever fails to answer, fail the test after 2 seconds
	// instead of waiting forever.
	conn.SetReadDeadline(time.Now().Add(2 * time.Second))

	return conn
}

func TestServerParsesRESPCommand(t *testing.T) {
	conn := startTestServer(t)

	// SET greeting "hello world" — note the space inside the third argument.
	// A line-based parser would have split it; RESP keeps it whole.
	request := "*3\r\n$3\r\nSET\r\n$8\r\ngreeting\r\n$11\r\nhello world\r\n"
	if _, err := conn.Write([]byte(request)); err != nil {
		t.Fatalf("failed to write request: %v", err)
	}

	response, err := bufio.NewReader(conn).ReadString('\n')
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}

	want := "received: [\"SET\" \"greeting\" \"hello world\"]\n"
	if response != want {
		t.Errorf("got %q, want %q", response, want)
	}
}

func TestServerHandlesPipelinedCommands(t *testing.T) {
	conn := startTestServer(t)

	// Two complete commands sent in a single Write. The server must find
	// the boundary between them using RESP's framing alone.
	request := "*1\r\n$4\r\nPING\r\n*2\r\n$4\r\nECHO\r\n$2\r\nhi\r\n"
	if _, err := conn.Write([]byte(request)); err != nil {
		t.Fatalf("failed to write request: %v", err)
	}

	// One reader for both responses, for the reason the server keeps one
	// RESPReader per connection: a second reader misses bytes the first buffered.
	reader := bufio.NewReader(conn)

	wants := []string{
		"received: [\"PING\"]\n",
		"received: [\"ECHO\" \"hi\"]\n",
	}
	for _, want := range wants {
		got, err := reader.ReadString('\n')
		if err != nil {
			t.Fatalf("failed to read response: %v", err)
		}
		if got != want {
			t.Errorf("got %q, want %q", got, want)
		}
	}
}

func TestServerClosesConnectionOnProtocolError(t *testing.T) {
	conn := startTestServer(t)

	// Exactly one invalid byte, so the server has read everything before it
	// hangs up — unread bytes would give us "connection reset" instead of EOF.
	if _, err := conn.Write([]byte("!")); err != nil {
		t.Fatalf("failed to write request: %v", err)
	}

	// Reading from a connection the other side has closed returns io.EOF.
	_, err := bufio.NewReader(conn).ReadString('\n')
	if !errors.Is(err, io.EOF) {
		t.Errorf("expected io.EOF after the server closed the connection, got %v", err)
	}
}