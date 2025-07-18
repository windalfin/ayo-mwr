package api

import (
	"embed"
	"fmt"
	"io/fs"
	"net/http"
	"path/filepath"
	"sync"

	"ayo-mwr/config"
	"ayo-mwr/database"
	"ayo-mwr/service"
	"ayo-mwr/storage"

	"github.com/gin-gonic/gin"
)

type Server struct {
	config              *config.Config
	db                  database.Database
	r2Storage           *storage.R2Storage
	uploadService       *service.UploadService
	videoRequestHandler *BookingVideoRequestHandler
	dashboardFS         embed.FS
	
	// Mutex untuk prevent concurrent uploads
	uploadMutex     sync.Mutex
	activeUploads   map[string]bool // key: cameraName, value: isUploading
}

func NewServer(cfg *config.Config, db database.Database, r2Storage *storage.R2Storage, uploadService *service.UploadService, dashboardFS embed.FS) *Server {
	// Initialize video request handler
	videoRequestHandler := NewBookingVideoRequestHandler(cfg, db, r2Storage, uploadService)

	return &Server{
		config:              cfg,
		db:                  db,
		r2Storage:           r2Storage,
		uploadService:       uploadService,
		videoRequestHandler: videoRequestHandler,
		dashboardFS:         dashboardFS,
		activeUploads:       make(map[string]bool),
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
		c.Writer.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}
		c.Next()
	})
}

func (s *Server) setupRoutes(r *gin.Engine) {
	// Static routes - serve HLS files from the recordings directory
	r.Static("/hls", filepath.Join(s.config.StoragePath, "recordings"))

	// Create a route group for the dashboard
	dashboard := r.Group("/dashboard")
	{
		// Serve embedded dashboard static files
		dashboardHTTPFS, err := fs.Sub(s.dashboardFS, "dashboard")
		if err != nil {
			panic(fmt.Sprintf("failed to get dashboard subdirectory: %v", err))
		}

		// Handle root dashboard path
		dashboard.GET("", func(c *gin.Context) {
			c.Redirect(http.StatusMovedPermanently, "/dashboard/admin_dashboard.html")
		})

		// Serve all other dashboard files
		dashboard.StaticFS("/", http.FS(dashboardHTTPFS))
	}

	// API routes
	api := r.Group("/api")
	{
		api.GET("/streams", s.listStreams)
        api.GET("/arduino-status", s.getArduinoStatus)
		api.GET("/streams/:id", s.getStream)
		api.POST("/upload", s.handleUpload)
		api.GET("/cameras", s.listCameras)
		api.GET("/videos", s.listVideos)
		api.GET("/system_health", s.getSystemHealth)
		api.GET("/logs", s.getLogs)
		api.POST("/request-booking-video", s.videoRequestHandler.ProcessBookingVideo)
		api.GET("/queue-status", s.videoRequestHandler.GetQueueStatus)
		
		// Booking management endpoints
		api.GET("/bookings", s.getBookings)
		api.GET("/bookings/:booking_id", s.getBookingByID)
		api.GET("/bookings/status/:status", s.getBookingsByStatus)
		api.GET("/bookings/date/:date", s.getBookingsByDate)
		
		// Admin endpoints for camera configuration
		admin := api.Group("/admin")
		{
			admin.GET("/cameras-config", s.getCamerasConfig)
            admin.PUT("/arduino-config", s.updateArduinoConfig)
			admin.PUT("/cameras-config", s.updateCamerasConfig)
			admin.POST("/reload-cameras", s.reloadCameras)
			
			// System configuration endpoints
			admin.GET("/system-config", s.getSystemConfig)
			admin.PUT("/system-config", s.updateSystemConfig)
			
			// Disk manager configuration endpoints
			admin.GET("/disk-manager-config", s.getDiskManagerConfig)
			admin.PUT("/disk-manager-config", s.updateDiskManagerConfig)
		}
	}
}
