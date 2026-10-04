package main

import (
	"bytes"
	"errors"
	"io"
	"math"
	"reflect"
	"testing"
)

// encodeToString writes v with a RESPWriter into an in-memory buffer, returning
// exactly the bytes that would have gone over the network.
func encodeToString(t *testing.T, v Value) string {
	t.Helper()

	var buf bytes.Buffer
	w := NewRESPWriter(&buf)
	if err := w.Write(v); err != nil {
		t.Fatalf("unexpected error from Write: %v", err)
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("unexpected error from Flush: %v", err)
	}
	return buf.String()
}

func TestWriteProducesExactBytes(t *testing.T) {
	tests := []struct {
		name  string
		value Value
		want  string
	}{
		{"simple string", SimpleStringValue("OK"), "+OK\r\n"},
		{"error", ErrorValue("ERR unknown command"), "-ERR unknown command\r\n"},
		{"integer", IntegerValue(1000), ":1000\r\n"},
		{"negative integer", IntegerValue(-42), ":-42\r\n"},
		{"zero", IntegerValue(0), ":0\r\n"},
		{"bulk string", BulkStringValue("gopher"), "$6\r\ngopher\r\n"},
		{"empty bulk string", BulkStringValue(""), "$0\r\n\r\n"},
		{"bulk string containing CRLF", BulkStringValue("a\r\nb"), "$4\r\na\r\nb\r\n"},
		{"length counts bytes, not characters", BulkStringValue("héllo"), "$6\r\nhéllo\r\n"},
		{"null bulk string", NullBulkStringValue(), "$-1\r\n"},
		{"array", ArrayValue(BulkStringValue("a"), IntegerValue(1)), "*2\r\n$1\r\na\r\n:1\r\n"},
		{"empty array", ArrayValue(), "*0\r\n"},
		{"null array", NullArrayValue(), "*-1\r\n"},
		{"nested array", ArrayValue(ArrayValue(SimpleStringValue("hi"))), "*1\r\n*1\r\n+hi\r\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := encodeToString(t, tt.value)
			if got != tt.want {
				t.Errorf("got %q, want %q", got, tt.want)
			}
		})
	}
}

func TestWriteIsBufferedUntilFlush(t *testing.T) {
	var buf bytes.Buffer
	w := NewRESPWriter(&buf)

	if err := w.Write(SimpleStringValue("OK")); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	// The reply is still sitting in the writer's memory buffer.
	if buf.Len() != 0 {
		t.Errorf("expected nothing to reach the underlying writer before Flush, got %q", buf.String())
	}

	if err := w.Flush(); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if buf.String() != "+OK\r\n" {
		t.Errorf("got %q after Flush, want %q", buf.String(), "+OK\r\n")
	}
}

func TestHelpersReplaceLineBreaks(t *testing.T) {
	// A message containing user text with a line break must still be
	// sent as exactly one line.
	got := encodeToString(t, ErrorValue("ERR bad\r\ninput"))
	want := "-ERR bad  input\r\n"
	if got != want {
		t.Errorf("got %q, want %q", got, want)
	}
}

func TestWriteRejectsInvalidValues(t *testing.T) {
	tests := []struct {
		name  string
		value Value
	}{
		{"simple string with a line break", Value{Type: TypeSimpleString, Str: "fake\r\n+OK"}},
		{"error with a line break", Value{Type: TypeError, Str: "ERR a\nb"}},
		{"zero Value with no type", Value{}},
		{"invalid value inside an array", ArrayValue(
			BulkStringValue("fine"),
			Value{Type: TypeSimpleString, Str: "bad\nvalue"},
		)},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			w := NewRESPWriter(&buf)

			err := w.Write(tt.value)
			if !errors.Is(err, ErrInvalidValue) {
				t.Errorf("expected an ErrInvalidValue error, got %v", err)
			}

			// All-or-nothing: even the valid first element of the array
			// case must not have been written.
			if err := w.Flush(); err != nil {
				t.Fatalf("unexpected error from Flush: %v", err)
			}
			if buf.Len() != 0 {
				t.Errorf("expected nothing to be written, got %q", buf.String())
			}
		})
	}
}

func TestRoundTrip(t *testing.T) {
	values := []Value{
		SimpleStringValue("PONG"),
		ErrorValue("WRONGTYPE Operation against a key holding the wrong kind of value"),
		IntegerValue(0),
		IntegerValue(math.MaxInt64),
		IntegerValue(math.MinInt64),
		BulkStringValue("binary\x00safe\r\ndata"),
		NullBulkStringValue(),
		ArrayValue(BulkStringValue("GET"), BulkStringValue("name")),
		ArrayValue(),
		NullArrayValue(),
		ArrayValue(IntegerValue(1), ArrayValue(BulkStringValue("nested"))),
	}

	// Encode every value, one after another, into a single buffer...
	var buf bytes.Buffer
	w := NewRESPWriter(&buf)
	for _, v := range values {
		if err := w.Write(v); err != nil {
			t.Fatalf("unexpected error writing %+v: %v", v, err)
		}
	}
	if err := w.Flush(); err != nil {
		t.Fatalf("unexpected error from Flush: %v", err)
	}

	// ...then decode them all back out of the same buffer with RESPReader.
	// Each one must come back exactly as it went in.
	r := NewRESPReader(&buf)
	for _, want := range values {
		got, err := r.Read()
		if err != nil {
			t.Fatalf("unexpected error reading back %+v: %v", want, err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("round trip changed the value: got %+v, want %+v", got, want)
		}
	}

	if _, err := r.Read(); !errors.Is(err, io.EOF) {
		t.Errorf("expected io.EOF after the last value, got %v", err)
	}
}