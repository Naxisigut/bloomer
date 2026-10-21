package alert

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net"
	"net/http"
	"os"
	"strings"
	"syscall"
	"time"

	"bloomer/agent/internal/config"
)

var errBark = errors.New("Bark push failed")

type bark struct {
	policy config.Alert
	logger *slog.Logger
}

func newBark(policy config.Alert, logger *slog.Logger) *bark { return &bark{policy, logger} }
func readKey(path string) (string, error) {
	fd, err := syscall.Open(path, syscall.O_RDONLY|syscall.O_NOFOLLOW|syscall.O_CLOEXEC|syscall.O_NONBLOCK, 0)
	if err != nil {
		return "", errBark
	}
	f := os.NewFile(uintptr(fd), path)
	defer f.Close()
	st, err := f.Stat()
	if err != nil || !st.Mode().IsRegular() || st.Mode().Perm() != 0600 {
		return "", errBark
	}
	b, err := io.ReadAll(io.LimitReader(f, 4097))
	if err != nil || len(b) > 4096 {
		return "", errBark
	}
	key := strings.TrimSpace(string(b))
	if key == "" || strings.ContainsAny(key, " \t\r\n\x00") {
		return "", errBark
	}
	return key, nil
}
func (b *bark) Send(ctx context.Context, r Record) error {
	key, err := readKey(b.policy.BarkKeyFile)
	if err != nil {
		return errBark
	}
	payload := struct {
		DeviceKey string `json:"device_key"`
		Title     string `json:"title"`
		Body      string `json:"body"`
		Group     string `json:"group"`
		Level     string `json:"level"`
		Sound     string `json:"sound,omitempty"`
		URL       string `json:"url,omitempty"`
	}{DeviceKey: key, Title: r.Title, Body: r.Body, Group: "darkbloom", Level: r.Level, URL: b.policy.DashboardURL}
	if r.Level == "critical" {
		payload.Sound = "alarm"
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return errBark
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(b.policy.BarkServer, "/")+"/push", bytes.NewReader(body))
	if err != nil {
		return errBark
	}
	req.Header.Set("Content-Type", "application/json")
	transport := b.transport()
	defer transport.CloseIdleConnections()
	client := &http.Client{Timeout: 10 * time.Second, Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return errBark }}
	response, err := client.Do(req)
	if err != nil {
		return errBark
	}
	defer response.Body.Close()
	var result struct {
		Code int `json:"code"`
	}
	// Ignore server error text: a compromised/misconfigured server can echo the key.
	data, err := io.ReadAll(io.LimitReader(response.Body, 65537))
	if err != nil || len(data) > 65536 || response.StatusCode < 200 || response.StatusCode >= 300 || json.Unmarshal(data, &result) != nil || result.Code != 200 {
		return errBark
	}
	return nil
}
func interfaceIPv4(name string) (*net.Interface, net.IP) {
	iface, err := net.InterfaceByName(name)
	if err != nil || iface.Flags&net.FlagUp == 0 {
		return nil, nil
	}
	addresses, err := iface.Addrs()
	if err != nil {
		return nil, nil
	}
	for _, address := range addresses {
		ip, _, err := net.ParseCIDR(address.String())
		if err == nil && ip.To4() != nil && !ip.IsUnspecified() {
			return iface, ip.To4()
		}
	}
	return nil, nil
}
func (b *bark) transport() *http.Transport {
	dialer := &net.Dialer{Timeout: 10 * time.Second}
	network := "tcp"
	if b.policy.BindInterface != "" {
		iface, ip := interfaceIPv4(b.policy.BindInterface)
		if ip == nil {
			b.logger.Warn("Bark interface unavailable; using default route")
		} else {
			dialer.LocalAddr = &net.TCPAddr{IP: ip}
			bindInterface(dialer, iface.Index)
			network = "tcp4"
			// Resolve through the same physical interface, so a system resolver's
			// network traffic does not silently follow the TUN route either.
			udp := &net.Dialer{Timeout: 10 * time.Second, LocalAddr: &net.UDPAddr{IP: ip}}
			bindInterface(udp, iface.Index)
			dnsTCP := &net.Dialer{Timeout: 10 * time.Second, LocalAddr: &net.TCPAddr{IP: ip}}
			bindInterface(dnsTCP, iface.Index)
			dialer.Resolver = &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, address string) (net.Conn, error) {
				if strings.HasPrefix(network, "tcp") {
					return dnsTCP.DialContext(ctx, "tcp4", address)
				}
				return udp.DialContext(ctx, "udp4", address)
			}}
		}
	}
	return &http.Transport{Proxy: nil, DialContext: func(ctx context.Context, _ string, address string) (net.Conn, error) {
		return dialer.DialContext(ctx, network, address)
	}, TLSHandshakeTimeout: 10 * time.Second, ResponseHeaderTimeout: 10 * time.Second}
}
