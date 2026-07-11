package panel

import (
	"context"
	"fmt"
	"io"
	"strings"

	"encoding/json/jsontext"
	"encoding/json/v2"

	"github.com/vmihailenco/msgpack/v5"
)

type OnlineUser struct {
	UID int
	IP  string
}

type UserInfo struct {
	Id          int    `json:"id" msgpack:"id"`
	Uuid        string `json:"uuid" msgpack:"uuid"`
	SpeedLimit  int    `json:"speed_limit" msgpack:"speed_limit"`
	DeviceLimit int    `json:"device_limit" msgpack:"device_limit"`
}

type UserListBody struct {
	Users []UserInfo `json:"users" msgpack:"users"`
}

type AliveMap struct {
	Alive map[int]int `json:"alive"`
}

// GetUserList will pull user from v2board.
func (c *Client) GetUserList() ([]UserInfo, error) {
	return c.GetUserListCtx(context.Background())
}

// GetUserListCtx is the ctx-aware variant used by the task framework.
// W3.2 / W3.4 / audit #25 #44.
func (c *Client) GetUserListCtx(ctx context.Context) ([]UserInfo, error) {
	const path = "/api/v1/server/UniProxy/user"
	r, err := c.client.R().
		SetContext(ctx).
		SetHeader("If-None-Match", c.userEtag).
		SetHeader("X-Response-Format", "msgpack").
		SetDoNotParseResponse(true).
		Get(path)
	if r == nil || r.RawResponse == nil {
		return nil, fmt.Errorf("received nil response or raw response")
	}
	defer func() {
		io.Copy(io.Discard, r.RawResponse.Body)
		r.RawResponse.Body.Close()
	}()

	if r.StatusCode() == 304 {
		return nil, nil
	}

	if err = c.checkResponse(r, path, err); err != nil {
		return nil, err
	}
	userlist := &UserListBody{}
	if strings.Contains(r.Header().Get("Content-Type"), "application/x-msgpack") {
		decoder := msgpack.NewDecoder(r.RawResponse.Body)
		if err := decoder.Decode(userlist); err != nil {
			return nil, fmt.Errorf("decode user list error: %w", err)
		}
	} else {
		dec := jsontext.NewDecoder(r.RawResponse.Body)
		for {
			tok, err := dec.ReadToken()
			if err != nil {
				return nil, fmt.Errorf("decode user list error: %w", err)
			}
			if tok.Kind() == '"' && tok.String() == "users" {
				break
			}
		}
		tok, err := dec.ReadToken()
		if err != nil {
			return nil, fmt.Errorf("decode user list error: %w", err)
		}
		if tok.Kind() != '[' {
			return nil, fmt.Errorf(`decode user list error: expected "users" array`)
		}
		for dec.PeekKind() != ']' {
			val, err := dec.ReadValue()
			if err != nil {
				return nil, fmt.Errorf("decode user list error: read user object: %w", err)
			}
			var u UserInfo
			if err := json.Unmarshal(val, &u); err != nil {
				return nil, fmt.Errorf("decode user list error: unmarshal user error: %w", err)
			}
			userlist.Users = append(userlist.Users, u)
		}
	}
	c.userEtag = r.Header().Get("ETag")
	return userlist.Users, nil
}

// GetUserAlive will fetch the alive_ip count for users.
// Note: Xboard does not provide the /alivelist endpoint, so this will
// gracefully return an empty map. The device_limit feature requires
// panel support for this endpoint to function properly.
//
// W1.8 / audit #43: previously every failure path returned (emptyMap, nil),
// indistinguishable from "panel returned no alive users". The caller would
// then overwrite the live AliveList with empty, silently disabling device
// limiting on transient errors. Now we return a real error on network /
// transport failures; only HTTP-level "endpoint not implemented" (>=399)
// and decode failures keep the empty-but-no-error semantics so unsupported
// panels still work.
func (c *Client) GetUserAlive() (map[int]int, error) {
	return c.GetUserAliveCtx(context.Background())
}

// GetUserAliveCtx is the ctx-aware variant. W3.2 / W3.4.
func (c *Client) GetUserAliveCtx(ctx context.Context) (map[int]int, error) {
	c.AliveMap = &AliveMap{}
	const path = "/api/v1/server/UniProxy/alivelist"
	r, err := c.client.R().
		SetContext(ctx).
		ForceContentType("application/json").
		Get(path)
	if err != nil {
		// Transport / network failure — propagate so the caller keeps the
		// previous AliveList instead of nuking the device-limit state.
		return nil, fmt.Errorf("get user alive: %w", err)
	}
	if r == nil || r.RawResponse == nil {
		return nil, fmt.Errorf("get user alive: nil response")
	}
	defer r.RawResponse.Body.Close()
	// H-03: distinguish "endpoint not implemented" from real failures. Only
	// 404/405/501 mean the panel genuinely lacks the alivelist endpoint —
	// those keep the empty-but-no-error semantics so unsupported panels work.
	// Auth (401/403), rate-limit (429) and server errors (5xx) are NOT
	// "unsupported": returning an empty map for them would fail-open and
	// silently disable device limiting. Propagate them (and decode failures)
	// as errors so the caller preserves the previous AliveList snapshot.
	switch code := r.StatusCode(); {
	case code == 404 || code == 405 || code == 501:
		c.AliveMap.Alive = make(map[int]int)
		return c.AliveMap.Alive, nil
	case code >= 400:
		return nil, fmt.Errorf("get user alive: unexpected status %d", code)
	}
	if err := json.Unmarshal(r.Body(), c.AliveMap); err != nil {
		return nil, fmt.Errorf("get user alive: decode body: %w", err)
	}

	return c.AliveMap.Alive, nil
}

type UserTraffic struct {
	UID      int
	Upload   int64
	Download int64
}

// ReportUserTraffic reports the user traffic.
func (c *Client) ReportUserTraffic(userTraffic []UserTraffic) error {
	return c.ReportUserTrafficCtx(context.Background(), userTraffic)
}

// ReportUserTrafficCtx is the ctx-aware variant. W3.2 / W3.4.
func (c *Client) ReportUserTrafficCtx(ctx context.Context, userTraffic []UserTraffic) error {
	data := make(map[int][]int64, len(userTraffic))
	for i := range userTraffic {
		data[userTraffic[i].UID] = []int64{userTraffic[i].Upload, userTraffic[i].Download}
	}
	const path = "/api/v1/server/UniProxy/push"
	r, err := c.client.R().
		SetContext(ctx).
		SetBody(data).
		ForceContentType("application/json").
		Post(path)
	err = c.checkResponse(r, path, err)
	if err != nil {
		return err
	}
	return nil
}

func (c *Client) ReportNodeOnlineUsers(data *map[int][]string) error {
	return c.ReportNodeOnlineUsersCtx(context.Background(), data)
}

// ReportNodeOnlineUsersCtx is the ctx-aware variant. W3.2 / W3.4.
func (c *Client) ReportNodeOnlineUsersCtx(ctx context.Context, data *map[int][]string) error {
	const path = "/api/v1/server/UniProxy/alive"
	r, err := c.client.R().
		SetContext(ctx).
		SetBody(data).
		ForceContentType("application/json").
		Post(path)
	err = c.checkResponse(r, path, err)
	if err != nil {
		return err
	}
	return nil
}
