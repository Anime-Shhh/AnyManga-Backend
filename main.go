package main

import (
	"log"
	"manga-scraper/scrapers"
	"net/http"
	"os"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

func main() {
	r := gin.Default()
	if origin := os.Getenv("FRONTEND_ORIGIN"); origin != "" {
		config := cors.DefaultConfig()
		config.AllowOrigins = []string{origin}
		r.Use(cors.New(config))
	}
	r.MaxMultipartMemory = 1 << 20
	r.Use(func(c *gin.Context) {
		c.Request.Body = http.MaxBytesReader(c.Writer, c.Request.Body, 16<<10)
		c.Next()
	})

	registerRoutes(r)
	registerRoutes(r.Group("/api/manga"))

	address := "127.0.0.1:8080"
	if port := os.Getenv("PORT"); port != "" {
		address = ":" + port
	}
	server := &http.Server{
		Addr: address, Handler: r, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second,
		IdleTimeout: 60 * time.Second, WriteTimeout: 65 * time.Second,
	}
	log.Fatal(server.ListenAndServe())
}

func registerRoutes(routes gin.IRouter) {
	routes.GET("/health", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	routes.GET("/info", scrapers.GetMangaInfo)
	routes.POST("/chapters", scrapers.GetAllChapters)
	routes.GET("/featured", scrapers.GetFeaturedManga)
	routes.GET("/popular", scrapers.GetPopular)
	routes.GET("/search", scrapers.Search)
	routes.GET("/mangapage", scrapers.GetMangaPageInfo)
	routes.GET("/chapterimages", scrapers.GetChapterPages)
	routes.POST("/chapterimages", scrapers.GetChapterPages)

}
