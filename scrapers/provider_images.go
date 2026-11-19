package scrapers

import (
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
		return address
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
func GetProviderImage(c *gin.Context) {
	address := c.Query("url")
	if len(address) > 2048 {
		c.Status(http.StatusBadRequest)
		return
	}
	u, err := url.Parse(address)
	if err != nil || !allowedImageURL(u) {
		c.Status(http.StatusBadRequest)
		return
	}
	data, mime, err := fetchImage(c.Request.Context(), address)
	if err != nil {
		_ = c.Error(err)
		c.Status(http.StatusBadGateway)
		return
	}
	c.Header("Cache-Control", "public, max-age=3600, stale-while-revalidate=86400")
	c.Header("X-Content-Type-Options", "nosniff")
	c.Data(http.StatusOK, mime, data)
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
