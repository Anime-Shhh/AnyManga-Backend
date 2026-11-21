package scrapers

import (
	"bytes"
	"github.com/gin-gonic/gin"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
)

func TestStreamingImageValidationAndDeliveryPolicy(t *testing.T) {
	old := imageClient
	t.Cleanup(func() { imageClient = old })
	var data bytes.Buffer
	img := image.NewRGBA(image.Rect(0, 0, 3, 4))
	img.Set(1, 1, color.White)
	if err := png.Encode(&data, img); err != nil {
		t.Fatal(err)
	}
	imageClient = &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
		body := data.Bytes()
		if strings.Contains(r.URL.Path, "bad") {
			body = []byte("<html>blocked</html>")
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(body)), ContentLength: int64(len(body)), Request: r}, nil
	})}
	router := gin.New()
	router.GET("/image", GetProviderImage)
	for _, tc := range []struct {
		address string
		status  int
	}{{"https://scans-hot.planeptune.us/manga/fixture/1.png", 200}, {"https://scans-hot.planeptune.us/manga/fixture/bad.png", 502}, {"http://127.0.0.1/secret", 400}} {
		recorder := httptest.NewRecorder()
		router.ServeHTTP(recorder, httptest.NewRequest("GET", "/image?"+url.Values{"url": {tc.address}}.Encode(), nil))
		if recorder.Code != tc.status {
			t.Fatalf("%s returned %d", tc.address, recorder.Code)
		}
		if tc.status == 200 && !bytes.Equal(recorder.Body.Bytes(), data.Bytes()) {
			t.Fatal("stream bytes differ")
		}
	}
	if p, f := imageDelivery("https://www.mangaread.org/wp-content/uploads/fixture/1.png"); !strings.HasPrefix(p, "https:") || !strings.HasPrefix(f, "/api/manga/image?") {
		t.Fatal("direct source fallback missing")
	}
	if p, f := imageDelivery("https://uploads.mangadex.org/data/fixture/1.png"); !strings.HasPrefix(p, "/api/manga/image?") || f != "" {
		t.Fatal("MangaDex unexpectedly direct")
	}
	if p, f := imageDelivery("https://unknown.example/a.png"); p != "" || f != "" {
		t.Fatal("unknown image host permitted")
	}
}
