package scrapers

import (
	"fmt"
	"net/http"
	"regexp"
	"strings"

	"github.com/gin-gonic/gin"
)

// Struct matching the frontend POST body
type ChapterRequest struct {
	Title   string `json:"manga"` // JSON key matches frontend
	Chapter string `json:"chapter"`
}

// Full handler with debug logs
func GetChapterPages(c *gin.Context) {
	var req ChapterRequest

	// Parse JSON body
	if err := c.BindJSON(&req); err != nil {
		fmt.Println("BindJSON failed:", err)
		c.JSON(http.StatusBadRequest, gin.H{"error": "invalid request"})
		return
	}
	fmt.Println("Received request:", req)

	// Clean and normalize manga title and chapter
	title := strings.ToLower(strings.TrimSpace(req.Title))
	ch := strings.ToLower(strings.TrimSpace(req.Chapter))
	re := regexp.MustCompile(`[\s-]+`)
	title = re.ReplaceAllString(title, "-")
	ch = re.ReplaceAllString(ch, "-")

	fmt.Println("Cleaned title:", title, "Cleaned chapter:", ch)

	// Construct URL for scraping
	chapURL := fmt.Sprintf("https://www.mangaread.org/manga/%s/%s/", title, ch)
	chapURL = CleanURL(chapURL) // assuming CleanURL exists
	fmt.Println("Constructed chapter URL:", chapURL)

	// Scrape the chapter
	chap, err := scrapeChapter(chapURL)
	if err != nil {
		fmt.Println("scrapeChapter error:", err)
		c.JSON(http.StatusInternalServerError, gin.H{"error": "failed to scrape chapter"})
		return
	}

	// Check if pages exist
	if len(chap.Pages) == 0 {
		fmt.Println("No pages found for chapter")
		c.JSON(http.StatusNotFound, gin.H{"error": "chapter has no pages"})
		return
	}

	// Debug: log pages found
	fmt.Println("Found pages:", chap.Pages)

	// Send JSON response
	c.JSON(http.StatusOK, gin.H{
		"images": chap.Pages,
	})
}
