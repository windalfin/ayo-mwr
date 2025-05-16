package api

import (
	"fmt"
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
	videoRequestHandler  *BookingVideoRequestHandler
}

func NewServer(cfg config.Config, db database.Database, r2Storage *storage.R2Storage, uploadService *service.UploadService) *Server {
	// Initialize video request handler
	videoRequestHandler := NewBookingVideoRequestHandler(cfg, db, r2Storage, uploadService)

	return &Server{
		config:        cfg,
		db:            db,
		r2Storage:     r2Storage,
		uploadService: uploadService,
		videoRequestHandler:  videoRequestHandler,
	}
}

func (s *Server) Start() {
	r := gin.Default()
	s.setupCORS(r)
	s.setupRoutes(r)
	portAddr := ":" + s.config.ServerPort
	fmt.Printf("Starting API server on %s\n", portAddr)
	r.Run(portAddr)
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
	// Serve dashboard static files
	r.Static("/dashboard", filepath.Join("dashboard"))
	r.GET("/dashboard", func(c *gin.Context) {
		c.File(filepath.Join("dashboard", "admin_dashboard.html"))
	})

	// API routes
	api := r.Group("/api")
	{
		api.GET("/streams", s.listStreams)
		api.GET("/streams/:id", s.getStream)
		api.POST("/upload", s.handleUpload)
		api.GET("/cameras", s.listCameras)
		api.GET("/videos", s.listVideos)
		api.GET("/system_health", s.getSystemHealth)
		api.GET("/logs", s.getLogs)
		api.POST("/request-booking-video", s.videoRequestHandler.ProcessBookingVideo)
	}
}
