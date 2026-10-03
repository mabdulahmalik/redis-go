package main

import (
	"errors"
	"fmt"
	"io"
	"log"
	"net"
)

// Listen opens a TCP listener on address (e.g. ":6380", meaning that port on
// every interface) without accepting yet, so tests can pass ":0" for a free port.
func Listen(address string) (net.Listener, error) {
	listener, err := net.Listen("tcp", address)
	if err != nil {
		return nil, fmt.Errorf("failed to listen on %s: %w", address, err)
	}
	return listener, nil
}

// Serve accepts forever, giving each connection its own goroutine so no client
// blocks another. It returns once Accept fails, normally from a closed listener.
func Serve(listener net.Listener, store *Store) {
	log.Printf("listening on %s", listener.Addr())

	for {
		conn, err := listener.Accept()
		if err != nil {
			log.Printf("accept loop stopping: %v", err)
			return
		}

		go handleConnection(conn, store)
	}
}

// RunServer opens the listener and serves on it, blocking until Serve returns.
// Tests call Listen and Serve separately to control the listener themselves.
func RunServer(address string, store *Store) error {
	listener, err := Listen(address)
	if err != nil {
		return err
	}
	defer listener.Close()

	Serve(listener, store)
	return nil
}

// handleConnection serves one client: read a complete RESP value, check its shape,
// reply. Reply is TEMPORARY text until a RESP encoder and real execution land.
func handleConnection(conn net.Conn, store *Store) {
	defer conn.Close()

	reader := NewRESPReader(conn)

	for {
		value, err := reader.Read()
		if err != nil {
			// io.EOF is a clean disconnect between commands: normal, not
			// logged. Truncation, protocol and network errors are. All close.
			if !errors.Is(err, io.EOF) {
				log.Printf("read error from %s: %v", conn.RemoteAddr(), err)
			}
			return
		}

		args, err := commandFromValue(value)
		if err != nil {
			log.Printf("bad command from %s: %v", conn.RemoteAddr(), err)
			return
		}

		// %q quotes each argument, so you can see where one ends and the next
		// begins even when an argument contains a space or a newline.
		response := fmt.Sprintf("received: %q\n", args)
		if _, err := conn.Write([]byte(response)); err != nil {
			log.Printf("write error to %s: %v", conn.RemoteAddr(), err)
			return
		}
	}
}