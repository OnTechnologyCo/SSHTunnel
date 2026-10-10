package server

import (
	"context"
	"errors"
	"net"
	"strconv"
	"strings"
)

func validPort(v string, zero bool) bool {
	n, e := strconv.Atoi(v)
	return e == nil && n >= 0 && n <= 65535 && (zero || n > 0)
}
func validPortRule(v string) bool {
	if v == "*" {
		return true
	}
	a, b, found := strings.Cut(v, "-")
	if !found {
		return validPort(a, true)
	}
	if !validPort(a, false) || !validPort(b, false) {
		return false
	}
	x, _ := strconv.Atoi(a)
	y, _ := strconv.Atoi(b)
	return x <= y
}
func portAllowed(rule string, port uint32) bool {
	if rule == "*" {
		return port <= 65535
	}
	a, b, rangeRule := strings.Cut(rule, "-")
	x, _ := strconv.Atoi(a)
	if !rangeRule {
		return uint32(x) == port
	}
	y, _ := strconv.Atoi(b)
	return port >= uint32(x) && port <= uint32(y)
}
func parseTarget(rule string) (string, string, error) {
	h, p, e := net.SplitHostPort(rule)
	if e != nil || h == "" || !validPortRule(p) {
		return "", "", errors.New("대상은 host:port, IP:*, [IPv6]:port, CIDR:port 또는 *:* 형식입니다")
	}
	if strings.ContainsAny(h, " \t\r\n\"'\\") {
		return "", "", errors.New("대상 호스트에 사용할 수 없는 문자가 있습니다")
	}
	if strings.Contains(h, "/") {
		if _, _, e = net.ParseCIDR(h); e != nil {
			return "", "", e
		}
	}
	return strings.ToLower(h), p, nil
}
func matchTarget(rules []string, host string, ip net.IP, port uint32) bool {
	for _, rule := range rules {
		h, p, e := parseTarget(rule)
		if e != nil || !portAllowed(p, port) {
			continue
		}
		if h == "*" || h == strings.ToLower(host) {
			return true
		}
		if address := net.ParseIP(h); address != nil && address.Equal(ip) {
			return true
		}
		if _, network, e := net.ParseCIDR(h); e == nil && network.Contains(ip) {
			return true
		}
	}
	return false
}

// DNS 이름은 한 번만 조회하고, 허용된 IP로 직접 연결해 CIDR 정책의 DNS 재해석 우회를 막습니다.
func dialTarget(ctx context.Context, u User, host string, port uint32) (net.Conn, error) {
	if !u.AllowLocal || port == 0 || port > 65535 || len(host) == 0 || len(host) > 253 || strings.ContainsAny(host, "\x00\r\n") {
		return nil, errors.New("target denied")
	}
	var ips []net.IPAddr
	if ip := net.ParseIP(host); ip != nil {
		ips = []net.IPAddr{{IP: ip}}
	} else {
		var e error
		ips, e = net.DefaultResolver.LookupIPAddr(ctx, host)
		if e != nil {
			return nil, e
		}
	}
	d := net.Dialer{}
	var last error = errors.New("target denied")
	for _, ip := range ips {
		if !matchTarget(u.Targets, host, ip.IP, port) {
			continue
		}
		c, e := d.DialContext(ctx, "tcp", net.JoinHostPort(ip.IP.String(), strconv.Itoa(int(port))))
		if e == nil {
			return c, nil
		}
		last = e
	}
	return nil, last
}
func reverseAddress(u User, host string, port uint32) (string, error) {
	if !u.AllowReverse || port > 65535 || len(host) > 253 {
		return "", errors.New("reverse denied")
	}
	ok := false
	for _, rule := range u.ListenPorts {
		if portAllowed(rule, port) {
			ok = true
			break
		}
	}
	if !ok {
		return "", errors.New("reverse port denied")
	}
	if host == "localhost" {
		return "127.0.0.1", nil
	}
	if host == "" || host == "*" {
		if !u.AllowPublicReverse {
			return "", errors.New("public reverse denied")
		}
		return "0.0.0.0", nil
	}
	ip := net.ParseIP(host)
	if ip == nil {
		return "", errors.New("reverse bind must be an IP address")
	}
	if !ip.IsLoopback() && !u.AllowPublicReverse {
		return "", errors.New("public reverse denied")
	}
	return ip.String(), nil
}
