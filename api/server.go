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

	"github.com/gin-contrib/sessions"
	"github.com/gin-contrib/sessions/cookie"
	"github.com/gin-gonic/gin"
)

type Server struct {
	config              *config.Config
	db                  database.Database
	r2Storage           *storage.R2Storage
	uploadService       *service.UploadService
	videoRequestHandler *BookingVideoRequestHandler
	chunkHandlers       *ChunkHandlers
	dashboardFS         embed.FS

	// Mutex untuk prevent concurrent uploads
	uploadMutex   sync.Mutex
	activeUploads map[string]bool // key: cameraName, value: isUploading
}

func NewServer(cfg *config.Config, db database.Database, r2Storage *storage.R2Storage, uploadService *service.UploadService, dashboardFS embed.FS) *Server {
	// Initialize video request handler
	videoRequestHandler := NewBookingVideoRequestHandler(cfg, db, r2Storage, uploadService)
	
	// Initialize chunk configuration service and handlers
	chunkConfigService := config.NewChunkConfigService(db)
	chunkHandlers := NewChunkHandlers(chunkConfigService, db)

	return &Server{
		config:              cfg,
		db:                  db,
		r2Storage:           r2Storage,
		uploadService:       uploadService,
		videoRequestHandler: videoRequestHandler,
		chunkHandlers:       chunkHandlers,
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
	// Setup session middleware with consistent secret key
	sessionSecret := "ayo-mwr-session-secret-key-fixed-2024"
	store := cookie.NewStore([]byte(sessionSecret))
	store.Options(sessions.Options{
		Path:     "/",
		MaxAge:   86400 * 7, // 7 days
		HttpOnly: true,
		Secure:   false, // Set to true in production with HTTPS
		SameSite: http.SameSiteLaxMode,
	})
	r.Use(sessions.Sessions("ayo-session", store))

	// Static routes - serve HLS files from the recordings directory
	r.Static("/hls", filepath.Join(s.config.StoragePath, "recordings"))
	
	// Static route for watermarks
	r.Static("/watermarks", filepath.Join(s.config.StoragePath, "watermarks"))

	// Authentication routes (no middleware)
	r.GET("/login", s.handleLogin)
	r.POST("/login", s.handleLogin)
	r.GET("/register", s.handleRegister)
	r.POST("/register", s.handleRegister)
	r.GET("/logout", s.handleLogout)

	// Root route - serve dashboard directly (protected)
	r.GET("/", s.AuthMiddleware(), func(c *gin.Context) {
		// Serve embedded dashboard static files
		dashboardHTTPFS, err := fs.Sub(s.dashboardFS, "dashboard")
		if err != nil {
			c.String(http.StatusInternalServerError, "Error loading dashboard")
			return
		}

		// Serve the admin dashboard HTML file directly
		file, err := dashboardHTTPFS.Open("admin_dashboard.html")
		if err != nil {
			c.String(http.StatusInternalServerError, "Dashboard not found")
			return
		}
		defer file.Close()

		c.Header("Content-Type", "text/html")
		c.DataFromReader(http.StatusOK, -1, "text/html", file, nil)
	})

	// Dashboard static assets (protected)
	dashboardGroup := r.Group("/dashboard", s.AuthMiddleware())
	{
		// Serve embedded dashboard static files
		dashboardHTTPFS, err := fs.Sub(s.dashboardFS, "dashboard")
		if err != nil {
			panic(fmt.Sprintf("failed to get dashboard subdirectory: %v", err))
		}

		// Serve all dashboard files
		dashboardGroup.StaticFS("/", http.FS(dashboardHTTPFS))
	}

	   // API routes
	   api := r.Group("/api")
	   {
			   // Public API endpoints (for external integrations and onboarding)
			   api.GET("/health", s.handleHealthCheck)
			   api.POST("/upload", s.handleUpload)
			   api.POST("/request-booking-video", s.videoRequestHandler.ProcessBookingVideo)
			   api.GET("/queue-status", s.videoRequestHandler.GetQueueStatus)

			   // Onboarding endpoints (public)
			   api.GET("/onboarding-status", s.getOnboardingStatus)
			   api.POST("/onboarding/venue-config", s.saveVenueConfig)
			   api.GET("/onboarding/camera-defaults", s.getCameraDefaults)
			   api.POST("/onboarding/first-camera", s.saveFirstCamera)

			   // All dashboard/admin endpoints are protected
			   dashboard := api.Group("", s.AuthMiddleware())
			   {
					   dashboard.GET("/streams", s.listStreams)
					   dashboard.GET("/arduino-status", s.getArduinoStatus)
					   dashboard.GET("/streams/:id", s.getStream)
					   dashboard.GET("/cameras", s.listCameras)
					   dashboard.GET("/videos", s.listVideos)
					   dashboard.GET("/system_health", s.getSystemHealth)
					   dashboard.GET("/logs", s.getLogs)

					   // Booking management endpoints (protected)
					   dashboard.GET("/bookings", s.getBookings)
					   dashboard.GET("/bookings/:booking_id", s.getBookingByID)
					   dashboard.GET("/bookings/status/:status", s.getBookingsByStatus)
					   dashboard.GET("/bookings/date/:date", s.getBookingsByDate)
			   }

			   // Admin endpoints for camera/system/disk config (protected)
			   admin := api.Group("/admin", s.AuthMiddleware())
			   {
					   admin.GET("/cameras-config", s.getCamerasConfig)
					   admin.PUT("/arduino-config", s.updateArduinoConfig)
					   admin.PUT("/cameras-config", s.updateCamerasConfig)
					   admin.POST("/cameras-config", s.updateCamerasConfig) // Add POST support for camera config
					   admin.POST("/reload-cameras", s.reloadCameras)

					   // System configuration endpoints
					   admin.GET("/system-config", s.getSystemConfig)
					   admin.PUT("/system-config", s.updateSystemConfig)

					   // Disk manager configuration endpoints
			admin.GET("/disk-manager-config", s.getDiskManagerConfig)
			admin.PUT("/disk-manager-config", s.updateDiskManagerConfig)

			// Chunk processing endpoints
			admin.GET("/chunk-config", s.chunkHandlers.GetChunkConfig)
			admin.PUT("/chunk-config", s.chunkHandlers.UpdateChunkConfig)
			admin.GET("/chunk-statistics", s.chunkHandlers.GetChunkStatistics)
			admin.POST("/chunk-processing/enable", s.chunkHandlers.EnableChunkProcessing)
			admin.POST("/chunk-processing/force", s.chunkHandlers.ForceChunkProcessing)
			admin.POST("/chunk-cleanup/force", s.chunkHandlers.ForceChunkCleanup)

			// Watermark endpoints
			admin.POST("/force-update-watermark", s.forceUpdateWatermark)
			   }
	   }
}
