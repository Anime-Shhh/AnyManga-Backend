package scrapers

import (
	"fmt"
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/gocolly/colly"
)

type item struct {
	Image    string   `json:"image"`
	Name     string   `json:"name"`
	Chapters []string `json:"chapters"`
}

func GetPopular(c *gin.Context) {
	col := colly.NewCollector(colly.AllowedDomains("mangaread.org", "www.mangaread.org"))

	var items []item

	col.OnHTML("div.popular-item-wrap", func(e *colly.HTMLElement) {
		fmt.Println("Found popular item block")

		currItem := item{}
		currItem.Name = e.ChildText("h5 a")
		fmt.Println("Name:", currItem.Name)

		url := e.ChildAttr("h5 a", "href")
		fmt.Println("URL:", url)

		currItem.Image = getCoverImage(url)
		fmt.Println("Image:", currItem.Image)

		e.ForEach("div.chapter-item span a", func(_ int, el *colly.HTMLElement) {
			currItem.Chapters = append(currItem.Chapters, el.Text)
		})
		fmt.Println("Chapters:", currItem.Chapters)

		items = append(items, currItem)
	})
	col.Visit("https://www.mangaread.org/")
	c.JSON(http.StatusOK, items)
}
