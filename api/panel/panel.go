package panel

import (
	"errors"
	"fmt"
	"net"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/sirupsen/logrus"

	"github.com/InazumaV/V2bX/conf"
	"github.com/go-resty/resty/v2"
)

// Panel is the interface for different panel's api.

type Client struct {
	maxUsers         int
	client           *resty.Client
	APIHost          string
	APISendIP        string
	Token            string
	NodeType         string
	NodeId           int
	ApiVersion       int // 1 = V1 UniProxy, 2 = V2 flat API
	nodeEtag         string
	userEtag         string
	responseBodyHash string
	UserList         *UserListBody
	AliveMap         *AliveMap
}

func New(c *conf.ApiConfig) (*Client, error) {
	if c.MaxResponseBodyBytes < 0 || c.MaxUsers < 0 {
		return nil, fmt.Errorf("panel response and user limits cannot be negative")
	}
	maxBody := c.MaxResponseBodyBytes
	if maxBody == 0 {
		maxBody = 64 << 20
	}
	maxUsers := c.MaxUsers
	if maxUsers == 0 {
		maxUsers = 500000
	}
	var client *resty.Client
	if c.APISendIP != "" {
		client = resty.NewWithLocalAddr(&net.TCPAddr{
			IP: net.ParseIP(c.APISendIP),
		})
	} else {
		client = resty.New()
	}
	// W1.6 / audit #45 #49: clone the Go default Transport rather than
	// replacing it. The previous bare &http.Transport{} silently disabled
	// ForceAttemptHTTP2, Proxy (HTTPS_PROXY env), TLSHandshakeTimeout, and
	// ExpectContinueTimeout. Clone preserves all of those and we only override
	// the idle-pool tunables we actually want.
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.MaxIdleConnsPerHost = 10
	transport.IdleConnTimeout = 90 * time.Second
	client.SetTransport(&boundedTransport{Transport: transport, maxBytes: maxBody})
	client.SetRetryCount(0)
	if c.Timeout > 0 {
		client.SetTimeout(time.Duration(c.Timeout) * time.Second)
	} else {
		client.SetTimeout(30 * time.Second)
	}
	client.OnError(func(req *resty.Request, err error) {
		var v *resty.ResponseError
		if errors.As(err, &v) {
			// v.Response contains the last response from the server
			// v.Err contains the original error
			logrus.Error(v.Err)
		}
	})
	client.SetBaseURL(c.APIHost)
	// Check node type
	c.NodeType = strings.ToLower(c.NodeType)
	switch c.NodeType {
	case "v2ray":
		c.NodeType = "vmess"
	case
		"vmess",
		"trojan",
		"shadowsocks",
		"hysteria",
		"hysteria2",
		"tuic",
		"anytls",
		"vless":
	default:
		return nil, fmt.Errorf("unsupported Node type: %s", c.NodeType)
	}
	// set params
	client.SetQueryParams(map[string]string{
		"node_type": c.NodeType,
		"node_id":   strconv.Itoa(c.NodeID),
		"token":     c.Key,
	})
	apiVersion := c.ApiVersion
	if apiVersion == 0 {
		apiVersion = 1
	}
	return &Client{
		maxUsers:   maxUsers,
		client:     client,
		Token:      c.Key,
		APIHost:    c.APIHost,
		APISendIP:  c.APISendIP,
		NodeType:   c.NodeType,
		NodeId:     c.NodeID,
		ApiVersion: apiVersion,
		UserList:   &UserListBody{},
		AliveMap:   &AliveMap{},
	}, nil
}

func (c *Client) Close() {
	c.client.GetClient().CloseIdleConnections()
}
