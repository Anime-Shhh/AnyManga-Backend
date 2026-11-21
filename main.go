package main

import (
	"errors"
	"flag"
	"log"
	"manga-scraper/scrapers"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"

	"github.com/gin-contrib/cors"
	"github.com/gin-gonic/gin"
)

func main() {
	configPath := flag.String("config", "", "Path to protected deployment JSON")
	serviceAction := flag.String("service", "", "Windows service action: install or start")
	flag.Parse()
	if handled, err := serviceCommand(*configPath, *serviceAction); handled {
		if err != nil {
			log.Fatal(err)
		}
		return
	}
	config, err := loadConfig(*configPath)
	if err != nil {
		log.Fatal(err)
	}
	if config.LogDirectory != "" {
		output, err := openLog(config.LogDirectory)
		if err != nil {
			log.Fatal(err)
		}
		defer output.Close()
		log.SetOutput(output)
		gin.DefaultWriter = output
		gin.DefaultErrorWriter = output
	}
	gin.SetMode(gin.ReleaseMode)
	r := gin.New()
	r.Use(gin.Recovery(), requestMiddleware(config))
	_ = r.SetTrustedProxies(nil)
	capacity := make(chan struct{}, 48)
	r.Use(func(c *gin.Context) {
		select {
		case capacity <- struct{}{}:
			defer func() { <-capacity }()
			c.Next()
		default:
			c.Header("Retry-After", "2")
			c.AbortWithStatus(http.StatusServiceUnavailable)
		}
	})
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

	server := &http.Server{
		Addr: config.Address, Handler: r, ReadHeaderTimeout: 5 * time.Second, ReadTimeout: 15 * time.Second,
		IdleTimeout: 60 * time.Second, WriteTimeout: 65 * time.Second,
	}
	if handled, err := runPlatformService(server); handled {
		if err != nil {
			log.Fatal(err)
		}
		return
	}
	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)
	go func() { <-stop; shutdown(server) }()
	log.Printf("backend_id=%s commit=%s address=%s", config.DeploymentID, buildCommit, config.Address)
	if err := server.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
		log.Fatal(err)
	}
}

func registerRoutes(routes gin.IRouter) {
	routes.GET("/health", func(c *gin.Context) { c.Status(http.StatusNoContent) })
	routes.GET("/image", scrapers.GetProviderImage)
	routes.GET("/info", scrapers.GetMangaInfo)
	routes.POST("/chapters", scrapers.GetAllChapters)
	routes.GET("/featured", scrapers.GetFeaturedManga)
	routes.GET("/popular", scrapers.GetPopular)
	routes.GET("/category", scrapers.GetBrowseCategory)
	routes.GET("/search", scrapers.Search)
	routes.GET("/mangapage", scrapers.GetMangaPageInfo)
	routes.GET("/chapterimages", scrapers.GetChapterPages)
	routes.POST("/chapterimages", scrapers.GetChapterPages)

}
