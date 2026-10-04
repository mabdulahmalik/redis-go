package main

import (
	"errors"
	"io"
	"net"
	"reflect"
	"strings"
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

func TestServerEchoesCommandAsRESPArray(t *testing.T) {
	conn := startTestServer(t)

	// SET greeting "hello world" — note the space inside the third argument.
	// A line-based parser would have split it; RESP keeps it whole.
	request := "*3\r\n$3\r\nSET\r\n$8\r\ngreeting\r\n$11\r\nhello world\r\n"
	if _, err := conn.Write([]byte(request)); err != nil {
		t.Fatalf("failed to write request: %v", err)
	}

	reply, err := NewRESPReader(conn).Read()
	if err != nil {
		t.Fatalf("failed to read reply: %v", err)
	}

	want := ArrayValue(
		BulkStringValue("SET"),
		BulkStringValue("greeting"),
		BulkStringValue("hello world"),
	)
	if !reflect.DeepEqual(reply, want) {
		t.Errorf("got %+v, want %+v", reply, want)
	}
}

func TestServerHandlesPipelinedCommands(t *testing.T) {
	conn := startTestServer(t)

	// Two complete commands sent in a single Write.
	request := "*1\r\n$4\r\nPING\r\n*2\r\n$4\r\nECHO\r\n$2\r\nhi\r\n"
	if _, err := conn.Write([]byte(request)); err != nil {
		t.Fatalf("failed to write request: %v", err)
	}

	// One reader for both replies, so no buffered bytes are lost.
	reader := NewRESPReader(conn)

	wants := []Value{
		ArrayValue(BulkStringValue("PING")),
		ArrayValue(BulkStringValue("ECHO"), BulkStringValue("hi")),
	}
	for _, want := range wants {
		got, err := reader.Read()
		if err != nil {
			t.Fatalf("failed to read reply: %v", err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %+v, want %+v", got, want)
		}
	}
}

// expectErrorThenClose reads one reply, checks it is an ERR error reply, then
// checks that the server closed the connection.
func expectErrorThenClose(t *testing.T, conn net.Conn) {
	t.Helper()

	reader := NewRESPReader(conn)

	reply, err := reader.Read()
	if err != nil {
		t.Fatalf("failed to read reply: %v", err)
	}
	if reply.Type != TypeError || !strings.HasPrefix(reply.Str, "ERR ") {
		t.Errorf("expected an error reply starting with \"ERR \", got %+v", reply)
	}

	if _, err := reader.Read(); !errors.Is(err, io.EOF) {
		t.Errorf("expected the server to close the connection, got %v", err)
	}
}

func TestServerRepliesWithErrorOnProtocolError(t *testing.T) {
	conn := startTestServer(t)

	// A single invalid byte, so the server has read everything we sent before
	// it hangs up — unread bytes would give us "connection reset" instead of EOF.
	if _, err := conn.Write([]byte("!")); err != nil {
		t.Fatalf("failed to write request: %v", err)
	}

	expectErrorThenClose(t, conn)
}

func TestServerRejectsCommandWithWrongShape(t *testing.T) {
	conn := startTestServer(t)

	// Valid RESP, invalid command: the element is an integer, not a bulk string.
	// The server reads all of it to find out, so no bytes are left unread.
	if _, err := conn.Write([]byte("*1\r\n:1\r\n")); err != nil {
		t.Fatalf("failed to write request: %v", err)
	}

	expectErrorThenClose(t, conn)
}