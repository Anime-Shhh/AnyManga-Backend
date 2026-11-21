package scrapers

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"image"
	_ "image/gif"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
)

func allowedImageURL(u *url.URL) bool {
	if u.Scheme != "https" || u.User != nil || (u.Port() != "" && u.Port() != "443") {
		return false
	}
	switch u.Hostname() {
	case "www.mangaread.org", "mangaread.org":
		return strings.HasPrefix(u.Path, "/wp-content/uploads/")
	case "cdn.readdetectiveconan.com":
		return strings.HasPrefix(u.Path, "/file/mangapill/") || strings.HasPrefix(u.Path, "/file/mangap/")
	case "scans-hot.planeptune.us":
		return strings.HasPrefix(u.Path, "/manga/")
	case "temp.compsci88.com":
		return strings.HasPrefix(u.Path, "/cover/")
	case "uploads.mangadex.org":
		return true
	}
	return strings.HasSuffix(u.Hostname(), ".mangadex.network")
}
func imageProxy(address string) string {
	u, err := url.Parse(address)
	if err != nil || !allowedImageURL(u) {
		return ""
	}
	return "/api/manga/image?" + url.Values{"url": {address}}.Encode()
}

var imageClient = &http.Client{Timeout: 20 * time.Second, Transport: &http.Transport{DialContext: publicImageDial, TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}, MaxIdleConns: 32, MaxIdleConnsPerHost: 8, IdleConnTimeout: 60 * time.Second}, CheckRedirect: func(req *http.Request, via []*http.Request) error {
	if len(via) > 3 || !allowedImageURL(req.URL) {
		return errors.New("invalid image redirect")
	}
	return nil
}}
var imageSlots = make(chan struct{}, 12)

func fetchImage(ctx context.Context, address string) (data []byte, mime string, err error) {
	started := time.Now()
	defer func() { reportMangaDexImage(address, err == nil, len(data), time.Since(started)) }()
	u, err := url.Parse(address)
	if err != nil || !allowedImageURL(u) {
		return nil, "", errors.New("unrecognized image host or path")
	}
	select {
	case imageSlots <- struct{}{}:
		defer func() { <-imageSlots }()
	case <-ctx.Done():
		return nil, "", ctx.Err()
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, address, nil)
	if err != nil {
		return nil, "", err
	}
	req.Header.Set("User-Agent", providerAgent)
	if u.Hostname() == "cdn.readdetectiveconan.com" {
		req.Header.Set("Referer", "https://mangapill.com/")
	} else if strings.Contains(u.Hostname(), "planeptune") {
		req.Header.Set("Referer", "https://weebcentral.com/")
	}
	resp, err := imageClient.Do(req)
	if err != nil {
		return nil, "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return nil, "", fmt.Errorf("image provider returned HTTP %d", resp.StatusCode)
	}
	data, err = io.ReadAll(io.LimitReader(resp.Body, (12<<20)+1))
	if err != nil || len(data) > 12<<20 {
		return nil, "", errors.New("image exceeds size limit")
	}
	mime = http.DetectContentType(data)
	if mime != "image/jpeg" && mime != "image/png" && mime != "image/webp" && mime != "image/gif" {
		return nil, "", errors.New("provider did not return an image")
	}
	if mime != "image/webp" {
		config, _, err := image.DecodeConfig(bytes.NewReader(data))
		if err != nil || int64(config.Width)*int64(config.Height) > 60_000_000 {
			return nil, "", errors.New("invalid image dimensions")
		}
	}
	return data, mime, nil
}

var proxyRequests, proxyBytes, proxyErrors atomic.Uint64

func ImageMetrics() map[string]uint64 {
	return map[string]uint64{"proxyRequests": proxyRequests.Load(), "proxyBytes": proxyBytes.Load(), "proxyErrors": proxyErrors.Load()}
}

// Only MangaRead passed real cross-origin browser embedding tests.
// MangaPill and WeebCentral CDNs returned blocked opaque responses; retain streaming proxy delivery.
func imageDelivery(address string) (string, string) {
	u, err := url.Parse(address)
	if err != nil || !allowedImageURL(u) {
		return "", ""
	}
	switch u.Hostname() {
	case "www.mangaread.org", "mangaread.org":
		return address, imageProxy(address)
	}
	return imageProxy(address), ""
}

func GetProviderImage(c *gin.Context) {
	address := c.Query("url")
	u, err := url.Parse(address)
	if len(address) > 2048 || err != nil || !allowedImageURL(u) {
		c.Status(http.StatusBadRequest)
		return
	}
	select {
	case imageSlots <- struct{}{}:
		defer func() { <-imageSlots }()
	case <-c.Request.Context().Done():
		return
	}
	proxyRequests.Add(1)
	started := time.Now()
	written := int64(0)
	success := false
	defer func() {
		proxyBytes.Add(uint64(written))
		if !success {
			proxyErrors.Add(1)
		}
		reportMangaDexImage(address, success, int(written), time.Since(started))
	}()
	req, err := http.NewRequestWithContext(c.Request.Context(), http.MethodGet, address, nil)
	if err != nil {
		c.Status(400)
		return
	}
	req.Header.Set("User-Agent", providerAgent)
	if u.Hostname() == "cdn.readdetectiveconan.com" {
		req.Header.Set("Referer", "https://mangapill.com/")
	} else if u.Hostname() == "scans-hot.planeptune.us" {
		req.Header.Set("Referer", "https://weebcentral.com/")
	}
	response, err := imageClient.Do(req)
	if err != nil {
		_ = c.Error(err)
		c.Status(502)
		return
	}
	defer response.Body.Close()
	const maxBytes = int64(12 << 20)
	if response.StatusCode != 200 || response.ContentLength > maxBytes {
		c.Status(502)
		return
	}
	reader := bufio.NewReaderSize(response.Body, 64<<10)
	peek, err := reader.Peek(512)
	if err != nil && err != io.EOF {
		c.Status(502)
		return
	}
	mime := http.DetectContentType(peek)
	if mime != "image/jpeg" && mime != "image/png" && mime != "image/gif" && mime != "image/webp" {
		c.Status(502)
		return
	}
	var prefix bytes.Buffer
	if mime != "image/webp" {
		config, _, err := image.DecodeConfig(io.TeeReader(io.LimitReader(reader, 64<<10), &prefix))
		if err != nil || int64(config.Width)*int64(config.Height) > 60_000_000 {
			c.Status(502)
			return
		}
	}
	c.Header("Content-Type", mime)
	c.Header("Cache-Control", "public, max-age=3600, stale-while-revalidate=86400")
	c.Header("X-Content-Type-Options", "nosniff")
	if response.ContentLength >= 0 {
		c.Header("Content-Length", fmt.Sprint(response.ContentLength))
	}
	body := io.MultiReader(&prefix, reader)
	written, err = io.Copy(c.Writer, io.LimitReader(body, maxBytes))
	var extra [1]byte
	n, extraErr := body.Read(extra[:])
	success = err == nil && n == 0 && (extraErr == io.EOF || extraErr == nil)
	if !success {
		_ = c.Error(errors.New("image stream interrupted or exceeded its limit"))
	}
}

var reportSlots = make(chan struct{}, 2)

func reportMangaDexImage(address string, success bool, size int, elapsed time.Duration) {
	u, err := url.Parse(address)
	if err != nil || !strings.HasSuffix(u.Hostname(), ".mangadex.network") {
		return
	}
	select {
	case reportSlots <- struct{}{}:
	default:
		return
	}
	go func() {
		defer func() { <-reportSlots }()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		defer cancel()
		payload, _ := json.Marshal(map[string]any{"url": address, "success": success, "cached": false, "bytes": size, "duration": elapsed.Milliseconds()})
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, "https://api.mangadex.network/report", bytes.NewReader(payload))
		if err != nil {
			return
		}
		req.Header.Set("Content-Type", "application/json")
		req.Header.Set("User-Agent", providerAgent)
		if response, err := providerClient.Do(req); err == nil {
			response.Body.Close()
		}
	}()
}
