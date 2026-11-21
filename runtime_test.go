package main

import (
	"github.com/gin-gonic/gin"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestAuthenticationHealthAndClientLimits(t *testing.T) {
	gin.SetMode(gin.TestMode)
	config := appConfig{DeploymentID: "windows-home-fixture", MetadataPerMinute: 1, ImagePerMinute: 600, PDFPerMinute: 2, key: strings.Repeat("k", 64)}
	router := gin.New()
	router.Use(requestMiddleware(config))
	router.GET("/data", func(c *gin.Context) { c.JSON(200, gin.H{"ok": true}) })
	request := func(path, key, ip string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", path, nil)
		r.RemoteAddr = "192.0.2.1:1234"
		r.Header.Set("Authorization", key)
		r.Header.Set("X-AnyManga-Client-IP", ip)
		w := httptest.NewRecorder()
		router.ServeHTTP(w, r)
		return w
	}
	if w := request("/health", "", ""); w.Code != 204 || w.Header().Get("X-AnyManga-Backend-ID") != "windows-home-fixture" {
		t.Fatalf("health: %d", w.Code)
	}
	if w := request("/data", "Bearer wrong", "203.0.113.2"); w.Code != 401 {
		t.Fatal("invalid key accepted")
	}
	auth := "Bearer " + config.key
	if w := request("/data", auth, "203.0.113.1"); w.Code != 200 || w.Header().Get("X-AnyManga-Request-ID") == "" {
		t.Fatal("authenticated request failed")
	}
	if w := request("/data", auth, "203.0.113.1"); w.Code != 429 || w.Header().Get("Retry-After") == "" {
		t.Fatal("client limit missing")
	}
	if w := request("/data", auth, "203.0.113.2"); w.Code != 200 {
		t.Fatal("distinct authenticated client shared rate bucket")
	}
	if w := request("/admin/status", "", ""); w.Code != 401 {
		t.Fatal("metrics are public")
	}
}
func TestRateBucketBoundAndRefill(t *testing.T) {
	l := clientLimits{buckets: map[string]rateBucket{}}
	now := time.Now()
	if ok, _ := l.allow("x", 1, now); !ok {
		t.Fatal("initial token missing")
	}
	if ok, _ := l.allow("x", 1, now); ok {
		t.Fatal("burst exceeded")
	}
	if ok, _ := l.allow("x", 1, now.Add(time.Minute)); !ok {
		t.Fatal("token failed to refill")
	}
}
