package main

import (
	"errors"
	"io"
	"reflect"
	"strings"
	"testing"
)

func TestReadValidValues(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  Value
	}{
		{"simple string", "+OK\r\n", Value{Type: TypeSimpleString, Str: "OK"}},
		{"error", "-ERR something broke\r\n", Value{Type: TypeError, Str: "ERR something broke"}},
		{"integer", ":1000\r\n", Value{Type: TypeInteger, Num: 1000}},
		{"negative integer", ":-42\r\n", Value{Type: TypeInteger, Num: -42}},
		{"bulk string", "$5\r\nhello\r\n", Value{Type: TypeBulkString, Str: "hello"}},
		{"empty bulk string", "$0\r\n\r\n", Value{Type: TypeBulkString, Str: ""}},
		{"bulk string containing CRLF", "$7\r\nhel\r\nlo\r\n", Value{Type: TypeBulkString, Str: "hel\r\nlo"}},
		{"null bulk string", "$-1\r\n", Value{Type: TypeBulkString, Null: true}},
		{"command array", "*2\r\n$3\r\nGET\r\n$4\r\nname\r\n", Value{Type: TypeArray, Items: []Value{
			{Type: TypeBulkString, Str: "GET"},
			{Type: TypeBulkString, Str: "name"},
		}}},
		{"empty array", "*0\r\n", Value{Type: TypeArray}},
		{"null array", "*-1\r\n", Value{Type: TypeArray, Null: true}},
		{"nested array", "*2\r\n:1\r\n*1\r\n+hi\r\n", Value{Type: TypeArray, Items: []Value{
			{Type: TypeInteger, Num: 1},
			{Type: TypeArray, Items: []Value{{Type: TypeSimpleString, Str: "hi"}}},
		}}},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			reader := NewRESPReader(strings.NewReader(tt.input))

			got, err := reader.Read()
			if err != nil {
				t.Fatalf("unexpected error: %v", err)
			}
			if !reflect.DeepEqual(got, tt.want) {
				t.Errorf("got %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestReadMultipleValuesFromOneStream(t *testing.T) {
	// Three values back to back, with nothing separating them except
	// RESP's own framing. The reader must find each boundary on its own.
	input := "+first\r\n:2\r\n$5\r\nthird\r\n"
	reader := NewRESPReader(strings.NewReader(input))

	wants := []Value{
		{Type: TypeSimpleString, Str: "first"},
		{Type: TypeInteger, Num: 2},
		{Type: TypeBulkString, Str: "third"},
	}

	for _, want := range wants {
		got, err := reader.Read()
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if !reflect.DeepEqual(got, want) {
			t.Errorf("got %+v, want %+v", got, want)
		}
	}

	// The stream is now used up exactly at a value boundary: a clean end.
	if _, err := reader.Read(); !errors.Is(err, io.EOF) {
		t.Errorf("expected io.EOF at the end of the stream, got %v", err)
	}
}

func TestReadProtocolErrors(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"unknown type byte", "!oops\r\n"},
		{"line ending missing carriage return", "+OK\n"},
		{"integer that is not a number", ":abc\r\n"},
		{"length that is not a number", "$abc\r\n"},
		{"negative length other than -1", "$-2\r\n"},
		{"bulk data longer than declared", "$3\r\nhello\r\n"},
		{"bulk string over the size limit", "$999999999999\r\n"},
		{"array over the size limit", "*999999999\r\n"},
		{"arrays nested too deeply", strings.Repeat("*1\r\n", 100)},
		{"line longer than the buffer", "+" + strings.Repeat("a", 5000) + "\r\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewRESPReader(strings.NewReader(tt.input)).Read()
			if !errors.Is(err, ErrProtocol) {
				t.Errorf("expected an ErrProtocol error, got %v", err)
			}
		})
	}
}

func TestReadTruncatedInput(t *testing.T) {
	tests := []struct {
		name  string
		input string
	}{
		{"simple string cut off before its line ending", "+OK"},
		{"bulk string cut off in its data", "$5\r\nhel"},
		{"bulk string missing its data entirely", "$5\r\n"},
		{"array missing its last element", "*2\r\n$3\r\nGET\r\n"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := NewRESPReader(strings.NewReader(tt.input)).Read()
			if !errors.Is(err, io.ErrUnexpectedEOF) {
				t.Errorf("expected io.ErrUnexpectedEOF, got %v", err)
			}
		})
	}
}

func TestCommandFromValue(t *testing.T) {
	valid := Value{Type: TypeArray, Items: []Value{
		{Type: TypeBulkString, Str: "SET"},
		{Type: TypeBulkString, Str: "name"},
		{Type: TypeBulkString, Str: "gopher"},
	}}

	got, err := commandFromValue(valid)
	if err != nil {
		t.Fatalf("unexpected error for a valid command: %v", err)
	}
	want := []string{"SET", "name", "gopher"}
	if !reflect.DeepEqual(got, want) {
		t.Errorf("got %v, want %v", got, want)
	}

	invalid := []struct {
		name  string
		value Value
	}{
		{"not an array", Value{Type: TypeSimpleString, Str: "PING"}},
		{"null array", Value{Type: TypeArray, Null: true}},
		{"empty array", Value{Type: TypeArray}},
		{"argument that is not a bulk string", Value{Type: TypeArray, Items: []Value{
			{Type: TypeInteger, Num: 1},
		}}},
		{"null bulk string argument", Value{Type: TypeArray, Items: []Value{
			{Type: TypeBulkString, Null: true},
		}}},
	}

	for _, tt := range invalid {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := commandFromValue(tt.value); !errors.Is(err, ErrProtocol) {
				t.Errorf("expected an ErrProtocol error, got %v", err)
			}
		})
	}
}