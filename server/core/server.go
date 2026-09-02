package core

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	box "github.com/sagernet/sing-box"
	"github.com/sagernet/sing-box/include"
	"github.com/sagernet/sing-box/option"
	"github.com/sagernet/sing/common/auth"
	"github.com/sagernet/sing/common/json/badoption"
	"fmt"
	"math/big"
	"net/netip"
	"os"
	"path/filepath"
	"time"
)

func listenAddr(s string) *badoption.Addr {
	addr := badoption.Addr(netip.MustParseAddr(s))
	return &addr
}

func Server(conf Peer) error {
	listenOptions := option.ListenOptions{
		Listen:     listenAddr(conf.Addr),
		ListenPort: conf.Port,
	}
	var in option.Inbound
	switch conf.Protocol {
	case "shadowsocks":
		in = option.Inbound{
			Type: "shadowsocks",
			Tag:  "ss-in",
			Options: &option.ShadowsocksInboundOptions{
				ListenOptions: listenOptions,
				Method:        "aes-256-gcm",
				Password:      conf.UUID,
				Multiplex: &option.InboundMultiplexOptions{
					Enabled: true,
				},
			},
		}
	case "socks":
		in = option.Inbound{
			Type: "socks",
			Tag:  "socks-in",
			Options: &option.SocksInboundOptions{
				ListenOptions: listenOptions,
				Users: []auth.User{
					{
						Username: "gpp",
						Password: conf.UUID,
					},
				},
			},
		}
	case "hysteria2":
		c, k := loadOrGenerateKey()
		in = option.Inbound{
			Type: "hysteria2",
			Tag:  "hy2-in",
			Options: &option.Hysteria2InboundOptions{
				ListenOptions: listenOptions,
				Users: []option.Hysteria2User{
					{
						Name:     "gpp",
						Password: conf.UUID,
					},
				},
				InboundTLSOptionsContainer: option.InboundTLSOptionsContainer{
					TLS: &option.InboundTLSOptions{
						Enabled:     true,
						ServerName:  "gpp",
						ALPN:        badoption.Listable[string]{"h3"},
						Certificate: badoption.Listable[string]{c},
						Key:         badoption.Listable[string]{k},
					},
				},
			},
		}
	default:
		in = option.Inbound{
			Type: "vless",
			Tag:  "vless-in",
			Options: &option.VLESSInboundOptions{
				ListenOptions: listenOptions,
				Users: []option.VLESSUser{
					{
						Name: "gpp",
						UUID: conf.UUID,
					},
				},
				Multiplex: &option.InboundMultiplexOptions{
					Enabled: true,
				},
			},
		}
	}
	var instance, err = box.New(box.Options{
		Context: include.Context(context.Background()),
		Options: option.Options{
			Log: &option.LogOptions{
				Disabled:     false,
				Level:        "info",
				Output:       "run.log",
				Timestamp:    true,
				DisableColor: true,
			},
			Inbounds: []option.Inbound{in},
			Outbounds: []option.Outbound{
				{
					Type:    "direct",
					Tag:     "direct-out",
					Options: &option.DirectOutboundOptions{},
				},
			},
		},
	})
	if err != nil {
		return err
	}
	err = instance.Start()
	if err != nil {
		return err
	}
	return nil
}
// loadOrGenerateKey 返回持久化的自签证书与私钥。
// 首次启动生成并保存到 ~/.gpp/，后续启动复用，避免证书每次变化。
func loadOrGenerateKey() (string, string) {
	home, _ := os.UserHomeDir()
	dir := fmt.Sprintf("%s%c%s", home, os.PathSeparator, ".gpp")
	certPath := filepath.Join(dir, "server-cert.pem")
	keyPath := filepath.Join(dir, "server-key.pem")
	cert, err1 := os.ReadFile(certPath)
	key, err2 := os.ReadFile(keyPath)
	if err1 == nil && err2 == nil {
		return string(cert), string(key)
	}
	c, k := generateKey()
	if c == "" || k == "" {
		return c, k
	}
	_ = os.MkdirAll(dir, 0o755)
	if err := os.WriteFile(certPath, []byte(c), 0o644); err != nil {
		return c, k
	}
	if err := os.WriteFile(keyPath, []byte(k), 0o600); err != nil {
		return c, k
	}
	return c, k
}

func generateKey() (string, string) {
	// 生成RSA密钥对
	pvk, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return "", ""
	}

	// 设置证书信息
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject: pkix.Name{
			Organization: []string{"GPP"},
			CommonName:   "gpp",
		},
		NotBefore: time.Now(),
		NotAfter:  time.Now().Add(365 * 24 * time.Hour),
		KeyUsage:  x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage: []x509.ExtKeyUsage{
			x509.ExtKeyUsageServerAuth,
		},
	}

	// 生成证书
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &pvk.PublicKey, pvk)
	if err != nil {
		return "", ""
	}
	buffer := bytes.NewBuffer([]byte{})
	_ = pem.Encode(buffer, &pem.Block{Type: "CERTIFICATE", Bytes: certDER})
	buffer2 := bytes.NewBuffer([]byte{})
	pvkBytes, _ := x509.MarshalPKCS8PrivateKey(pvk)
	_ = pem.Encode(buffer2, &pem.Block{Type: "PRIVATE KEY", Bytes: pvkBytes})
	return buffer.String(), buffer2.String()
}
