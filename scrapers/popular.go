package scrapers

import (
	"errors"
	"net/http"

	"github.com/gin-gonic/gin"
)

func GetPopular(c *gin.Context) {
	items, err := browseCache.get("popular", func() ([]catalogItem, error) {
		doc, base, err := loadDocument(source.String() + "/")
		if err != nil {
			return nil, err
		}
		results := parseCards(doc, base, ".popular-item-wrap", "h5 a")
		if len(results) == 0 {
			return nil, errors.New("MangaRead popular markup not recognized")
		}
		return results, nil
	})
	if err != nil {
		items, err = fallbackCatalog("popular")
	}
	if err != nil {
		reportError(c, err, "Could not load popular manga.")
		return
	}
	type popularItem struct {
		catalogItem
		Name string `json:"name"`
	}
	response := make([]popularItem, 0, len(items))
	for _, item := range items {
		response = append(response, popularItem{catalogItem: item, Name: item.Title})
	}
	c.JSON(http.StatusOK, response)
}
