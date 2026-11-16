package scrapers

import (
	"errors"
	"net/http"
	"strings"

	"github.com/gin-gonic/gin"
)

func reportError(c *gin.Context, err error, message string) {
	if errors.Is(err, missing) {
		c.JSON(http.StatusNotFound, gin.H{"error": message})
		return
	}
	_ = c.Error(err)
	c.JSON(http.StatusBadGateway, gin.H{"error": message})
}

func validSlug(title string) (string, bool) {
	slug := strings.ToLower(strings.Join(strings.Fields(title), "-"))
	return slug, mangaSlug.MatchString(slug)
}

func indexForRequest(c *gin.Context, title string) *mangaIndex {
	slug, valid := validSlug(title)
	if !valid {
		c.JSON(http.StatusBadRequest, gin.H{"error": "Invalid manga identifier."})
		return nil
	}
	index, err := getIndex(slug)
	if err != nil {
		reportError(c, err, "Could not load this manga from MangaRead.")
		return nil
	}
	return index
}

func GetMangaPageInfo(c *gin.Context) {
	if index := indexForRequest(c, c.Query("title")); index != nil {
		c.JSON(http.StatusOK, index)
	}
}

func GetMangaInfo(c *gin.Context) {
	if index := indexForRequest(c, c.Query("title")); index != nil {
		c.JSON(http.StatusOK, gin.H{"cover": index.Image, "chapters": index.Chapters})
	}
}
