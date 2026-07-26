// Package resp encodes and decodes the Redis Serialization Protocol
// (RESP2). This package does not know about commands or storage. It only
// converts between wire bytes and Value.
package resp

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"strconv"
)

// Type identifies which RESP frame a Value holds.
type Type byte

const (
	SimpleString Type = '+'
	Error        Type = '-'
	Integer      Type = ':'
	BulkString   Type = '$'
	Array        Type = '*'
)

// Value is one RESP protocol value. Only the field that matches Type holds
// data. The other fields stay at zero.
type Value struct {
	Type  Type
	Str   string  // Holds data for SimpleString, Error, and BulkString.
	Int   int64   // Holds data for Integer.
	Array []Value // Holds data for Array.
	Null  bool    // True for a null BulkString ($-1\r\n) or null Array (*-1\r\n).
}

func NewSimpleString(s string) Value { return Value{Type: SimpleString, Str: s} }
func NewError(s string) Value        { return Value{Type: Error, Str: s} }
func NewInteger(i int64) Value       { return Value{Type: Integer, Int: i} }
func NewBulkString(s string) Value   { return Value{Type: BulkString, Str: s} }
func NewNullBulkString() Value       { return Value{Type: BulkString, Null: true} }
func NewArray(items []Value) Value   { return Value{Type: Array, Array: items} }
func NewNullArray() Value            { return Value{Type: Array, Null: true} }

// Equal reports whether a and b hold the same RESP value. Value contains a
// slice, so Go does not allow the == operator on it. Use Equal instead,
// in tests and in other code.
func Equal(a, b Value) bool {
	if a.Type != b.Type || a.Null != b.Null || a.Str != b.Str || a.Int != b.Int {
		return false
	}
	if len(a.Array) != len(b.Array) {
		return false
	}
	for i := range a.Array {
		if !Equal(a.Array[i], b.Array[i]) {
			return false
		}
	}
	return true
}

// Limits sets maximum sizes for incoming data. The decoder checks these
// limits before it reads or allocates memory for a frame. This stops a
// bad or hostile client from forcing the server to use too much memory.
type Limits struct {
	MaxBulkLen  int64 // Maximum declared length of one bulk string.
	MaxArrayLen int64 // Maximum declared number of items in an array.
	MaxLineLen  int   // Maximum length of a line-based frame header (simple string, error, or integer).
}

// DefaultLimits gives safe starting values. Real Redis uses similar
// values. These values are large enough for normal use. They are also
// small enough to stop one frame from using too much server memory.
var DefaultLimits = Limits{
	MaxBulkLen:  512 * 1024 * 1024, // 512MB. This matches the default proto-max-bulk-len value in Redis.
	MaxArrayLen: 1024 * 1024,
	MaxLineLen:  64 * 1024,
}

var (
	ErrInvalidSyntax  = errors.New("resp: invalid syntax")
	ErrLineTooLong    = errors.New("resp: line exceeds max length")
	ErrBulkTooLong    = errors.New("resp: bulk string exceeds max length")
	ErrArrayTooLong   = errors.New("resp: array exceeds max length")
	ErrNegativeLength = errors.New("resp: negative length not permitted here")
)

// Reader decodes RESP values from a stream.
type Reader struct {
	br     *bufio.Reader
	limits Limits
}

func NewReader(r io.Reader) *Reader {
	return &Reader{br: bufio.NewReader(r), limits: DefaultLimits}
}

func NewReaderWithLimits(r io.Reader, limits Limits) *Reader {
	return &Reader{br: bufio.NewReader(r), limits: limits}
}

// readLine reads one line that ends in CRLF. It removes the CRLF from the
// result. It checks the line against MaxLineLen as it reads, before it
// buffers more bytes than the limit allows.
func (r *Reader) readLine() (string, error) {
	var line []byte
	for {
		chunk, err := r.br.ReadSlice('\n')
		line = append(line, chunk...)
		if len(line) > r.limits.MaxLineLen {
			return "", ErrLineTooLong
		}
		if err == nil {
			break
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		return "", err
	}
	if len(line) < 2 || line[len(line)-2] != '\r' {
		return "", ErrInvalidSyntax
	}
	return string(line[:len(line)-2]), nil
}

// Read decodes the next Value from the stream. Read returns io.EOF only
// when the stream ends before Read reads any byte of a new frame.
func (r *Reader) Read() (Value, error) {
	typeByte, err := r.br.ReadByte()
	if err != nil {
		return Value{}, err
	}

	switch Type(typeByte) {
	case SimpleString:
		s, err := r.readLine()
		if err != nil {
			return Value{}, err
		}
		return NewSimpleString(s), nil

	case Error:
		s, err := r.readLine()
		if err != nil {
			return Value{}, err
		}
		return NewError(s), nil

	case Integer:
		s, err := r.readLine()
		if err != nil {
			return Value{}, err
		}
		n, err := strconv.ParseInt(s, 10, 64)
		if err != nil {
			return Value{}, fmt.Errorf("%w: bad integer %q", ErrInvalidSyntax, s)
		}
		return NewInteger(n), nil

	case BulkString:
		return r.readBulkString()

	case Array:
		return r.readArray()

	default:
		return Value{}, fmt.Errorf("%w: unknown type byte %q", ErrInvalidSyntax, typeByte)
	}
}

func (r *Reader) readBulkString() (Value, error) {
	lenStr, err := r.readLine()
	if err != nil {
		return Value{}, err
	}
	n, err := strconv.ParseInt(lenStr, 10, 64)
	if err != nil {
		return Value{}, fmt.Errorf("%w: bad bulk length %q", ErrInvalidSyntax, lenStr)
	}
	if n == -1 {
		return NewNullBulkString(), nil
	}
	if n < 0 {
		return Value{}, ErrNegativeLength
	}
	if n > r.limits.MaxBulkLen {
		return Value{}, ErrBulkTooLong
	}

	buf := make([]byte, n+2) // Add 2 bytes for the trailing CRLF.
	if _, err := io.ReadFull(r.br, buf); err != nil {
		return Value{}, err
	}
	if buf[n] != '\r' || buf[n+1] != '\n' {
		return Value{}, ErrInvalidSyntax
	}
	return NewBulkString(string(buf[:n])), nil
}

func (r *Reader) readArray() (Value, error) {
	countStr, err := r.readLine()
	if err != nil {
		return Value{}, err
	}
	n, err := strconv.ParseInt(countStr, 10, 64)
	if err != nil {
		return Value{}, fmt.Errorf("%w: bad array length %q", ErrInvalidSyntax, countStr)
	}
	if n == -1 {
		return NewNullArray(), nil
	}
	if n < 0 {
		return Value{}, ErrNegativeLength
	}
	if n > r.limits.MaxArrayLen {
		return Value{}, ErrArrayTooLong
	}

	items := make([]Value, 0, n)
	for i := int64(0); i < n; i++ {
		v, err := r.Read()
		if err != nil {
			return Value{}, err
		}
		items = append(items, v)
	}
	return NewArray(items), nil
}

// Writer encodes Values and writes them to a stream.
type Writer struct {
	bw *bufio.Writer
}

func NewWriter(w io.Writer) *Writer {
	return &Writer{bw: bufio.NewWriter(w)}
}

// Write encodes v. Write then sends the result to the stream.
func (w *Writer) Write(v Value) error {
	if err := w.encode(v); err != nil {
		return err
	}
	return w.bw.Flush()
}

func (w *Writer) encode(v Value) error {
	switch v.Type {
	case SimpleString:
		_, err := fmt.Fprintf(w.bw, "+%s\r\n", v.Str)
		return err

	case Error:
		_, err := fmt.Fprintf(w.bw, "-%s\r\n", v.Str)
		return err

	case Integer:
		_, err := fmt.Fprintf(w.bw, ":%d\r\n", v.Int)
		return err

	case BulkString:
		if v.Null {
			_, err := w.bw.WriteString("$-1\r\n")
			return err
		}
		_, err := fmt.Fprintf(w.bw, "$%d\r\n%s\r\n", len(v.Str), v.Str)
		return err

	case Array:
		if v.Null {
			_, err := w.bw.WriteString("*-1\r\n")
			return err
		}
		if _, err := fmt.Fprintf(w.bw, "*%d\r\n", len(v.Array)); err != nil {
			return err
		}
		for _, item := range v.Array {
			if err := w.encode(item); err != nil {
				return err
			}
		}
		return nil

	default:
		return fmt.Errorf("resp: cannot encode unknown type %q", v.Type)
	}
}
