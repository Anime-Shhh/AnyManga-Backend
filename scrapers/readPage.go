package scrapers

import (
	"fmt"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

// Struct matching the frontend POST body
type ChapterRequest struct {
	Title   string `json:"manga"` // JSON key matches frontend
	Chapter string `json:"chapter"`
}

func GetChapterPages(c *gin.Context) {
	var req ChapterRequest

	// Parse JSON body
	if err := c.BindJSON(&req); err != nil {
		fmt.Println("BindJSON failed:", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	fmt.Println("Received request:", req)

	// Clean manga title and chapter
	title := strings.ToLower(strings.TrimSpace(req.Title))
	ch := strings.ToLower(strings.TrimSpace(req.Chapter))

	fmt.Println("Original title:", title, "Original chapter:", ch)

	// Construct URL
	chapURL := fmt.Sprintf("https://www.mangaread.org/manga/%s/%s/", title, ch)
	chapURL = CleanURL(chapURL)
	fmt.Println("Constructed chapter URL:", chapURL)

	// Scrape chapter
	chap, err := scrapeChapter(chapURL)
	if err != nil {
		fmt.Println("scrapeChapter error:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to scrape chapter"})
		return
	}

	// Debug: log pages found
	fmt.Println("Found pages:", chap.Pages)

	// Return JSON
	c.JSON(http.StatusOK, gin.H{
		"images": chap.Pages,
	})
}
