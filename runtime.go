package main

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/gin-gonic/gin"
	"manga-scraper/scrapers"
)

var buildCommit = "development"

type appConfig struct {
	Address           string `json:"address"`
	APIKeyFile        string `json:"apiKeyFile"`
	DeploymentID      string `json:"deploymentId"`
	LogDirectory      string `json:"logDirectory"`
	MetadataPerMinute int    `json:"metadataPerMinute"`
	ImagePerMinute    int    `json:"imagePerMinute"`
	PDFPerMinute      int    `json:"pdfPerMinute"`
	key               string
}

func loadConfig(path string) (appConfig, error) {
	c := appConfig{Address: "127.0.0.1:8080", MetadataPerMinute: 120, ImagePerMinute: 600, PDFPerMinute: 2}
	if path != "" {
		data, err := os.ReadFile(path)
		if err != nil {
			return c, err
		}
		decoder := json.NewDecoder(strings.NewReader(string(data)))
		decoder.DisallowUnknownFields()
		if err := decoder.Decode(&c); err != nil {
			return c, err
		}
	}
	if port := os.Getenv("PORT"); port != "" && path == "" {
		c.Address = ":" + port
	}
	if address := os.Getenv("BIND_ADDR"); address != "" {
		c.Address = address
	}
	if id := os.Getenv("MANGA_DEPLOYMENT_ID"); id != "" {
		c.DeploymentID = id
	}
	if c.DeploymentID == "" {
		c.DeploymentID = "anymanga-" + buildCommit
	}
	if keyFile := os.Getenv("MANGA_API_KEY_FILE"); keyFile != "" {
		c.APIKeyFile = keyFile
	}
	if c.APIKeyFile != "" {
		data, err := os.ReadFile(c.APIKeyFile)
		if err != nil {
			return c, fmt.Errorf("read API key file: %w", err)
		}
		c.key = strings.TrimSpace(string(data))
		if len(c.key) < 32 {
			return c, errors.New("API key must contain at least 32 characters")
		}
	}
	if c.MetadataPerMinute < 1 || c.ImagePerMinute < 1 || c.PDFPerMinute < 1 {
		return c, errors.New("rate limits must be positive")
	}
	if path != "" {
		host, _, err := net.SplitHostPort(c.Address)
		if err != nil || net.ParseIP(host) == nil || !net.ParseIP(host).IsLoopback() {
			return c, errors.New("configured home deployments must bind to loopback")
		}
	}
	if path != "" && c.key == "" {
		return c, errors.New("a configured deployment requires an API key file")
	}
	return c, nil
}

type trafficMetrics struct{ Requests, Errors, Bytes, Nanoseconds atomic.Uint64 }

var traffic trafficMetrics
var startedAt = time.Now().UTC()

type rateBucket struct {
	tokens        float64
	updated, used time.Time
}
type clientLimits struct {
	sync.Mutex
	buckets map[string]rateBucket
}

func (l *clientLimits) allow(key string, limit int, now time.Time) (bool, int) {
	l.Lock()
	defer l.Unlock()
	burst := min(limit, 60)
	bucket, ok := l.buckets[key]
	if !ok {
		bucket = rateBucket{tokens: float64(burst), updated: now}
	}
	bucket.tokens = min(float64(burst), bucket.tokens+now.Sub(bucket.updated).Seconds()*float64(limit)/60)
	bucket.updated = now
	bucket.used = now
	allowed := bucket.tokens >= 1
	retry := 1
	if allowed {
		bucket.tokens--
	} else {
		retry = int((1-bucket.tokens)*60/float64(limit)) + 1
	}
	if len(l.buckets) >= 10000 && !ok {
		var oldest string
		var used time.Time
		for k, b := range l.buckets {
			if used.IsZero() || b.used.Before(used) {
				oldest = k
				used = b.used
			}
		}
		delete(l.buckets, oldest)
	}
	l.buckets[key] = bucket
	return allowed, retry
}
func requestMiddleware(config appConfig) gin.HandlerFunc {
	limits := &clientLimits{buckets: map[string]rateBucket{}}
	expected := sha256.Sum256([]byte(config.key))
	return func(c *gin.Context) {
		start := time.Now()
		idBytes := make([]byte, 12)
		_, _ = rand.Read(idBytes)
		requestID := hex.EncodeToString(idBytes)
		c.Header("X-AnyManga-Request-ID", requestID)
		c.Header("X-AnyManga-Backend-ID", config.DeploymentID)
		c.Header("X-Content-Type-Options", "nosniff")
		defer func() {
			traffic.Requests.Add(1)
			traffic.Nanoseconds.Add(uint64(time.Since(start)))
			if c.Writer.Status() >= 400 {
				traffic.Errors.Add(1)
			}
			traffic.Bytes.Add(uint64(max(c.Writer.Size(), 0)))
			log.Printf("request_id=%s method=%s path=%s status=%d bytes=%d duration_ms=%d", requestID, c.Request.Method, c.Request.URL.Path, c.Writer.Status(), c.Writer.Size(), time.Since(start).Milliseconds())
		}()
		path := strings.TrimPrefix(c.Request.URL.Path, "/api/manga")
		if path == "/health" {
			c.Header("Cache-Control", "no-store")
			c.Status(http.StatusNoContent)
			c.Abort()
			return
		}
		authenticated := false
		if config.key != "" {
			candidate := sha256.Sum256([]byte(strings.TrimPrefix(c.GetHeader("Authorization"), "Bearer ")))
			authenticated = subtle.ConstantTimeCompare(candidate[:], expected[:]) == 1 && strings.HasPrefix(c.GetHeader("Authorization"), "Bearer ")
			if !authenticated {
				c.AbortWithStatusJSON(http.StatusUnauthorized, gin.H{"error": "Unauthorized."})
				return
			}
		}
		identity, _, _ := net.SplitHostPort(c.Request.RemoteAddr)
		if authenticated {
			if ip := net.ParseIP(c.GetHeader("X-AnyManga-Client-IP")); ip != nil {
				identity = ip.String()
			}
		}
		limit := config.MetadataPerMinute
		class := "metadata"
		if path == "/image" {
			limit = config.ImagePerMinute
			class = "image"
		}
		if path == "/chapters" {
			limit = config.PDFPerMinute
			class = "pdf"
		}
		if ok, retry := limits.allow(identity+":"+class, limit, time.Now()); !ok {
			c.Header("Retry-After", strconv.Itoa(retry))
			c.AbortWithStatusJSON(http.StatusTooManyRequests, gin.H{"error": "Too many requests. Please try again shortly."})
			return
		}
		if path == "/admin/status" {
			if !authenticated {
				c.AbortWithStatus(http.StatusUnauthorized)
				return
			}
			c.Header("Cache-Control", "no-store")
			c.JSON(200, gin.H{"deploymentId": config.DeploymentID, "commit": buildCommit, "startedAt": startedAt, "requests": traffic.Requests.Load(), "errors": traffic.Errors.Load(), "responseBytes": traffic.Bytes.Load(), "requestNanoseconds": traffic.Nanoseconds.Load(), "cache": scrapers.CacheMetrics(), "images": scrapers.ImageMetrics()})
			c.Abort()
			return
		}
		c.Next()
	}
}

// The service account writes only the logs directory; releases/configuration are read-only.
type rotatingLog struct {
	sync.Mutex
	path string
	file *os.File
	size int64
}

func openLog(directory string) (io.WriteCloser, error) {
	if err := os.MkdirAll(directory, 0750); err != nil {
		return nil, err
	}
	r := &rotatingLog{path: filepath.Join(directory, "backend.log")}
	err := r.open()
	return r, err
}
func (r *rotatingLog) open() error {
	f, err := os.OpenFile(r.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
	if err != nil {
		return err
	}
	r.file = f
	info, err := f.Stat()
	if err != nil {
		f.Close()
		return err
	}
	r.size = info.Size()
	return nil
}
func (r *rotatingLog) Write(p []byte) (int, error) {
	r.Lock()
	defer r.Unlock()
	if r.size+int64(len(p)) > 10<<20 {
		if err := r.file.Close(); err != nil {
			return 0, err
		}
		_ = os.Remove(r.path + ".3")
		for i := 2; i >= 1; i-- {
			_ = os.Rename(r.path+"."+strconv.Itoa(i), r.path+"."+strconv.Itoa(i+1))
		}
		_ = os.Rename(r.path, r.path+".1")
		if err := r.open(); err != nil {
			return 0, err
		}
	}
	n, err := r.file.Write(p)
	r.size += int64(n)
	return n, err
}
func (r *rotatingLog) Close() error { r.Lock(); defer r.Unlock(); return r.file.Close() }
func shutdown(server *http.Server) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_ = server.Shutdown(ctx)
}
