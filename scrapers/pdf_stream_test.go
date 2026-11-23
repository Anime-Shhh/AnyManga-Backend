package scrapers

import (
	"bytes"
	"context"
	"fmt"
	"image"
	"image/color"
	"image/jpeg"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/gin-gonic/gin"
)

func pdfFixtureImages(t *testing.T) [][]byte {
	t.Helper()
	img := image.NewNRGBA(image.Rect(0, 0, 80, 120))
	for y := 0; y < 120; y++ {
		for x := 0; x < 80; x++ {
			img.Set(x, y, color.NRGBA{R: uint8(x * 3), G: uint8(y * 2), B: 80, A: 180})
		}
	}
	var jpg, pngData bytes.Buffer
	if err := jpeg.Encode(&jpg, img, nil); err != nil {
		t.Fatal(err)
	}
	if err := png.Encode(&pngData, img); err != nil {
		t.Fatal(err)
	}
	return [][]byte{jpg.Bytes(), pngData.Bytes()}
}

func TestStreamingPDFStructureAndPartialWrites(t *testing.T) {
	var output bytes.Buffer
	pdf := newStreamingPDF(&output)
	for _, data := range pdfFixtureImages(t) {
		if err := pdf.addPage(data); err != nil {
			t.Fatal(err)
		}
	}
	if output.Len() == 0 || bytes.Contains(output.Bytes(), []byte("%%EOF")) {
		t.Fatal("pages must be written before the document is finalized")
	}
	if err := pdf.finish(); err != nil {
		t.Fatal(err)
	}
	data := output.Bytes()
	if !bytes.HasSuffix(data, []byte("%%EOF\n")) || !bytes.Contains(data, []byte("/Count 2")) {
		t.Fatal("invalid final document")
	}
	for id, offset := range pdf.offsets[1:] {
		if !bytes.HasPrefix(data[offset:], []byte(fmt.Sprintf("%d 0 obj\n", id+1))) {
			t.Fatalf("invalid object offset %d", id+1)
		}
	}
	if path := os.Getenv("PDF_TEST_OUTPUT"); path != "" {
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
	bad := newStreamingPDF(shortPDFWriter{})
	if err := bad.addPage(pdfFixtureImages(t)[0]); err != io.ErrShortWrite {
		t.Fatalf("write error lost: %v", err)
	}
	if err := bad.finish(); err == nil {
		t.Fatal("finalized after failed write")
	}
}

type shortPDFWriter struct{}

func (shortPDFWriter) Write(p []byte) (int, error) { return len(p) - 1, nil }

func TestPDFEndpointStreamsBeforeLastImageAndHandlesFailure(t *testing.T) {
	for _, failure := range []bool{false, true} {
		t.Run(strconv.FormatBool(failure), func(t *testing.T) {
			oldImages := imageClient
			t.Cleanup(func() { imageClient = oldImages })
			fixture := pdfFixtureImages(t)
			entered, release := make(chan struct{}), make(chan struct{})
			defer close(release)
			imageClient = &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
				if strings.HasSuffix(r.URL.Path, "2.png") {
					close(entered)
					select {
					case <-release:
					case <-r.Context().Done():
						return nil, r.Context().Err()
					}
					if failure {
						return nil, fmt.Errorf("upstream failed")
					}
				}
				i := 0
				if strings.HasSuffix(r.URL.Path, "2.png") {
					i = 1
				}
				return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(fixture[i])), Request: r}, nil
			})}
			slug := "pdf-stream-" + strconv.FormatBool(failure)
			index := &mangaIndex{Slug: slug, Name: slug, Language: "en", Entries: []chapterLink{{ID: "chapter-1"}}, positions: map[string]int{"chapter-1": 0}, candidates: map[string][]sourceChapter{}}
			_, _ = unifiedCache.get(slug+":en", func() (*mangaIndex, error) { return index, nil })
			_, _ = contentCache.get(slug+":en:chapter-1:", func() (chapterContent, error) {
				return chapterContent{Images: []string{"https://www.mangaread.org/wp-content/uploads/test/1.jpg", "https://www.mangaread.org/wp-content/uploads/test/2.png"}}, nil
			})
			router := gin.New()
			router.POST("/chapters", GetAllChapters)
			server := httptest.NewServer(router)
			defer server.Close()
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			req, _ := http.NewRequestWithContext(ctx, "POST", server.URL+"/chapters", strings.NewReader(`{"title":"`+slug+`","chapters":["chapter-1"]}`))
			req.Header.Set("Content-Type", "application/json")
			resp, err := http.DefaultClient.Do(req)
			if err != nil {
				t.Fatal(err)
			}
			defer resp.Body.Close()
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal("second image never requested")
			}
			prefix := make([]byte, 1024)
			if _, err := io.ReadFull(resp.Body, prefix); err != nil {
				t.Fatal(err)
			}
			if !bytes.HasPrefix(prefix, []byte("%PDF-")) {
				t.Fatal("no PDF bytes received while second image is blocked")
			}
			// Unblock only after the HTTP client has observed actual first-page bytes.
			release <- struct{}{}
			rest, err := io.ReadAll(resp.Body)
			if err != nil {
				t.Fatal(err)
			}
			complete := bytes.HasSuffix(rest, []byte("%%EOF\n"))
			if complete == failure {
				t.Fatalf("incorrect completion marker: failure=%v", failure)
			}
			if bytes.Contains(rest, []byte(`{"error"`)) {
				t.Fatal("JSON appended inside PDF")
			}
		})
	}
}

func TestPDFImageBudgetAndCancellation(t *testing.T) {
	old := imageClient
	defer func() { imageClient = old }()
	data := pdfFixtureImages(t)[0]
	imageClient = &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) {
		return &http.Response{StatusCode: 200, Body: io.NopCloser(bytes.NewReader(data)), Request: r}, nil
	})}
	budget := 1
	if _, err := downloadPDFImage(httptest.NewRequest("GET", "/", nil), "https://www.mangaread.org/wp-content/uploads/test.jpg", &budget); err == nil {
		t.Fatal("source budget ignored")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	imageClient = &http.Client{Transport: testTransport(func(r *http.Request) (*http.Response, error) { return nil, r.Context().Err() })}
	budget = 64 << 20
	if _, err := downloadPDFImage(httptest.NewRequest("GET", "/", nil).WithContext(ctx), "https://www.mangaread.org/wp-content/uploads/test.jpg", &budget); err == nil {
		t.Fatal("cancellation ignored")
	}
}
