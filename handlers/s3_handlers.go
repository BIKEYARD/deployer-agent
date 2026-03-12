package handlers

import (
	"context"
	"deployer-agent/config"
	s3client "deployer-agent/s3"
	"net/http"
	"time"

	"github.com/gin-gonic/gin"
)

// S3Status returns whether S3 is configured on this agent and the bucket name (safe to expose).
func S3Status(c *gin.Context) {
	cfg := config.GetConfig()
	configured := cfg.S3.IsConfigured() && s3client.IsConfigured()

	resp := gin.H{
		"configured": configured,
	}
	if configured {
		resp["bucket"] = cfg.S3.Bucket
		resp["region"] = cfg.S3.Region
	}

	c.JSON(http.StatusOK, resp)
}

type PresignUploadRequest struct {
	Key         string `json:"key" binding:"required"`
	ContentType string `json:"content_type"`
}

// S3PresignUpload generates a presigned PUT URL for uploading an object.
func S3PresignUpload(c *gin.Context) {
	client := s3client.GetClient()
	if client == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "S3 is not configured on this agent"})
		return
	}

	var req PresignUploadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": err.Error()})
		return
	}

	if req.ContentType == "" {
		req.ContentType = "application/octet-stream"
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	url, err := client.PresignPutObject(ctx, req.Key, req.ContentType, 15*time.Minute)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "Failed to generate upload URL"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"upload_url": url,
		"key":        req.Key,
		"bucket":     client.Bucket(),
	})
}

type PresignDownloadRequest struct {
	Key string `json:"key" binding:"required"`
}

// S3PresignDownload generates a presigned GET URL for downloading an object.
func S3PresignDownload(c *gin.Context) {
	client := s3client.GetClient()
	if client == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "S3 is not configured on this agent"})
		return
	}

	var req PresignDownloadRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	url, err := client.PresignGetObject(ctx, req.Key, 15*time.Minute)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "Failed to generate download URL"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"download_url": url,
	})
}

type HeadObjectRequest struct {
	Key string `json:"key" binding:"required"`
}

// S3HeadObject checks if an object exists and returns its size.
func S3HeadObject(c *gin.Context) {
	client := s3client.GetClient()
	if client == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "S3 is not configured on this agent"})
		return
	}

	var req HeadObjectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	size, err := client.HeadObject(ctx, req.Key)
	if err != nil {
		if err == s3client.ErrNotFound {
			c.JSON(http.StatusNotFound, gin.H{"detail": "Object not found", "exists": false})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "Failed to check object"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"exists": true,
		"size":   size,
	})
}

type DeleteObjectRequest struct {
	Key string `json:"key" binding:"required"`
}

// S3DeleteObject deletes an object from S3.
func S3DeleteObject(c *gin.Context) {
	client := s3client.GetClient()
	if client == nil {
		c.JSON(http.StatusServiceUnavailable, gin.H{"detail": "S3 is not configured on this agent"})
		return
	}

	var req DeleteObjectRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": err.Error()})
		return
	}

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	if err := client.DeleteObject(ctx, req.Key); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": "Failed to delete object"})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
	})
}
