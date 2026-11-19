package scrapers

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/PuerkitoBio/goquery"
)

const providerAgent = "AnyManga/1.0 (+https://github.com/Anime-Shhh/AnyManga-Backend)"

var providerClient = &http.Client{
	Timeout:   12 * time.Second,
	Transport: &http.Transport{Proxy: http.ProxyFromEnvironment, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS13}, MaxIdleConns: 32, MaxIdleConnsPerHost: 8, IdleConnTimeout: 90 * time.Second},
	CheckRedirect: func(req *http.Request, via []*http.Request) error {
		if len(via) > 4 || !allowedProviderURL(req.URL) {
			return errors.New("unexpected provider redirect")
		}
		return nil
	},
}

var mdQuota struct {
	sync.Mutex
	next, nextManifest, blocked time.Time
}
var providerPermits = map[string]chan struct{}{
	"mangadex": make(chan struct{}, 4), "weebcentral": make(chan struct{}, 4), "mangapill": make(chan struct{}, 4), "mangaread": make(chan struct{}, 4),
}

func allowedProviderURL(u *url.URL) bool {
	if u.Scheme != "https" || u.User != nil || (u.Port() != "" && u.Port() != "443") {
		return false
	}
	switch u.Hostname() {
	case "api.mangadex.org", "www.mangaread.org", "mangaread.org", "weebcentral.com", "mangapill.com":
		return true
	}
	return allowedImageURL(u)
}

func reserveMangaDex(ctx context.Context, manifest bool) error {
	mdQuota.Lock()
	now := time.Now()
	if now.Before(mdQuota.blocked) {
		mdQuota.Unlock()
		return errors.New("MangaDex is cooling down after rate limiting")
	}
	at := maxTime(now, mdQuota.next)
	if manifest {
		at = maxTime(at, mdQuota.nextManifest)
		mdQuota.nextManifest = at.Add(1600 * time.Millisecond)
	}
	mdQuota.next = at.Add(260 * time.Millisecond)
	mdQuota.Unlock()
	timer := time.NewTimer(time.Until(at))
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}
func maxTime(a, b time.Time) time.Time {
	if a.After(b) {
		return a
	}
	return b
}

func providerGet(ctx context.Context, provider, address string) (*http.Response, error) {
	u, err := url.Parse(address)
	if err != nil || !allowedProviderURL(u) {
		return nil, errors.New("invalid provider address")
	}
	permit := providerPermits[provider]
	select {
	case permit <- struct{}{}:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	defer func() { <-permit }()
	if provider == "mangadex" && u.Hostname() == "api.mangadex.org" {
		if err := reserveMangaDex(ctx, strings.HasPrefix(u.Path, "/at-home/")); err != nil {
			return nil, err
		}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("User-Agent", providerAgent)
	if provider == "weebcentral" {
		req.Header.Set("Referer", "https://weebcentral.com/")
	}
	if provider == "mangapill" {
		req.Header.Set("Referer", "https://mangapill.com/")
	}
	resp, err := providerClient.Do(req)
	if err != nil {
		return nil, err
	}
	if resp.StatusCode == http.StatusOK {
		return resp, nil
	}
	defer resp.Body.Close()
	if provider == "mangadex" && resp.StatusCode == http.StatusTooManyRequests {
		delay := time.Minute
		if seconds, err := strconv.Atoi(resp.Header.Get("Retry-After")); err == nil && seconds > 0 {
			delay = time.Duration(seconds) * time.Second
		}
		mdQuota.Lock()
		mdQuota.blocked = time.Now().Add(delay)
		mdQuota.Unlock()
	}
	if resp.StatusCode == http.StatusNotFound {
		return nil, missing
	}
	return nil, fmt.Errorf("%s returned HTTP %d", provider, resp.StatusCode)
}

func providerDocument(provider, address string) (*goquery.Document, *url.URL, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	resp, err := providerGet(ctx, provider, address)
	if err != nil {
		return nil, nil, err
	}
	defer resp.Body.Close()
	doc, err := goquery.NewDocumentFromReader(io.LimitReader(resp.Body, 16<<20))
	return doc, resp.Request.URL, err
}

var mdJSONCache = newCache[[]byte](256, 3*time.Minute)

func mdJSON(path string, params url.Values, target any) error {
	address := "https://api.mangadex.org" + path
	if len(params) > 0 {
		address += "?" + params.Encode()
	}
	data, err := mdJSONCache.get(address, func() ([]byte, error) {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
		defer cancel()
		resp, err := providerGet(ctx, "mangadex", address)
		if err != nil {
			return nil, err
		}
		defer resp.Body.Close()
		return io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	})
	if err != nil {
		return err
	}
	return json.Unmarshal(data, target)
}

// Reject non-public destinations even when an allowed CDN redirects or changes DNS.
func publicImageDial(ctx context.Context, network, address string) (net.Conn, error) {
	host, port, err := net.SplitHostPort(address)
	if err != nil {
		return nil, err
	}
	ips, err := net.DefaultResolver.LookupIPAddr(ctx, host)
	if err != nil {
		return nil, err
	}
	for _, ip := range ips {
		if !ip.IP.IsGlobalUnicast() || ip.IP.IsPrivate() || ip.IP.IsLoopback() || ip.IP.IsLinkLocalUnicast() {
			return nil, errors.New("non-public image destination")
		}
	}
	for _, ip := range ips {
		conn, err := (&net.Dialer{Timeout: 8 * time.Second}).DialContext(ctx, network, net.JoinHostPort(ip.IP.String(), port))
		if err == nil {
			return conn, nil
		}
	}
	return nil, errors.New("image destination unavailable")
}
