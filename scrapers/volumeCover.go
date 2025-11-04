package scrapers

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
	"github.com/gocolly/colly"
)

func Reverse[T any](s []T) {
	for i, j := 0, len(s)-1; i < j; i, j = i+1, j-1 {
		s[i], s[j] = s[j], s[i]
	}
}

func getCoverImage(url string) string {
	col := colly.NewCollector(colly.AllowedDomains("mangaread.org", "www.mangaread.org"))

	var bookCover string
	col.OnHTML("div.summary_image a", func(e *colly.HTMLElement) {
		bookCover = e.ChildAttr("img", "src")
	})

	col.Visit(url)
	col.Wait()
	return bookCover
}

func GetMangaInfo(c *gin.Context) {
	var bookCover string
	var chapters []string
	col := colly.NewCollector(colly.AllowedDomains("mangaread.org", "www.mangaread.org"))

	title := c.Query("title")
	title = strings.ReplaceAll(strings.ToLower(strings.TrimSpace(title)), " ", "-")

	if title == "" {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Missing title"})
		return
	}

	chapURL := fmt.Sprintf("https://www.mangaread.org/manga/%s/", title)

	col.OnHTML("div.summary_image a", func(e *colly.HTMLElement) {
		bookCover = e.ChildAttr("img", "src")
	})

	col.OnHTML("li.wp-manga-chapter a", func(e *colly.HTMLElement) {
		chapter := e.Text
		if chapter != "" {
			chapters = append(chapters, chapter)
		}
	})

	col.Visit(chapURL)

	if bookCover == "" && len(chapters) == 0 {
		c.JSON(http.StatusNotFound, gin.H{"error": "No manga info found"})
		return
	}

	Reverse(chapters)

	c.JSON(http.StatusOK, gin.H{
		"cover":    bookCover,
		"chapters": chapters,
	})
}
