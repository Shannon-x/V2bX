package panel

import (
	"fmt"
	"net/http"
)

// Bound both Resty's buffered responses and its streaming user-list path.
// The base transport has already applied automatic gzip decompression.
type boundedTransport struct {
	*http.Transport
	maxBytes int64
}

func (t *boundedTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	res, err := t.Transport.RoundTrip(req)
	if err != nil {
		return nil, err
	}
	if res.ContentLength > t.maxBytes {
		res.Body.Close()
		return nil, fmt.Errorf("panel response exceeds %d bytes", t.maxBytes)
	}
	res.Body = http.MaxBytesReader(nil, res.Body, t.maxBytes)
	return res, nil
}
