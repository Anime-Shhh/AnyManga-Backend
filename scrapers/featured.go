package scrapers

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gocolly/colly"
)

type featuredItem struct {
	Title       string `json:"title"`
	Description string `json:"description"`
	Image       string `json:"image"`
}

func GetDescriptionAndImage(url string) (string, string) {
	col := colly.NewCollector(
		colly.AllowedDomains("mangaread.org", "www.mangaread.org"),
	)
	var description string
	var image string

	col.OnHTML(`div[class*="summary__content"] p`, func(e *colly.HTMLElement) {
		description = e.Text
	})

	col.OnHTML("div.summary_image a", func(e *colly.HTMLElement) {
		image = e.ChildAttr("img", "src")
	})

	col.Visit(url)
	return description, image
}

func GetFeaturedManga(c *gin.Context) {
	var featuredList []featuredItem
	col := colly.NewCollector(
		colly.AllowedDomains("mangaread.org", "www.mangaread.org"),
	)

	col.OnHTML("div.page-item-detail.manga", func(e *colly.HTMLElement) {
		var item featuredItem

		item.Title = e.ChildText("h3 a")

		link := e.ChildAttr("h3 a", "href")
		item.Description, item.Image = GetDescriptionAndImage(link)

		featuredList = append(featuredList, item)
	})

	url := "https://www.mangaread.org/genres/manga/?m_orderby=views"
	col.Visit(url)

	c.JSON(http.StatusOK, featuredList)
}
