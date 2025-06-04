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
	configManager *config.ConfigManager
	db            database.Database
	r2Storage     *storage.R2Storage
	uploadService *service.UploadService
	videoRequestHandler  *BookingVideoRequestHandler
}

func NewServer(configManager *config.ConfigManager, db database.Database, r2Storage *storage.R2Storage, uploadService *service.UploadService) *Server {
	// Get current config from manager
	cfg := configManager.GetConfig()
	
	// Initialize video request handler
	videoRequestHandler := NewBookingVideoRequestHandler(cfg, db, r2Storage, uploadService)

	return &Server{
		configManager: configManager,
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
	// Get current config from manager
	currentConfig := s.configManager.GetConfig()
	
	portAddr := ":" + currentConfig.ServerPort
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
	// Get current config from manager
	currentConfig := s.configManager.GetConfig()
	
	// Static routes
	r.Static("/hls", filepath.Join(currentConfig.StoragePath, "hls"))
	// Serve dashboard static files
	r.Static("/dashboard", filepath.Join("dashboard"))
	r.GET("/dashboard", func(c *gin.Context) {
		c.File(filepath.Join("dashboard", "admin_dashboard.html"))
	})
	r.GET("/admin", func(c *gin.Context) {
		c.File(filepath.Join("dashboard", "admin_config.html"))
	})
	r.GET("/admin/cameras", func(c *gin.Context) {
		c.File(filepath.Join("dashboard", "admin_cameras.html"))
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
		
		// Config management endpoints
		api.GET("/config", s.GetConfig)
		api.POST("/config", s.UpdateConfig)
	}
}
