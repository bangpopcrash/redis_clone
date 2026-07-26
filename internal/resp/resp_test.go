package resp

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

func TestReaderDecode(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		want    Value
		wantErr error
	}{
		{
			name:  "simple string",
			input: "+OK\r\n",
			want:  NewSimpleString("OK"),
		},
		{
			name:  "empty simple string",
			input: "+\r\n",
			want:  NewSimpleString(""),
		},
		{
			name:  "error",
			input: "-ERR unknown command\r\n",
			want:  NewError("ERR unknown command"),
		},
		{
			name:  "positive integer",
			input: ":1000\r\n",
			want:  NewInteger(1000),
		},
		{
			name:  "negative integer",
			input: ":-1\r\n",
			want:  NewInteger(-1),
		},
		{
			name:  "bulk string",
			input: "$5\r\nhello\r\n",
			want:  NewBulkString("hello"),
		},
		{
			name:  "empty bulk string",
			input: "$0\r\n\r\n",
			want:  NewBulkString(""),
		},
		{
			name:  "null bulk string",
			input: "$-1\r\n",
			want:  NewNullBulkString(),
		},
		{
			name:  "bulk string containing CRLF",
			input: "$6\r\nfo\r\nbo\r\n",
			want:  NewBulkString("fo\r\nbo"),
		},
		{
			name:  "empty array",
			input: "*0\r\n",
			want:  NewArray([]Value{}),
		},
		{
			name:  "null array",
			input: "*-1\r\n",
			want:  NewNullArray(),
		},
		{
			name:  "array of bulk strings",
			input: "*2\r\n$3\r\nfoo\r\n$3\r\nbar\r\n",
			want: NewArray([]Value{
				NewBulkString("foo"),
				NewBulkString("bar"),
			}),
		},
		{
			name:  "nested array",
			input: "*2\r\n*1\r\n:1\r\n$3\r\nfoo\r\n",
			want: NewArray([]Value{
				NewArray([]Value{NewInteger(1)}),
				NewBulkString("foo"),
			}),
		},
		{
			name:    "unknown type byte",
			input:   "?garbage\r\n",
			wantErr: ErrInvalidSyntax,
		},
		{
			name:    "bad integer",
			input:   ":notanumber\r\n",
			wantErr: ErrInvalidSyntax,
		},
		{
			name:    "bulk string negative length other than -1",
			input:   "$-5\r\n",
			wantErr: ErrNegativeLength,
		},
		{
			name:    "bulk string missing trailing CRLF",
			input:   "$5\r\nhelloXX",
			wantErr: ErrInvalidSyntax,
		},
		{
			name:    "array negative length other than -1",
			input:   "*-5\r\n",
			wantErr: ErrNegativeLength,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := NewReader(strings.NewReader(tt.input))
			got, err := r.Read()

			if tt.wantErr != nil {
				if !errors.Is(err, tt.wantErr) {
					t.Fatalf("Read() error = %v, want error wrapping %v", err, tt.wantErr)
				}
				return
			}
			if err != nil {
				t.Fatalf("Read() unexpected error: %v", err)
			}
			if !Equal(got, tt.want) {
				t.Fatalf("Read() = %+v, want %+v", got, tt.want)
			}
		})
	}
}

func TestReaderEnforcesLimits(t *testing.T) {
	t.Run("bulk string exceeding MaxBulkLen is rejected before allocation", func(t *testing.T) {
		r := NewReaderWithLimits(strings.NewReader("$1000\r\n"), Limits{
			MaxBulkLen:  10,
			MaxArrayLen: DefaultLimits.MaxArrayLen,
			MaxLineLen:  DefaultLimits.MaxLineLen,
		})
		_, err := r.Read()
		if !errors.Is(err, ErrBulkTooLong) {
			t.Fatalf("Read() error = %v, want %v", err, ErrBulkTooLong)
		}
	})

	t.Run("array exceeding MaxArrayLen is rejected before allocation", func(t *testing.T) {
		r := NewReaderWithLimits(strings.NewReader("*1000\r\n"), Limits{
			MaxBulkLen:  DefaultLimits.MaxBulkLen,
			MaxArrayLen: 10,
			MaxLineLen:  DefaultLimits.MaxLineLen,
		})
		_, err := r.Read()
		if !errors.Is(err, ErrArrayTooLong) {
			t.Fatalf("Read() error = %v, want %v", err, ErrArrayTooLong)
		}
	})

	t.Run("line exceeding MaxLineLen is rejected", func(t *testing.T) {
		longLine := "+" + strings.Repeat("a", 100) + "\r\n"
		r := NewReaderWithLimits(strings.NewReader(longLine), Limits{
			MaxBulkLen:  DefaultLimits.MaxBulkLen,
			MaxArrayLen: DefaultLimits.MaxArrayLen,
			MaxLineLen:  10,
		})
		_, err := r.Read()
		if !errors.Is(err, ErrLineTooLong) {
			t.Fatalf("Read() error = %v, want %v", err, ErrLineTooLong)
		}
	})
}

func TestWriterEncode(t *testing.T) {
	tests := []struct {
		name  string
		value Value
		want  string
	}{
		{"simple string", NewSimpleString("OK"), "+OK\r\n"},
		{"error", NewError("ERR bad"), "-ERR bad\r\n"},
		{"integer", NewInteger(42), ":42\r\n"},
		{"negative integer", NewInteger(-1), ":-1\r\n"},
		{"bulk string", NewBulkString("hello"), "$5\r\nhello\r\n"},
		{"empty bulk string", NewBulkString(""), "$0\r\n\r\n"},
		{"null bulk string", NewNullBulkString(), "$-1\r\n"},
		{"null array", NewNullArray(), "*-1\r\n"},
		{"empty array", NewArray([]Value{}), "*0\r\n"},
		{
			"array of bulk strings",
			NewArray([]Value{NewBulkString("foo"), NewBulkString("bar")}),
			"*2\r\n$3\r\nfoo\r\n$3\r\nbar\r\n",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var buf bytes.Buffer
			w := NewWriter(&buf)
			if err := w.Write(tt.value); err != nil {
				t.Fatalf("Write() unexpected error: %v", err)
			}
			if got := buf.String(); got != tt.want {
				t.Fatalf("Write() = %q, want %q", got, tt.want)
			}
		})
	}
}

func TestRoundTrip(t *testing.T) {
	values := []Value{
		NewSimpleString("PONG"),
		NewError("ERR nope"),
		NewInteger(123456789),
		NewBulkString("round trip me"),
		NewNullBulkString(),
		NewNullArray(),
		NewArray([]Value{
			NewBulkString("SET"),
			NewBulkString("key"),
			NewBulkString("value"),
		}),
	}

	var buf bytes.Buffer
	w := NewWriter(&buf)
	for _, v := range values {
		if err := w.Write(v); err != nil {
			t.Fatalf("Write() unexpected error: %v", err)
		}
	}

	r := NewReader(&buf)
	for i, want := range values {
		got, err := r.Read()
		if err != nil {
			t.Fatalf("Read() #%d unexpected error: %v", i, err)
		}
		if !Equal(got, want) {
			t.Fatalf("Read() #%d = %+v, want %+v", i, got, want)
		}
	}
}
