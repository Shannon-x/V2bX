package json5

import (
	"bytes"
	"fmt"
	"io"
)

// DefaultMaxBytes is the safety ceiling enforced by prep when the caller
// does not supply a tighter bound via NewTrimNodeReaderLimit. 64 MiB is
// far above any sane config file but well below an OOM threshold.
const DefaultMaxBytes = int64(64 << 20)

type TrimNodeReader struct {
	r   io.Reader
	br  *bytes.Reader
	max int64 // 0 means "use DefaultMaxBytes"
}

func isNL(c byte) bool {
	return c == '\n' || c == '\r'
}

func isWS(c byte) bool {
	return c == ' ' || c == '\t' || isNL(c)
}

func consumeComment(s []byte, i int) int {
	if i < len(s) && s[i] == '/' {
		s[i-1] = ' '
		for ; i < len(s) && !isNL(s[i]); i += 1 {
			s[i] = ' '
		}
	}
	if i < len(s) && s[i] == '*' {
		s[i-1] = ' '
		s[i] = ' '
		for ; i < len(s); i += 1 {
			if s[i] != '*' {
				s[i] = ' '
			} else {
				s[i] = ' '
				i++
				if i < len(s) {
					if s[i] == '/' {
						s[i] = ' '
						break
					}
				}
			}
		}
	}
	return i
}

// W6 / audit #50 后半: enforce a size ceiling so a runaway local file (or
// a panel-controlled Include URL that somehow slipped past the caller's
// own limit) cannot OOM the process with a 1 GiB payload.
func prep(r io.Reader, max int64) (s []byte, err error) {
	if max <= 0 {
		max = DefaultMaxBytes
	}
	buf := &bytes.Buffer{}
	// LimitReader returns EOF at max+1 bytes; we read max+1 then check
	// whether the source actually ended (acceptable) or got truncated.
	n, err := io.Copy(buf, io.LimitReader(r, max+1))
	if err != nil {
		return nil, err
	}
	if n > max {
		return nil, fmt.Errorf("json5: input exceeds %d bytes (size cap)", max)
	}
	s = buf.Bytes()

	i := 0
	for i < len(s) {
		switch s[i] {
		case '"':
			i += 1
			for i < len(s) {
				if s[i] == '"' {
					i += 1
					break
				} else if s[i] == '\\' {
					i += 1
				}
				i += 1
			}
		case '/':
			i = consumeComment(s, i+1)
		case ',':
			j := i
			for {
				i += 1
				if i >= len(s) {
					break
				} else if s[i] == '}' || s[i] == ']' {
					s[j] = ' '
					break
				} else if s[i] == '/' {
					i = consumeComment(s, i+1)
				} else if !isWS(s[i]) {
					break
				}
			}
		default:
			i += 1
		}
	}
	return
}

// Read acts as a proxy for the underlying reader and cleans p
// of comments and trailing commas preceeding ] and }
// comments are delimitted by // up until the end the line
func (st *TrimNodeReader) Read(p []byte) (n int, err error) {
	if st.br == nil {
		var s []byte
		if s, err = prep(st.r, st.max); err != nil {
			return
		}
		st.br = bytes.NewReader(s)
	}
	return st.br.Read(p)
}

// NewTrimNodeReader returns an io.Reader acting as proxy to r. A
// DefaultMaxBytes safety cap is enforced on the underlying read; use
// NewTrimNodeReaderLimit for a tighter or looser bound.
func NewTrimNodeReader(r io.Reader) io.Reader {
	return &TrimNodeReader{r: r}
}

// NewTrimNodeReaderLimit is the size-capped variant. max <= 0 means
// "use DefaultMaxBytes"; pass an explicit value for paths where the
// caller knows the realistic upper bound (e.g. 8 MiB for Include URLs).
// W6 / audit #50.
func NewTrimNodeReaderLimit(r io.Reader, max int64) io.Reader {
	return &TrimNodeReader{r: r, max: max}
}
