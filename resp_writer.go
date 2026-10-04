package main

import (
	"bufio"
	"fmt"
	"io"
	"strconv"
	"strings"
)

// ########################################################################################################
// Building values: One helper per RESP type, so handlers stay short and never hand-build an invalid Value.
// ########################################################################################################

// lineBreaks replaces carriage returns and line feeds with spaces. Built once
// at startup and shared: a strings.Replacer is safe for concurrent use.
var lineBreaks = strings.NewReplacer("\r", " ", "\n", " ")

// SimpleStringValue returns a simple string reply, such as "OK" or "PONG".
// Line breaks in s become spaces, because this type is one line on the wire.
func SimpleStringValue(s string) Value {
	return Value{Type: TypeSimpleString, Str: lineBreaks.Replace(s)}
}

// ErrorValue returns an error reply. By Redis convention msg starts with an
// uppercase code, e.g. "ERR wrong number of arguments"; line breaks become spaces.
func ErrorValue(msg string) Value {
	return Value{Type: TypeError, Str: lineBreaks.Replace(msg)}
}

// IntegerValue returns an integer reply.
func IntegerValue(n int64) Value {
	return Value{Type: TypeInteger, Num: n}
}

// BulkStringValue returns a bulk string reply. s may contain any bytes at
// all, which is why user data should always be sent as a bulk string.
func BulkStringValue(s string) Value {
	return Value{Type: TypeBulkString, Str: s}
}

// NullBulkStringValue returns the null bulk string ($-1), Redis's "there is no
// value" — what GET replies with for a key that does not exist.
func NullBulkStringValue() Value {
	return Value{Type: TypeBulkString, Null: true}
}

// ArrayValue returns an array reply containing items, in order.
// Calling it with no arguments gives an empty array (*0\r\n).
func ArrayValue(items ...Value) Value {
	return Value{Type: TypeArray, Items: items}
}

// NullArrayValue returns the null array (*-1).
func NullArrayValue() Value {
	return Value{Type: TypeArray, Null: true}
}

// ########################################################################################################
// Writing values: turns a Value back into RESP bytes, validating it first and buffering until Flush.
// ########################################################################################################

// RESPWriter encodes Values as RESP onto any io.Writer (a connection, a
// bytes.Buffer in tests). Writes collect in memory until Flush sends them.
type RESPWriter struct {
	writer  *bufio.Writer
	scratch []byte // reused for turning numbers into text
}

// NewRESPWriter wraps w in a buffered writer and returns a RESPWriter.
func NewRESPWriter(w io.Writer) *RESPWriter {
	return &RESPWriter{writer: bufio.NewWriter(w)}
}

// Write buffers the RESP encoding of v; nothing is sent until Flush. All-or-
// nothing: a half-written value would corrupt every reply after it.
func (w *RESPWriter) Write(v Value) error {
	if err := validateValue(v); err != nil {
		return err
	}
	w.encode(v)
	return nil
}

// Flush sends everything buffered. bufio.Writer remembers its first error, so
// Flush also surfaces failures from earlier writes — always check it.
func (w *RESPWriter) Flush() error {
	return w.writer.Flush()
}

// validateValue reports whether v can be encoded as valid RESP.
func validateValue(v Value) error {
	switch v.Type {
	case TypeSimpleString, TypeError:
		if strings.ContainsAny(v.Str, "\r\n") {
			return fmt.Errorf("%w: %q contains a line break", ErrInvalidValue, v.Str)
		}
	case TypeInteger, TypeBulkString:
		// Every int64 and every sequence of bytes can be encoded.
	case TypeArray:
		if !v.Null {
			for _, item := range v.Items {
				if err := validateValue(item); err != nil {
					return err
				}
			}
		}
	default:
		return fmt.Errorf("%w: unknown type byte %q", ErrInvalidValue, byte(v.Type))
	}
	return nil
}

// encode writes v into the buffer, assuming validateValue(v) passed. Individual
// write errors go unchecked on purpose: bufio keeps the first and Flush reports it.
func (w *RESPWriter) encode(v Value) {
	switch v.Type {
	case TypeSimpleString, TypeError:
		w.writer.WriteByte(byte(v.Type))
		w.writer.WriteString(v.Str)
		w.writer.WriteString("\r\n")

	case TypeInteger:
		w.writePrefixed(TypeInteger, v.Num)

	case TypeBulkString:
		if v.Null {
			w.writePrefixed(TypeBulkString, -1)
			return
		}
		w.writePrefixed(TypeBulkString, int64(len(v.Str)))
		w.writer.WriteString(v.Str)
		w.writer.WriteString("\r\n")

	case TypeArray:
		if v.Null {
			w.writePrefixed(TypeArray, -1)
			return
		}
		w.writePrefixed(TypeArray, int64(len(v.Items)))
		for _, item := range v.Items {
			w.encode(item)
		}
	}
}

// writePrefixed writes a type byte, a decimal number, and CRLF — the one shape
// behind integers (":42\r\n") and length headers ("$6\r\n", "*3\r\n").
func (w *RESPWriter) writePrefixed(t Type, n int64) {
	w.writer.WriteByte(byte(t))
	w.scratch = strconv.AppendInt(w.scratch[:0], n, 10)
	w.writer.Write(w.scratch)
	w.writer.WriteString("\r\n")
}