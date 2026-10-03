package main

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
)

// Type identifies which kind of RESP value a Value holds. Each type is
// named after the single byte that begins it on the wire.
type Type byte

const (
	TypeSimpleString Type = '+'
	TypeError        Type = '-'
	TypeInteger      Type = ':'
	TypeBulkString   Type = '$'
	TypeArray        Type = '*'
)

// Safety limits: every RESP length is chosen by the sender, so we check each
// before acting. Header lines are capped by the bufio buffer, 4096 by default.
const (
	maxBulkLength   = 512 * 1024 * 1024 // 512 MB, the same default limit real Redis uses
	maxArrayLength  = 1024 * 1024       // about one million elements
	maxNestingDepth = 32                // arrays inside arrays, at most this deep
)

//    TypeSimpleString, TypeError, TypeBulkString       -> Str                                              
//    TypeInteger                                       -> Num                                             
//    TypeArray                                         -> Items                            
// 	Value is one parsed RESP value. Go has no sum type, so one struct covers all
// 	five: Type picks the live field including Str (+ - $), Num (:), Items (*), Null ($-1/*-1).
type Value struct {
	Type  Type
	Str   string
	Num   int64
	Items []Value
	Null  bool
}

// RESPReader reads RESP values one at a time from any byte stream (a connection,
// a string in tests). One per connection: its buffer may hold the next value.
type RESPReader struct {
	reader *bufio.Reader
}

// NewRESPReader wraps r in a buffered reader and returns a RESPReader.
func NewRESPReader(r io.Reader) *RESPReader {
	return &RESPReader{reader: bufio.NewReader(r)}
}

// Read reads exactly one complete RESP value. io.EOF means a clean end between
// values, io.ErrUnexpectedEOF a cut-off value, and invalid input wraps ErrProtocol.
func (r *RESPReader) Read() (Value, error) {
	return r.readValue(0)
}

// readValue reads one value of any type. depth counts how many arrays we
// are currently inside, so readArray can refuse absurdly deep nesting.
func (r *RESPReader) readValue(depth int) (Value, error) {
	typeByte, err := r.reader.ReadByte()
	if err != nil {
		// Nothing of this value is read yet, so io.EOF passes up unchanged: a
		// clean disconnect at top level, converted below if we are in an array.
		return Value{}, err
	}

	var value Value
	switch Type(typeByte) {
	case TypeSimpleString, TypeError:
		value, err = r.readLineValue(Type(typeByte))
	case TypeInteger:
		value, err = r.readInteger()
	case TypeBulkString:
		value, err = r.readBulkString()
	case TypeArray:
		value, err = r.readArray(depth)
	default:
		return Value{}, fmt.Errorf("%w: unknown type byte %q", ErrProtocol, typeByte)
	}

	// We already consumed this value's type byte, so running out of input
	// at this point means the value was cut off partway through.
	if errors.Is(err, io.EOF) {
		return Value{}, io.ErrUnexpectedEOF
	}
	return value, err
}

// readLine reads one CRLF-terminated line and returns it without the CRLF.
func (r *RESPReader) readLine() (string, error) {
	// ReadSlice never grows past the reader's buffer size, so a client
	// cannot make us hold an endless line in memory.
	line, err := r.reader.ReadSlice('\n')
	if err != nil {
		if errors.Is(err, bufio.ErrBufferFull) {
			return "", fmt.Errorf("%w: line longer than %d bytes", ErrProtocol, r.reader.Size())
		}
		return "", err
	}
	if len(line) < 2 || line[len(line)-2] != '\r' {
		return "", fmt.Errorf("%w: line is not terminated by CRLF", ErrProtocol)
	}

	// string(...) copies, and must: the ReadSlice result points into bufio's
	// internal buffer, which the next read overwrites.
	return string(line[:len(line)-2]), nil
}

// readLineValue handles simple strings (+) and errors (-). Both are just
// "the rest of the line," so they share one function.
func (r *RESPReader) readLineValue(t Type) (Value, error) {
	line, err := r.readLine()
	if err != nil {
		return Value{}, err
	}
	return Value{Type: t, Str: line}, nil
}

// readInteger handles integers (:), for example ":1000\r\n".
func (r *RESPReader) readInteger() (Value, error) {
	line, err := r.readLine()
	if err != nil {
		return Value{}, err
	}
	n, err := strconv.ParseInt(line, 10, 64)
	if err != nil {
		return Value{}, fmt.Errorf("%w: invalid integer %q", ErrProtocol, line)
	}
	return Value{Type: TypeInteger, Num: n}, nil
}

// readLength reads the length line that follows $ or *. It returns -1
// for RESP's "null" marker and rejects every other negative number.
func (r *RESPReader) readLength() (int, error) {
	line, err := r.readLine()
	if err != nil {
		return 0, err
	}
	n, err := strconv.Atoi(line)
	if err != nil {
		return 0, fmt.Errorf("%w: invalid length %q", ErrProtocol, line)
	}
	if n < -1 {
		return 0, fmt.Errorf("%w: invalid length %d", ErrProtocol, n)
	}
	return n, nil
}

// readBulkString handles bulk strings ($), e.g. "$5\r\nhello\r\n". The length comes
// first, so we read that many bytes blindly — which is why they may include \r\n.
func (r *RESPReader) readBulkString() (Value, error) {
	length, err := r.readLength()
	if err != nil {
		return Value{}, err
	}
	if length == -1 {
		return Value{Type: TypeBulkString, Null: true}, nil
	}
	if length > maxBulkLength {
		return Value{}, fmt.Errorf("%w: bulk string length %d exceeds limit", ErrProtocol, length)
	}

	// +2 so we also read the \r\n that must follow the data.
	buf := make([]byte, length+2)
	if _, err := io.ReadFull(r.reader, buf); err != nil {
		return Value{}, err
	}
	if buf[length] != '\r' || buf[length+1] != '\n' {
		return Value{}, fmt.Errorf("%w: bulk string not terminated by CRLF", ErrProtocol)
	}
	return Value{Type: TypeBulkString, Str: string(buf[:length])}, nil
}

// readArray handles arrays (*), e.g. "*2\r\n$3\r\nGET\r\n$4\r\nname\r\n". Each element is
// itself a complete RESP value, maybe another array, so we recurse into readValue.
func (r *RESPReader) readArray(depth int) (Value, error) {
	if depth >= maxNestingDepth {
		return Value{}, fmt.Errorf("%w: arrays nested more than %d deep", ErrProtocol, maxNestingDepth)
	}
	length, err := r.readLength()
	if err != nil {
		return Value{}, err
	}
	if length == -1 {
		return Value{Type: TypeArray, Null: true}, nil
	}
	if length > maxArrayLength {
		return Value{}, fmt.Errorf("%w: array length %d exceeds limit", ErrProtocol, length)
	}

	// No preallocation: the sender picks length, so the 10-byte "*1000000\r\n"
	// would reserve a million slots. append only grows for elements that arrive.
	var items []Value
	for i := 0; i < length; i++ {
		item, err := r.readValue(depth + 1)
		if err != nil {
			return Value{}, err
		}
		items = append(items, item)
	}
	return Value{Type: TypeArray, Items: items}, nil
}

// commandFromValue checks v has every real command's shape — a non-empty array
// of non-null bulk strings — and returns them as []string{"SET", "name", "gopher"}.
func commandFromValue(v Value) ([]string, error) {
	if v.Type != TypeArray || v.Null {
		return nil, fmt.Errorf("%w: a command must be an array", ErrProtocol)
	}
	if len(v.Items) == 0 {
		return nil, fmt.Errorf("%w: a command must not be empty", ErrProtocol)
	}

	// Preallocating is safe here, unlike in readArray: v.Items has already
	// been fully read, so its length describes data we actually received.
	args := make([]string, 0, len(v.Items))
	for _, item := range v.Items {
		if item.Type != TypeBulkString || item.Null {
			return nil, fmt.Errorf("%w: every command argument must be a bulk string", ErrProtocol)
		}
		args = append(args, item.Str)
	}
	return args, nil
}