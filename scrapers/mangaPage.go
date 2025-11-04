package scrapers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
)

type mangaPageItem struct {
	Image       string   `json:"image"`
	Name        string   `json:"name"`
	Description string   `json:"description"`
	Chapters    []string `json:"chapters"`
}

func GetMangaPageInfo(c *gin.Context) {
	var pageInfo mangaPageItem
	title := c.Query("title")
	chapURL := fmt.Sprintf("https://www.mangaread.org/manga/%s/", title)
	pageInfo.Name = title
	pageInfo.Chapters = getChapters(chapURL)
	pageInfo.Description, pageInfo.Image = GetDescriptionAndImage(chapURL)

	c.JSON(http.StatusOK, pageInfo)
}
