package main

import (
	"bufio"
	"fmt"
	"io"
	"log"
	"net"
	"strings"
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

// handleConnection serves one client, line by line, until it disconnects or
// errors. Placeholder echo protocol; Part 7 swaps this body for RESP parsing.
func handleConnection(conn net.Conn, store *Store) {
	defer conn.Close()

	reader := bufio.NewReader(conn)

	for {
		line, err := reader.ReadString('\n')
		if err != nil {
			if err != io.EOF {
				log.Printf("read error from %s: %v", conn.RemoteAddr(), err)
			}
			return
		}

		line = strings.TrimRight(line, "\r\n")
		if line == "" {
			continue
		}

		response := fmt.Sprintf("you said: %s\n", line)
		if _, err := conn.Write([]byte(response)); err != nil {
			log.Printf("write error to %s: %v", conn.RemoteAddr(), err)
			return
		}
	}
}