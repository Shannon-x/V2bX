package xray

import (
	"encoding/base64"
	"strings"

	"github.com/InazumaV/V2bX/api/panel"
	"github.com/InazumaV/V2bX/common/format"
	"github.com/sirupsen/logrus"
	"github.com/xtls/xray-core/common/protocol"
	"github.com/xtls/xray-core/common/serial"
	"github.com/xtls/xray-core/proxy/shadowsocks"
	"github.com/xtls/xray-core/proxy/shadowsocks_2022"
)

func buildSSUsers(tag string, userInfo []panel.UserInfo, cypher string, serverKey string) (users []*protocol.User) {
	// W1.4 / audit #36: filter out users whose Uuid is too short for the
	// SS2022 key length, otherwise buildSSUser would slice-panic and abort
	// the entire AddUsers batch, leaving the inbound half-initialised.
	users = make([]*protocol.User, 0, len(userInfo))
	for i := range userInfo {
		u := buildSSUser(tag, &userInfo[i], cypher, serverKey)
		if u == nil {
			continue
		}
		users = append(users, u)
	}
	return users
}

func buildSSUser(tag string, userInfo *panel.UserInfo, cypher string, serverKey string) (user *protocol.User) {
	if serverKey == "" {
		ssAccount := &shadowsocks.Account{
			Password:   userInfo.Uuid,
			CipherType: getCipherFromString(cypher),
		}
		return &protocol.User{
			Level:   0,
			Email:   format.UserTag(tag, userInfo.Uuid),
			Account: serial.ToTypedMessage(ssAccount),
		}
	} else {
		keyLength := ss2022KeyLength(cypher)
		// W1.4 / audit #36: guard against panel-supplied short UUIDs.
		if len(userInfo.Uuid) < keyLength {
			logrus.WithFields(logrus.Fields{
				"tag":            tag,
				"uuid":           userInfo.Uuid,
				"cypher":         cypher,
				"required_bytes": keyLength,
				"actual_bytes":   len(userInfo.Uuid),
			}).Warn("Shadowsocks 2022 user UUID shorter than required key length, skipping user")
			return nil
		}
		ssAccount := &shadowsocks_2022.Account{
			Key: base64.StdEncoding.EncodeToString([]byte(userInfo.Uuid[:keyLength])),
		}
		return &protocol.User{
			Level:   0,
			Email:   format.UserTag(tag, userInfo.Uuid),
			Account: serial.ToTypedMessage(ssAccount),
		}
	}
}

// ss2022KeyLength returns the PSK length in bytes for a Shadowsocks 2022
// cipher: 16 for 2022-blake3-aes-128-gcm, 32 for the other 2022 ciphers.
func ss2022KeyLength(cipher string) int {
	if strings.EqualFold(cipher, "2022-blake3-aes-128-gcm") {
		return 16
	}
	return 32
}

func getCipherFromString(c string) shadowsocks.CipherType {
	switch strings.ToLower(c) {
	case "aes-128-gcm", "aead_aes_128_gcm":
		return shadowsocks.CipherType_AES_128_GCM
	case "aes-256-gcm", "aead_aes_256_gcm":
		return shadowsocks.CipherType_AES_256_GCM
	case "chacha20-poly1305", "aead_chacha20_poly1305", "chacha20-ietf-poly1305":
		return shadowsocks.CipherType_CHACHA20_POLY1305
	// Upstream xray-core removed CipherType_NONE (plaintext SS); "none"/"plain"
	// now falls through to UNKNOWN. Plain shadowsocks is unencrypted and
	// deprecated — real nodes use an AEAD cipher or shadowsocks-2022.
	default:
		return shadowsocks.CipherType_UNKNOWN
	}
}
