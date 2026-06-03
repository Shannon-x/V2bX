package panel

import (
	"fmt"
	"strings"

	"github.com/go-resty/resty/v2"
)

// Debug set the client debug for client
func (c *Client) Debug() {
	c.client.SetDebug(true)
}

// assembleURL joins the API host and path for error reporting.
// W1.5 / audit #59: path.Join collapses the "://" in "https://host" to "https:/host";
// concatenate manually instead.
func (c *Client) assembleURL(path string) string {
	return strings.TrimRight(c.APIHost, "/") + "/" + strings.TrimLeft(path, "/")
}
func (c *Client) checkResponse(res *resty.Response, path string, err error) error {
	if err != nil {
		return fmt.Errorf("request %s failed: %s", c.assembleURL(path), err)
	}
	if res.StatusCode() >= 400 {
		body := res.Body()
		return fmt.Errorf("request %s failed: %s", c.assembleURL(path), string(body))
	}
	return nil
}
