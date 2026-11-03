package panel

import (
	"encoding/json/jsontext"
	"encoding/json/v2"
	"fmt"
	"io"

	"github.com/vmihailenco/msgpack/v5"
	"github.com/vmihailenco/msgpack/v5/msgpcode"
)

// Validate the complete envelope before publishing a snapshot or its ETag.
// A valid empty array is non-nil; nil is reserved for HTTP 304.
func decodeJSONUsers(r io.Reader, limit int) ([]UserInfo, error) {
	d := jsontext.NewDecoder(r)
	t, err := d.ReadToken()
	if err != nil {
		return nil, err
	}
	if t.Kind() != '{' {
		return nil, fmt.Errorf("expected user-list object")
	}
	var users []UserInfo
	for d.PeekKind() != '}' {
		key, err := d.ReadToken()
		if err != nil {
			return nil, err
		}
		if key.Kind() != '"' {
			return nil, fmt.Errorf("expected object member")
		}
		if key.String() != "users" {
			if err := d.SkipValue(); err != nil {
				return nil, err
			}
			continue
		}
		if users != nil {
			return nil, fmt.Errorf("duplicate users member")
		}
		t, err := d.ReadToken()
		if err != nil {
			return nil, err
		}
		if t.Kind() != '[' {
			return nil, fmt.Errorf("users must be an array")
		}
		users = make([]UserInfo, 0)
		for d.PeekKind() != ']' {
			if len(users) >= limit {
				return nil, fmt.Errorf("user count exceeds %d", limit)
			}
			if d.PeekKind() != '{' {
				return nil, fmt.Errorf("user must be an object")
			}
			var user UserInfo
			if err := json.UnmarshalDecode(d, &user); err != nil {
				return nil, err
			}
			users = append(users, user)
		}
		if _, err := d.ReadToken(); err != nil {
			return nil, err
		}
	}
	if _, err := d.ReadToken(); err != nil {
		return nil, err
	}
	if users == nil {
		return nil, fmt.Errorf("missing users array")
	}
	if _, err := d.ReadToken(); err != io.EOF {
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("trailing data after user list")
	}
	return users, nil
}

func decodeMsgpackUsers(r io.Reader, limit int) ([]UserInfo, error) {
	d := msgpack.NewDecoder(r)
	n, err := d.DecodeMapLen()
	if err != nil {
		return nil, err
	}
	if n < 0 {
		return nil, fmt.Errorf("expected user-list map")
	}
	var users []UserInfo
	for i := 0; i < n; i++ {
		key, err := d.DecodeString()
		if err != nil {
			return nil, err
		}
		if key != "users" {
			if err := d.Skip(); err != nil {
				return nil, err
			}
			continue
		}
		if users != nil {
			return nil, fmt.Errorf("duplicate users member")
		}
		count, err := d.DecodeArrayLen()
		if err != nil {
			return nil, err
		}
		if count < 0 {
			return nil, fmt.Errorf("users must be an array")
		}
		if count > limit {
			return nil, fmt.Errorf("user count exceeds %d", limit)
		}
		// Do not allocate an attacker-declared array before reading its items.
		users = make([]UserInfo, 0, min(count, 1024))
		for j := 0; j < count; j++ {
			code, err := d.PeekCode()
			if err != nil {
				return nil, err
			}
			if !msgpcode.IsFixedMap(code) && code != msgpcode.Map16 && code != msgpcode.Map32 {
				return nil, fmt.Errorf("user must be an object")
			}
			var user UserInfo
			if err := d.Decode(&user); err != nil {
				return nil, err
			}
			users = append(users, user)
		}
	}
	if users == nil {
		return nil, fmt.Errorf("missing users array")
	}
	if _, err := d.PeekCode(); err != io.EOF {
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("trailing data after user list")
	}
	return users, nil
}
