package scrapers

import (
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

type ChapterRequest struct {
	Title   string `json:"manga"`
	Chapter string `json:"chapter"`
}

func GetChapterPages(c *gin.Context) {
	req := ChapterRequest{Title: c.Query("manga"), Chapter: c.Query("chapter")}
	if c.Request.Method == http.MethodPost {
		if err := c.ShouldBindJSON(&req); err != nil {
			c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid chapter request."})
			return
		}
	}
	if strings.TrimSpace(req.Chapter) == "" || len(req.Chapter) > 400 {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid chapter identifier."})
		return
	}
	index := indexForRequest(c, req.Title)
	if index == nil {
		return
	}
	position, ok := index.lookup(req.Chapter)
	if !ok {
		c.JSON(http.StatusNotFound, gin.H{"error": "Chapter not found in this manga’s chapter list."})
		return
	}
	current := index.Entries[position]
	images, err := loadPages(current)
	if err != nil {
		reportError(c, err, "Could not load the chapter pages from MangaRead.")
		return
	}
	var previous, next *chapterLink
	if position > 0 {
		previous = &index.Entries[position-1]
	}
	if position+1 < len(index.Entries) {
		next = &index.Entries[position+1]
	}
	c.JSON(http.StatusOK, gin.H{
		"images": images, "manga": index.Name, "slug": index.Slug,
		"chapter": current, "previous": previous, "next": next,
	})
}
