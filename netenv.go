package main

// Сетевое окружение ПК, которое влияет на TUN: IPv6 и корпоративный VPN.

import (
	"context"
	"encoding/json"
	"net"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
	"golang.org/x/sys/windows/registry"
)

// tunDevice - имя TUN-адаптера, которое программа задаёт ядру.
const tunDevice = "Mejgorod"

// hasGlobalIPv6 - есть ли у ПК настоящий IPv6 (не TUN, не Teredo, не локальный).
// Без него ядру незачем отдавать приложениям адреса IPv6: соединения на них
// падают с bind6 и ждут перехода на IPv4.
func hasGlobalIPv6() bool {
	ifs, err := net.Interfaces()
	if err != nil {
		return true
	}
	for _, ifc := range ifs {
		if ifc.Flags&net.FlagUp == 0 || ifc.Flags&net.FlagLoopback != 0 || ifc.Name == tunDevice {
			continue
		}
		addrs, _ := ifc.Addrs()
		for _, a := range addrs {
			ipn, ok := a.(*net.IPNet)
			if !ok || ipn.IP.To4() != nil || !ipn.IP.IsGlobalUnicast() {
				continue
			}
			ip := ipn.IP
			// 2001::/32 - Teredo, fc00::/7 - частные (в том числе TUN других VPN)
			if (ip[0] == 0x20 && ip[1] == 0x01 && ip[2] == 0 && ip[3] == 0) || ip[0]&0xfe == 0xfc {
				continue
			}
			return true
		}
	}
	return false
}

// CorpVPN - подключённый корпоративный VPN, с которым TUN должен ужиться.
//
// Citrix Secure Access перехватывает исходящие пакеты и «не свои» возвращает
// в стек, а Windows отправляет их по лучшему маршруту - в TUN: ядро теряет
// привязку к Wi-Fi и ловит само себя. Маршруты к своему шлюзу и DNS Citrix
// тоже прокладывает через шлюз по умолчанию, то есть через TUN, и рвётся сам.
// Поэтому при нём TUN не забирает весь интернет (см. split-режим в adapt.go).
type CorpVPN struct {
	Name       string
	DNS        []string // DNS-серверы адаптера VPN
	Suffixes   []string // внутренние домены: их резолвит DNS VPN, без fake-ip
	Gateway    string   // хост шлюза
	GatewayIPs []string
}

const (
	gaaSkipAnycast   = 0x2
	gaaSkipMulticast = 0x4
)

func adapterAddresses() []*windows.IpAdapterAddresses {
	size := uint32(16 << 10)
	for range 4 {
		buf := make([]byte, size)
		first := (*windows.IpAdapterAddresses)(unsafe.Pointer(&buf[0]))
		err := windows.GetAdaptersAddresses(windows.AF_UNSPEC, gaaSkipAnycast|gaaSkipMulticast, 0, first, &size)
		if err == windows.ERROR_BUFFER_OVERFLOW {
			continue
		}
		if err != nil {
			return nil
		}
		var out []*windows.IpAdapterAddresses
		for aa := first; aa != nil; aa = aa.Next {
			out = append(out, aa)
		}
		// buf жив, пока жив out: указатели ведут внутрь него
		return out
	}
	return nil
}

// corpAdapter - адаптер подключённого Citrix Secure Access (есть IPv4), иначе nil.
func corpAdapter(list []*windows.IpAdapterAddresses) *windows.IpAdapterAddresses {
	for _, aa := range list {
		desc := windows.UTF16PtrToString(aa.Description)
		if aa.OperStatus != windows.IfOperStatusUp || !strings.Contains(desc, "Citrix Virtual Adapter") {
			continue
		}
		for u := aa.FirstUnicastAddress; u != nil; u = u.Next {
			if ip := u.Address.IP(); ip.To4() != nil && !ip.IsLinkLocalUnicast() {
				return aa
			}
		}
	}
	return nil
}

// corpVPNUp - быстрая проверка без DNS и реестра, для слежения.
func corpVPNUp() bool { return corpAdapter(adapterAddresses()) != nil }

// detectCorpVPN ищет подключённый Citrix Secure Access и собирает его настройки.
func detectCorpVPN() *CorpVPN {
	list := adapterAddresses()
	if aa := corpAdapter(list); aa != nil {
		vpn := &CorpVPN{Name: "Citrix Secure Access"}
		for d := aa.FirstDnsServerAddress; d != nil; d = d.Next {
			if ip := d.Address.IP(); ip.To4() != nil {
				vpn.DNS = append(vpn.DNS, ip.String())
			}
		}
		var sfx []string
		if s := windows.UTF16PtrToString(aa.DnsSuffix); s != "" {
			sfx = append(sfx, s)
		}
		for s := aa.FirstDnsSuffix; s != nil; s = s.Next {
			sfx = append(sfx, windows.UTF16ToString(s.String[:]))
		}
		// Citrix пишет внутренние домены в общий список поиска DNS
		if k, err := registry.OpenKey(registry.LOCAL_MACHINE, `SYSTEM\CurrentControlSet\Services\Tcpip\Parameters`, registry.QUERY_VALUE); err == nil {
			if v, _, err := k.GetStringValue("SearchList"); err == nil {
				sfx = append(sfx, strings.Split(v, ",")...)
			}
			k.Close()
		}
		vpn.Suffixes = cleanDomains(sfx)
		vpn.Gateway = citrixGateway()
		if vpn.Gateway != "" {
			ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
			ips, _ := net.DefaultResolver.LookupIPAddr(ctx, vpn.Gateway)
			cancel()
			for _, ip := range ips {
				if ip.IP.To4() != nil {
					vpn.GatewayIPs = append(vpn.GatewayIPs, ip.IP.String())
				}
			}
		}
		return vpn
	}
	return nil
}

// citrixGateway - адрес шлюза из настроек клиента Citrix («connectingTo»).
func citrixGateway() string {
	b, err := os.ReadFile(filepath.Join(os.Getenv("LOCALAPPDATA"), "Citrix", "AGEE", "config.js"))
	if err != nil {
		return ""
	}
	var cfg struct {
		ConnectingTo string `json:"connectingTo"`
	}
	if json.Unmarshal(b, &cfg) != nil {
		return ""
	}
	u, err := url.Parse(cfg.ConnectingTo)
	if err != nil {
		return ""
	}
	return u.Hostname()
}

func cleanDomains(in []string) []string {
	seen := map[string]bool{}
	var out []string
	for _, s := range in {
		s = strings.ToLower(strings.Trim(strings.TrimSpace(s), "."))
		if s == "" || seen[s] || strings.ContainsAny(s, " \t,") {
			continue
		}
		seen[s] = true
		out = append(out, s)
	}
	sort.Strings(out)
	return out
}
