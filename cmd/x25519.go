package cmd

import (
	"crypto/rand"
	"encoding/base64"
	"fmt"
	"strings"

	"github.com/InazumaV/V2bX/common/crypt"

	"github.com/spf13/cobra"
	"golang.org/x/crypto/curve25519"
)

var x25519Command = cobra.Command{
	Use:   "x25519",
	Short: "Generate key pair for x25519 key exchange",
	Run: func(cmd *cobra.Command, args []string) {
		executeX25519()
	},
}

func init() {
	command.AddCommand(&x25519Command)
}

func executeX25519() {
	var output string
	var err error
	defer func() {
		fmt.Println(output)
	}()
	var privateKey []byte
	var publicKey []byte
	var yes, key string
	// W4.5 / audit #37: default to a cryptographically random private key.
	// The previous default ("Y") derived the key from (node_id || type ||
	// token) — two nodes sharing those inputs got the SAME private key, and
	// any token leak let an attacker reconstruct private keys offline,
	// breaking Reality TLS handshake secrecy. We keep the derivation mode
	// available behind an explicit opt-in for users who need reproducible
	// keys, but warn loudly.
	fmt.Println("是否基于节点信息派生密钥? (y / N, 默认 N = 随机生成 — 推荐)")
	fmt.Println("  ⚠️  派生模式安全性较弱: 任何获得 (node_id, node_type, token) 三元组的人都可重现私钥.")
	fmt.Println("      仅在确实需要可复现密钥时选 y, 并请妥善保护 token.")
	fmt.Scan(&yes)
	if strings.ToLower(yes) == "y" {
		fmt.Println("⚠️  正在使用派生模式 — 请确保上游 token 妥善保管!")
		var temp string
		fmt.Println("请输入节点id:")
		fmt.Scan(&temp)
		key = temp
		fmt.Println("请输入节点类型:")
		fmt.Scan(&temp)
		key += strings.ToLower(temp)
		fmt.Println("请输入Token:")
		fmt.Scan(&temp)
		key += temp
		privateKey = crypt.GenX25519Private([]byte(key))
	} else {
		privateKey = make([]byte, curve25519.ScalarSize)
		if _, err = rand.Read(privateKey); err != nil {
			output = Err("read rand error: ", err)
			return
		}
	}
	if publicKey, err = curve25519.X25519(privateKey, curve25519.Basepoint); err != nil {
		output = Err("gen X25519 error: ", err)
		return
	}
	p := base64.RawURLEncoding.EncodeToString(privateKey)
	output = fmt.Sprint("Private key: ",
		p,
		"\nPublic key: ",
		base64.RawURLEncoding.EncodeToString(publicKey))
}
