package scrapers

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/gin-gonic/gin"
)

type testTransport func(*http.Request) (*http.Response, error)

func (transport testTransport) RoundTrip(req *http.Request) (*http.Response, error) {
	return transport(req)
}

func TestChapterReaderUsesSeriesLinksAndCachesPages(t *testing.T) {
	const series = "/manga/reader-test/"
	const named = "chapter-1-the-beginning"
	requests := map[string]int{}
	original := upstream
	t.Cleanup(func() { upstream = original })
	upstream = &http.Client{Transport: testTransport(func(req *http.Request) (*http.Response, error) {
		path := req.URL.Path
		requests[path]++
		var html string
		switch path {
		case series:
			html = `<div class="post-title"><h1>Reader Test</h1></div>
				<div class="summary__content"><p>Chapter links fixture</p></div>
				<ul>
				<li class="wp-manga-chapter"><a href="/manga/reader-test/special/">Special Chapter</a></li>
				<li class="wp-manga-chapter"><a href="/manga/reader-test/chapter-2-5/">Chapter 2.5: A side story</a></li>
				<li class="wp-manga-chapter"><a href="/manga/reader-test/chapter-2/">Chapter 2</a></li>
				<li class="wp-manga-chapter"><a href="/manga/reader-test/chapter-1-the-beginning/">Chapter 1 [The Beginning]</a></li>
				</ul>`
		case series + named + "/", series + "chapter-2/", series + "special/":
			html = `<img class="wp-manga-chapter-img" src="data:image/gif;base64,AA==" data-src="/pages/1.jpg">
				<img class="wp-manga-chapter-img" src=" /pages/2.jpg ">`
		default:
			t.Fatalf("unexpected upstream request for %s", path)
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(html)), Request: req}, nil
	})}

	router := gin.New()
	router.GET("/chapterimages", GetChapterPages)
	router.POST("/chapterimages", GetChapterPages)
	router.GET("/mangapage", GetMangaPageInfo)

	type response struct {
		Images         []string
		Chapter        chapterLink
		Previous, Next *chapterLink
	}
	getChapter := func(method, address, body string) response {
		t.Helper()
		recorder := httptest.NewRecorder()
		req := httptest.NewRequest(method, address, strings.NewReader(body))
		req.Header.Set("Content-Type", "application/json")
		router.ServeHTTP(recorder, req)
		if recorder.Code != 200 {
			t.Fatalf("chapter endpoint returned %d: %s", recorder.Code, recorder.Body)
		}
		var data response
		if err := json.Unmarshal(recorder.Body.Bytes(), &data); err != nil {
			t.Fatal(err)
		}
		return data
	}

	first := getChapter("GET", "/chapterimages?source=mangaread&manga=reader-test&chapter=chapter-1", "")
	if first.Chapter.ID != named || first.Chapter.Title != "Chapter 1 [The Beginning]" || first.Previous != nil ||
		first.Next == nil || first.Next.ID != "chapter-2" || len(first.Images) != 2 ||
		first.Images[0] != "https://www.mangaread.org/pages/1.jpg" {
		t.Fatalf("unexpected first chapter response: %+v", first)
	}

	next := getChapter("GET", "/chapterimages?source=mangaread&manga=reader-test&chapter=chapter-2", "")
	if next.Previous == nil || next.Previous.ID != named || next.Next == nil || next.Next.ID != "chapter-2-5" {
		t.Fatalf("unexpected neighbors for chapter 2: %+v", next)
	}
	last := getChapter("GET", "/chapterimages?source=mangaread&manga=reader-test&chapter=special", "")
	if last.Next != nil || last.Previous == nil || last.Previous.ID != "chapter-2-5" {
		t.Fatalf("unexpected neighbors for special chapter: %+v", last)
	}

	legacy := getChapter("POST", "/chapterimages?source=mangaread", `{"manga":"reader-test","chapter":"chapter-1-[the-beginning]"}`)
	if legacy.Chapter.ID != first.Chapter.ID || requests[series] != 1 || requests[series+named+"/"] != 1 {
		t.Fatalf("legacy request missed the cached chapter: %+v, upstream requests %v", legacy, requests)
	}
}

func TestSearchUsesUpstreamOrderAndPagination(t *testing.T) {
	original := upstream
	t.Cleanup(func() { upstream = original })
	requests := 0
	upstream = &http.Client{Transport: testTransport(func(req *http.Request) (*http.Response, error) {
		requests++
		params := req.URL.Query()
		if params.Get("s") != "A & B" || params.Get("post_type") != "wp-manga" {
			t.Fatalf("unexpected upstream search parameters: %s", req.URL.RawQuery)
		}
		html := `<div class="search-wrap"><h1>3 results for A &amp; B</h1></div>`
		titles := [][2]string{{"z-original-slug", "Z: A &amp; B"}, {"a-different-slug", "A: A &amp; B"}}
		if params.Get("paged") == "2" {
			titles = [][2]string{{"last", "Another match"}}
		}
		for _, item := range titles {
			html += `<div class="c-tabs-item__content"><div class="post-title"><h3><a href="/manga/` +
				item[0] + `/">` + item[1] + `</a></h3></div></div>`
		}
		return &http.Response{StatusCode: 200, Body: io.NopCloser(strings.NewReader(html)), Request: req}, nil
	})}

	first, err := cachedSearch("A & B", 1)
	if err != nil || len(first.Results) != 2 || first.Results[0].Slug != "z-original-slug" ||
		first.Results[0].Title != "Z: A & B" || first.Results[1].Slug != "a-different-slug" || !first.HasMore {
		t.Fatalf("wrong first page: %+v, %v", first, err)
	}
	second, err := cachedSearch("A & B", 2)
	if err != nil || len(second.Results) != 1 || second.Results[0].Slug != "last" || second.HasMore ||
		second.Total != 3 || requests != 2 {
		t.Fatalf("wrong second page: %+v, %v, requests=%d", second, err, requests)
	}
}
