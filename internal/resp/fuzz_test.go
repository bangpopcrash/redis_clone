package resp

import (
	"errors"
	"io"
	"strings"
	"testing"
)

// FuzzDecode sends random byte data to the RESP decoder. The decoder reads
// data from a client we do not trust. The decoder must never panic. The
// decoder must also check a declared length against Limits before it
// allocates memory of that size.
func FuzzDecode(f *testing.F) {
	seeds := []string{
		"+OK\r\n",
		"-ERR bad\r\n",
		":12345\r\n",
		"$5\r\nhello\r\n",
		"$-1\r\n",
		"$0\r\n\r\n",
		"*2\r\n$3\r\nfoo\r\n$3\r\nbar\r\n",
		"*-1\r\n",
		"*0\r\n",
		"$99999999999999999999\r\n",
		"*99999999999999999999\r\n",
		"*1\r\n*1\r\n*1\r\n:1\r\n",
		"",
		"garbage",
	}
	for _, s := range seeds {
		f.Add(s)
	}

	f.Fuzz(func(t *testing.T, input string) {
		r := NewReader(strings.NewReader(input))
		_, err := r.Read()
		if err != nil && !errors.Is(err, io.EOF) {
			// A decode error is a valid result. A panic is not valid. The
			// Go test runner catches a panic on its own. This function
			// passes the test if it reaches this line.
			return
		}
	})
}
