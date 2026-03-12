package main

import (
	"deployer-agent/config"
	"deployer-agent/handlers"
	s3client "deployer-agent/s3"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"syscall"

	"github.com/gin-gonic/gin"
)

const AgentVersion = "1.1"

func main() {
	// Parse command line flags
	configPath := flag.String("config", "config.yaml", "Path to configuration file")
	flag.Parse()

	// Load configuration
	if err := config.LoadConfig(*configPath); err != nil {
		log.Fatalf("Failed to load config: %v", err)
	}

	cfg := config.GetConfig()

	// Log startup info
	log.Printf("🚀 Starting Go Deployer Agent v%s", AgentVersion)
	log.Printf("📍 Host: %s:%d", cfg.Host, cfg.Port)
	log.Printf("🔧 Debug mode: %v", cfg.Debug)

	// Initialize S3 client if configured
	if cfg.S3.IsConfigured() {
		if err := s3client.Init(&cfg.S3); err != nil {
			log.Printf("⚠️  S3 initialization failed: %v", err)
		} else {
			log.Printf("✅ S3 configured: bucket=%s region=%s", cfg.S3.Bucket, cfg.S3.Region)
		}
	} else {
		log.Printf("ℹ️  S3 not configured (optional)")
	}

	// Set Gin mode
	if !cfg.Debug {
		gin.SetMode(gin.ReleaseMode)
	}

	// Create router
	router := gin.Default()

	// CORS middleware
	router.Use(func(c *gin.Context) {
		c.Writer.Header().Set("Access-Control-Allow-Origin", "*")
		c.Writer.Header().Set("Access-Control-Allow-Credentials", "true")
		c.Writer.Header().Set("Access-Control-Allow-Headers", "Content-Type, Content-Length, Accept-Encoding, X-CSRF-Token, Authorization, accept, origin, Cache-Control, X-Requested-With, X-Deployer-Timestamp, X-Deployer-Nonce, X-Deployer-Content-SHA256, X-Deployer-Signature")
		c.Writer.Header().Set("Access-Control-Allow-Methods", "POST, OPTIONS, GET, PUT, DELETE")

		if c.Request.Method == "OPTIONS" {
			c.AbortWithStatus(204)
			return
		}

		c.Next()
	})

	// Protected routes
	protected := router.Group("/")
	protected.Use(handlers.AuthMiddleware())
	{
		protected.GET("/health", handlers.HealthCheck)
		protected.GET("/projects", handlers.ListProjects)
		protected.POST("/deploy", handlers.StartDeployment)
		protected.GET("/config/:project_id/:config_file_id", handlers.GetConfigFile)
		protected.POST("/config", handlers.UpdateConfigFile)
		protected.POST("/terminal/execute", handlers.ExecuteTerminalCommand)
		protected.GET("/crontab", handlers.GetCrontab)
		protected.POST("/crontab", handlers.UpdateCrontab)

		// S3 routes
		protected.GET("/s3/status", handlers.S3Status)
		protected.POST("/s3/presign-upload", handlers.S3PresignUpload)
		protected.POST("/s3/presign-download", handlers.S3PresignDownload)
		protected.POST("/s3/head-object", handlers.S3HeadObject)
		protected.POST("/s3/delete-object", handlers.S3DeleteObject)
	}

	// Handle shutdown signals
	quit := make(chan os.Signal, 1)
	signal.Notify(quit, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		<-quit
		log.Println("Shutting down server...")
		os.Exit(0)
	}()

	// Start server
	addr := fmt.Sprintf("%s:%d", cfg.Host, cfg.Port)
	log.Printf("Server listening on %s", addr)
	if err := router.Run(addr); err != nil {
		log.Fatalf("Failed to start server: %v", err)
	}
}
