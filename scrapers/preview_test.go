package scrapers

import (
	"github.com/gin-gonic/gin"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestPreviewSuppressesNextChapterWarm(t *testing.T) {
	old := providerClient
	defer func() { providerClient = old }()
	warmed := make(chan struct{}, 1)
	providerClient = &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
		warmed <- struct{}{}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(`<div id="chapter-images"><img src="https://scans-hot.planeptune.us/manga/preview-fixture/1.jpg"></div>`)), Request: r}, nil
	})}
	index := &mangaIndex{Slug: "preview-prefetch-fixture", Language: "en", Name: "Preview", Entries: []chapterLink{{ID: "first", Number: "50.5"}, {ID: "second", Number: "51"}}, positions: map[string]int{"first": 0, "second": 1}, candidates: map[string][]sourceChapter{"second": {{Provider: "weebcentral", ID: "preview-warm-second", URL: "https://weebcentral.com/chapters/preview-warm-second"}}}}
	_, _ = unifiedCache.get(index.Slug+":en", func() (*mangaIndex, error) { return index, nil })
	_, _ = contentCache.get(index.Slug+":en:first:", func() (chapterContent, error) { return chapterContent{Images: []string{"/fixture.jpg"}}, nil })
	router := gin.New()
	router.GET("/chapterimages", GetChapterPages)
	for _, query := range []string{"&prefetch=0", ""} {
		w := httptest.NewRecorder()
		router.ServeHTTP(w, httptest.NewRequest("GET", "/chapterimages?manga="+index.Slug+"&chapter=first"+query, nil))
		if w.Code != 200 || !strings.Contains(w.Body.String(), `"number":"50.5"`) {
			t.Fatalf("response contract lost: %s", w.Body.String())
		}
		if query != "" {
			select {
			case <-warmed:
				t.Fatal("preview warmed next chapter")
			case <-time.After(80 * time.Millisecond):
			}
		} else {
			select {
			case <-warmed:
			case <-time.After(time.Second):
				t.Fatal("normal reader no longer warms next chapter")
			}
		}
	}
	// Wait for background loader completion before restoring its HTTP client.
	_, err := loadContent(index, index.Entries[1], "")
	if err != nil {
		t.Fatal(err)
	}
}
