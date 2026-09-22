package main

// Кнопка «+» на вкладке «Группы»: что добавить в правила для сайта или программы.
// Домены сайта ищутся в категориях geosite и geoip из MetaCubeX/meta-rules-dat
// (те же списки, что в шаблонах): instagram.com -> geosite instagram.

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"
	"unsafe"

	"golang.org/x/sys/windows"
)

const geoBase = "https://raw.githubusercontent.com/MetaCubeX/meta-rules-dat/meta/geo/"

// Домены, чья категория называется не по имени сайта.
var siteAliases = map[string]string{
	"x.com": "twitter", "twimg.com": "twitter", "t.co": "twitter",
	"t.me": "telegram", "telegram.org": "telegram", "telegram.me": "telegram",
	"youtu.be": "youtube", "googlevideo.com": "youtube", "ytimg.com": "youtube",
	"fb.com": "facebook", "fbcdn.net": "facebook", "messenger.com": "facebook",
	"cdninstagram.com": "instagram", "ig.me": "instagram",
	"wa.me":      "whatsapp",
	"discord.gg": "discord", "discordapp.com": "discord", "discordapp.net": "discord",
	"chatgpt.com": "openai", "oaistatic.com": "openai",
	"claude.ai": "anthropic",
	"ttvnw.net": "twitch", "jtvnw.net": "twitch",
	"tiktokcdn.com": "tiktok", "tiktokv.com": "tiktok",
	"scdn.co":       "spotify",
	"nflxvideo.net": "netflix", "nflximg.net": "netflix",
	"steampowered.com": "steam", "steamcommunity.com": "steam",
}

var domainRe = regexp.MustCompile(`^([a-z0-9\p{L}]([a-z0-9\p{L}-]*[a-z0-9\p{L}])?\.)+[a-z\p{L}][a-z0-9\p{L}-]*$`)

// normalizeDomain: «https://www.Instagram.com/p/x» -> «instagram.com».
func normalizeDomain(in string) (string, error) {
	s := strings.TrimSpace(strings.ToLower(in))
	if strings.Contains(s, "://") {
		if u, err := url.Parse(s); err == nil {
			s = u.Hostname()
		}
	}
	s = strings.SplitN(s, "/", 2)[0]
	s = strings.TrimPrefix(strings.SplitN(s, ":", 2)[0], "*.")
	s = strings.TrimPrefix(s, "www.")
	s = strings.Trim(s, ".")
	if !domainRe.MatchString(s) {
		return "", fmt.Errorf("%q не похоже на адрес сайта", in)
	}
	return s, nil
}

// registrable - «сайт» без поддоменов: m.vk.com -> vk.com (по двум меткам).
func registrable(d string) string {
	parts := strings.Split(d, ".")
	if len(parts) <= 2 {
		return d
	}
	return strings.Join(parts[len(parts)-2:], ".")
}

func geoCandidates(domain string) []string {
	var out []string
	add := func(s string) {
		for _, x := range out {
			if x == s {
				return
			}
		}
		out = append(out, s)
	}
	for _, d := range []string{domain, registrable(domain)} {
		if c, ok := siteAliases[d]; ok {
			add(c)
		}
	}
	add(strings.Split(registrable(domain), ".")[0])
	return out
}

type geoList struct {
	Name     string   `json:"name"`
	Count    int      `json:"count"`
	Sample   []string `json:"sample"`
	Contains bool     `json:"contains"` // есть ли в списке введённый домен
	URL      string   `json:"url"`      // .mrs для rule-provider
}

type SiteLookup struct {
	Domain  string   `json:"domain"`
	Geosite *geoList `json:"geosite"`
	Geoip   *geoList `json:"geoip"`
}

// fetchGeoList качает текстовый список категории; nil - такой категории нет.
func fetchGeoList(ctx context.Context, kind, name, domain string) (*geoList, error) {
	req, _ := http.NewRequestWithContext(ctx, "GET", geoBase+kind+"/"+url.PathEscape(name)+".list", nil)
	req.Header.Set("User-Agent", appName+"/"+appVersion)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		return nil, nil
	}
	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GitHub ответил %s", resp.Status)
	}
	l := &geoList{Name: name, URL: geoBase + kind + "/" + name + ".mrs"}
	sc := bufio.NewScanner(resp.Body)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		e := strings.TrimSpace(sc.Text())
		if e == "" || strings.HasPrefix(e, "#") {
			continue
		}
		l.Count++
		if len(l.Sample) < 40 {
			l.Sample = append(l.Sample, e)
		}
		if kind == "geosite" && !l.Contains {
			d := strings.TrimPrefix(strings.TrimPrefix(e, "+."), ".")
			l.Contains = domain == d || strings.HasSuffix(domain, "."+d)
		}
	}
	if err := sc.Err(); err != nil {
		return nil, err
	}
	return l, nil
}

func lookupSite(ctx context.Context, input string) (*SiteLookup, error) {
	domain, err := normalizeDomain(input)
	if err != nil {
		return nil, err
	}
	ctx, cancel := context.WithTimeout(ctx, 20*time.Second)
	defer cancel()
	res := &SiteLookup{Domain: domain}
	var firstErr error
	for _, cand := range geoCandidates(domain) {
		gs, err := fetchGeoList(ctx, "geosite", cand, domain)
		if err != nil {
			firstErr = err
			continue
		}
		if gs == nil {
			continue
		}
		res.Geosite = gs
		if gi, err := fetchGeoList(ctx, "geoip", cand, domain); err == nil {
			res.Geoip = gi
		}
		break
	}
	if res.Geosite == nil && firstErr != nil {
		return res, errors.New("списки доменов недоступны: " + firstErr.Error())
	}
	return res, nil
}

type ProcInfo struct {
	Name string `json:"name"`
	Path string `json:"path"`
}

// listProcesses - запущенные программы пользователя (без системных), по имени.
func listProcesses() []ProcInfo {
	snap, err := windows.CreateToolhelp32Snapshot(windows.TH32CS_SNAPPROCESS, 0)
	if err != nil {
		return nil
	}
	defer windows.CloseHandle(snap)
	winDir := strings.ToLower(os.Getenv("SystemRoot"))
	self, _ := os.Executable()
	seen := map[string]bool{}
	var out []ProcInfo
	var e windows.ProcessEntry32
	e.Size = uint32(unsafe.Sizeof(e))
	for err = windows.Process32First(snap, &e); err == nil; err = windows.Process32Next(snap, &e) {
		name := windows.UTF16ToString(e.ExeFile[:])
		key := strings.ToLower(name)
		if seen[key] || !strings.HasSuffix(key, ".exe") {
			continue
		}
		path := processPath(e.ProcessID)
		lp := strings.ToLower(path)
		if path == "" || (winDir != "" && strings.HasPrefix(lp, winDir+`\`)) ||
			strings.EqualFold(path, self) || key == "mihomo.exe" {
			continue
		}
		seen[key] = true
		out = append(out, ProcInfo{Name: name, Path: path})
	}
	sort.Slice(out, func(i, j int) bool { return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name) })
	return out
}

func processPath(pid uint32) string {
	h, err := windows.OpenProcess(windows.PROCESS_QUERY_LIMITED_INFORMATION, false, pid)
	if err != nil {
		return ""
	}
	defer windows.CloseHandle(h)
	buf := make([]uint16, windows.MAX_LONG_PATH)
	n := uint32(len(buf))
	if windows.QueryFullProcessImageName(h, 0, &buf[0], &n) != nil {
		return ""
	}
	return filepath.Clean(windows.UTF16ToString(buf[:n]))
}

var procNameRe = regexp.MustCompile(`^[^\\/:*?"<>|,()#]+\.exe$`)

// normalizeProcess: путь или имя -> «Telegram.exe».
func normalizeProcess(in string) (string, error) {
	s := strings.TrimSpace(strings.Trim(strings.TrimSpace(in), `"`))
	s = filepath.Base(strings.ReplaceAll(s, "/", `\`))
	if !strings.HasSuffix(strings.ToLower(s), ".exe") {
		s += ".exe"
	}
	if !procNameRe.MatchString(s) {
		return "", fmt.Errorf("%q не похоже на имя программы", in)
	}
	return s, nil
}
