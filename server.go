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

// handleConnection serves one client, reading a complete RESP command at a time
// and replying. Replies are TEMPORARY echoes until real execution replaces them.
func handleConnection(conn net.Conn, store *Store) {
	defer conn.Close()

	// One reader and one writer per connection, created once and reused: each
	// holds buffered bytes belonging to this connection.
	reader := NewRESPReader(conn)
	writer := NewRESPWriter(conn)

	for {
		value, err := reader.Read()
		if err != nil {
			switch {
			case errors.Is(err, io.EOF):
				// The client disconnected cleanly between commands.
			case errors.Is(err, ErrProtocol):
				// The client sent bytes that break RESP's rules.
				sendProtocolError(conn, writer, err)
			default:
				// A truncated command or network failure: the connection
				// is unusable, so there is no one left to reply to.
				log.Printf("read error from %s: %v", conn.RemoteAddr(), err)
			}
			return
		}

		args, err := commandFromValue(value)
		if err != nil {
			sendProtocolError(conn, writer, err)
			return
		}

		if err := writer.Write(echoReply(args)); err != nil {
			log.Printf("could not encode reply for %s: %v", conn.RemoteAddr(), err)
			return
		}

		// Send this reply now. Flush is also where a network error from the
		// buffered writes above finally surfaces.
		if err := writer.Flush(); err != nil {
			log.Printf("write error to %s: %v", conn.RemoteAddr(), err)
			return
		}
	}
}

// sendProtocolError tells the client what was wrong with its input, as real
// Redis does, before the caller closes the connection.
func sendProtocolError(conn net.Conn, writer *RESPWriter, err error) {
	log.Printf("protocol error from %s: %v", conn.RemoteAddr(), err)

	// This connection closes whether or not the message lands, so Write and
	// Flush errors are ignored on purpose — "_ =" makes that choice visible.
	_ = writer.Write(ErrorValue("ERR " + err.Error()))
	_ = writer.Flush()
}

// echoReply is TEMPORARY: it sends the parsed command back as an array of bulk
// strings, so a real client shows what the server understood. Execution replaces it.
func echoReply(args []string) Value {
	items := make([]Value, 0, len(args))
	for _, arg := range args {
		items = append(items, BulkStringValue(arg))
	}
	return ArrayValue(items...)
}