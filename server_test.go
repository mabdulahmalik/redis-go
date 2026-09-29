package main

import (
	"bufio"
	"net"
	"testing"
)

func TestServerEchoesInput(t *testing.T) {
	store := NewStore()

	// ":0" asks the OS for any free port instead of a fixed one, so repeated
	// or parallel runs never fail with "port already in use".
	listener, err := Listen(":0")
	if err != nil {
		t.Fatalf("failed to start listener: %v", err)
	}
	defer listener.Close()

	go Serve(listener, store)

	conn, err := net.Dial("tcp", listener.Addr().String())
	if err != nil {
		t.Fatalf("failed to dial server: %v", err)
	}
	defer conn.Close()

	if _, err := conn.Write([]byte("hello\n")); err != nil {
		t.Fatalf("failed to write to connection: %v", err)
	}

	reader := bufio.NewReader(conn)
	response, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("failed to read response: %v", err)
	}

	want := "you said: hello\n"
	if response != want {
		t.Errorf("got response %q, want %q", response, want)
	}
}