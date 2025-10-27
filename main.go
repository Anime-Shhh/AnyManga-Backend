package main

import (
	"manga-scraper/scrapers"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

func main() {
	// Initialize Gin
	r := gin.Default()

	// Enable CORS so your Next.js frontend can call this API
	r.Use(cors.Default())

	// Routes
	r.GET("/info", scrapers.GetMangaInfo)
	r.POST("/chapters", scrapers.GetAllChapters)

	// Start the server on port 8080
	r.Run("localhost:8080")
}
