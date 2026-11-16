package scrapers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
)

func GetFeaturedManga(c *gin.Context) {
	items, err := browseCache.get("featured", func() ([]catalogItem, error) {
		doc, base, err := loadDocument(source.String() + "/genres/manga/?m_orderby=views")
		if err != nil {
			return nil, err
		}
		results := parseCards(doc, base, ".page-item-detail.manga", searchTitle)
		if len(results) == 0 {
			return nil, errors.New("MangaRead featured markup not recognized")
		}
		return results, nil
	})
	if err != nil {
		reportError(c, err, "Could not load featured manga.")
		return
	}
	c.JSON(http.StatusOK, items)
}
