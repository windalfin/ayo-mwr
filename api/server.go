package api

import (
	"path/filepath"

	"ayo-mwr/config"
	"ayo-mwr/database"
	"ayo-mwr/service"
	"ayo-mwr/storage"

	"github.com/gin-gonic/gin"
)

type Server struct {
	config        config.Config
	db            database.Database
	r2Storage     *storage.R2Storage
	uploadService *service.UploadService
}

func NewServer(cfg config.Config, db database.Database, r2Storage *storage.R2Storage, uploadService *service.UploadService) *Server {
	return &Server{
		config:        cfg,
		db:            db,
		r2Storage:     r2Storage,
		uploadService: uploadService,
	}
}

func (s *Server) Start() {
	r := gin.Default()
	s.setupCORS(r)
	s.setupRoutes(r)
	r.Run()
}

func (s *Server) setupCORS(r *gin.Engine) {
	r.Use(func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type")
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		c.Next()
	})
}

func (s *Server) setupRoutes(r *gin.Engine) {
	// Static routes
	r.Static("/hls", filepath.Join(s.config.StoragePath, "hls"))

	// API routes
	api := r.Group("/api")
	{
		api.GET("/streams", s.listStreams)
		api.GET("/streams/:id", s.getStream)
		api.POST("/transcode", s.handleTranscode)
	}
}
