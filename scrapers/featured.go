package scrapers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
)

func GetFeaturedManga(c *gin.Context) {
	writeCatalog(c, "featured", "/genres/manga/?m_orderby=views", "Could not load featured manga.")
}

func GetBrowseCategory(c *gin.Context) {
	name := c.Query("name")
	switch name {
	case "manhwa", "manhua":
		writeCatalog(c, name, "/genres/"+name+"/", "Could not load this category.")
	default:
		c.JSON(http.StatusBadRequest, gin.H{"error": "Unknown browse category."})
	}
}

func writeCatalog(c *gin.Context, key, path, message string) {
	items, err := browseCache.get(key, func() ([]catalogItem, error) {
		doc, base, err := loadDocument(source.String() + path)
		if err != nil {
			return nil, err
		}
		results := parseCards(doc, base, ".page-item-detail.manga", searchTitle)
		if len(results) == 0 {
			return nil, errors.New("MangaRead catalog markup not recognized")
		}
		return results, nil
	})
	if err != nil {
		items, err = fallbackCatalog(key)
	}
	if err != nil {
		reportError(c, err, message)
		return
	}
	c.JSON(http.StatusOK, items)
}
