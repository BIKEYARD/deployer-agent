package handlers

import (
	"crypto/rand"
	"deployer-agent/config"
	"deployer-agent/deploy"
	"deployer-agent/security"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
)

const AgentVersion = "1.1"

// Deployment data store
type DeploymentData struct {
	FullDeploymentID string
	ProjectID        string
	Branch           string
	Stand            string
	UserID           interface{}
	EnvID            interface{}
	Timestamp        int64
}

var (
	deploymentDataStore  = make(map[string]*DeploymentData)
	deploymentDataMutex  sync.RWMutex
	deploymentLocks      = make(map[string]*sync.Mutex)
	deploymentLocksMutex sync.Mutex
)

func generateUniqueID() string {
	bytes := make([]byte, 8)
	rand.Read(bytes)
	return hex.EncodeToString(bytes)
}

func getDeploymentLock(key string) *sync.Mutex {
	deploymentLocksMutex.Lock()
	defer deploymentLocksMutex.Unlock()

	if _, exists := deploymentLocks[key]; !exists {
		deploymentLocks[key] = &sync.Mutex{}
	}
	return deploymentLocks[key]
}

func isDeploymentLocked(key string) bool {
	deploymentLocksMutex.Lock()
	defer deploymentLocksMutex.Unlock()

	lock, exists := deploymentLocks[key]
	if !exists {
		return false
	}

	// Try to lock without blocking
	locked := lock.TryLock()
	if locked {
		lock.Unlock()
		return false
	}
	return true
}

func sendDeployWebhook(deployID, codename, status string, output *string) (int, error) {
	cfg := config.GetConfig()

	uriPath := fmt.Sprintf("/api/v1/deploy/%s/webhook", deployID)
	url := fmt.Sprintf("%s%s", cfg.DeployerURL, uriPath)

	data := map[string]interface{}{
		"codename": codename,
		"status":   status,
		"output":   output,
	}

	jsonData, err := json.Marshal(data)
	if err != nil {
		return 0, err
	}

	if cfg.Debug {
		log.Printf("[DEBUG webhook] URL=%s uriPath=%s keyLen=%d bodyLen=%d", url, uriPath, len(cfg.AgentToAPISigningKey), len(jsonData))
	}

	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(string(jsonData)))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	if cfg.AgentToAPISigningKey != "" {
		if err := security.SignOutgoingRequest(req, jsonData, cfg.AgentToAPISigningKey, uriPath); err != nil {
			return 0, err
		}
		if cfg.Debug {
			sig := req.Header.Get("X-Deployer-Signature")
			shortSig := sig
			if len(shortSig) > 20 {
				shortSig = shortSig[:20] + "..."
			}
			log.Printf("[DEBUG webhook] Headers: ts=%s nonce=%s sha=%s sig=%s",
				req.Header.Get("X-Deployer-Timestamp"),
				req.Header.Get("X-Deployer-Nonce"),
				req.Header.Get("X-Deployer-Content-SHA256"),
				shortSig)
		}
	}

	client := &http.Client{Timeout: 30 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		body, _ := io.ReadAll(resp.Body)
		if cfg.Debug {
			log.Printf("[DEBUG webhook] Response status=%d body=%s", resp.StatusCode, string(body))
		}
	}

	return resp.StatusCode, nil
}

// AuthMiddleware checks request authorization
func AuthMiddleware() gin.HandlerFunc {
	return func(c *gin.Context) {
		cfg := config.GetConfig()
		if cfg.APIToAgentSigningKey == "" {
			security.LogHMACReject("missing api_to_agent_signing_key")
			c.JSON(http.StatusForbidden, gin.H{"detail": "Unauthorized"})
			c.Abort()
			return
		}

		rawBody, err := security.ReadRawBodyPreserve(c.Request)
		if err != nil {
			security.LogHMACReject("read body")
			c.JSON(http.StatusUnauthorized, gin.H{"detail": "Unauthorized"})
			c.Abort()
			return
		}
		_, status, verr := security.VerifyIncomingRequest(c.Request, rawBody, cfg.APIToAgentSigningKey, c.Request.URL.Path)
		if verr != nil {
			security.LogHMACReject(verr.Error())
			if status == 0 {
				status = http.StatusUnauthorized
			}
			c.JSON(status, gin.H{"detail": "Unauthorized"})
			c.Abort()
			return
		}
		c.Next()
	}
}

// Health check endpoint
func HealthCheck(c *gin.Context) {
	c.JSON(http.StatusOK, gin.H{
		"status":  "healthy",
		"version": AgentVersion,
	})
}

// List projects endpoint
func ListProjects(c *gin.Context) {
	cfg := config.GetConfig()
	projects := make(map[string]interface{})

	for projectID, project := range cfg.Projects {
		configFiles := make([]map[string]interface{}, 0)
		for idx, cf := range project.ConfigFiles {
			configFiles = append(configFiles, map[string]interface{}{
				"id":       strconv.Itoa(idx),
				"name":     cf.Name,
				"path":     cf.Path,
				"editable": cf.Editable,
			})
		}

		projects[projectID] = map[string]interface{}{
			"id":           projectID,
			"name":         project.Name,
			"type":         project.Type,
			"config_files": configFiles,
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"projects":                 projects,
		"config_editing_enabled":   cfg.ConfigEditingEnabled,
		"terminal_enabled":         cfg.TerminalEnabled,
	})
}

// Terminal info endpoint — exposes allowed/forbidden commands so the UI can hint users.
func GetTerminalInfo(c *gin.Context) {
	cfg := config.GetConfig()
	c.JSON(http.StatusOK, gin.H{
		"enabled":              cfg.TerminalEnabled,
		"allowed_commands":     cfg.TerminalSecurity.AllowedCommands,
		"forbidden_commands":   cfg.TerminalSecurity.ForbiddenCommands,
		"max_command_length":   cfg.TerminalSecurity.MaxCommandLength,
		"allow_arguments":      cfg.TerminalSecurity.AllowArguments,
		"block_command_chains": cfg.TerminalSecurity.BlockCommandChains,
	})
}

// Deploy request model
type DeploymentRequest struct {
	DeployID  string      `json:"deploy_id" binding:"required"`
	ProjectID string      `json:"project_id" binding:"required"`
	Branch    string      `json:"branch" binding:"required"`
	Stand     string      `json:"stand" binding:"required"`
	UserID    interface{} `json:"user_id"`
	EnvID     interface{} `json:"env_id"`
}

// Start deployment endpoint
func StartDeployment(c *gin.Context) {
	var req DeploymentRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": err.Error()})
		return
	}

	cfg := config.GetConfig()

	// Check if project exists
	_, exists := cfg.Projects[req.ProjectID]
	if !exists {
		c.JSON(http.StatusNotFound, gin.H{"detail": fmt.Sprintf("Project with ID %s was not found", req.ProjectID)})
		return
	}

	// Create lock key
	lockKey := fmt.Sprintf("%s/%s", req.ProjectID, req.Stand)

	// Check if deployment is already running
	if isDeploymentLocked(lockKey) {
		c.JSON(http.StatusConflict, gin.H{
			"detail": fmt.Sprintf("A deployment is already running for project %s on stand %s. Wait for the current deployment to finish.", req.ProjectID, req.Stand),
		})
		return
	}

	// Generate unique ID
	uniqueID := generateUniqueID()
	fullDeploymentID := fmt.Sprintf("%s/%s/%s/%s", req.ProjectID, req.Branch, req.Stand, uniqueID)

	// Store deployment data
	deploymentDataMutex.Lock()
	deploymentDataStore[uniqueID] = &DeploymentData{
		FullDeploymentID: fullDeploymentID,
		ProjectID:        req.ProjectID,
		Branch:           req.Branch,
		Stand:            req.Stand,
		UserID:           req.UserID,
		EnvID:            req.EnvID,
		Timestamp:        time.Now().Unix(),
	}
	deploymentDataMutex.Unlock()

	// Start cleanup goroutine
	go func() {
		time.Sleep(1 * time.Hour)
		deploymentDataMutex.Lock()
		delete(deploymentDataStore, uniqueID)
		deploymentDataMutex.Unlock()

		log.Printf("Deployment data for %s cleaned up after timeout", uniqueID)
	}()

	log.Printf("Starting deployment: %s", fullDeploymentID)

	// Handshake: prove webhook reachability before committing to a long-running
	// deploy. If the agent cannot reach the API, fail fast so the UI does not
	// hang in a silent "deploying" state for minutes.
	if ackCode, ackErr := sendDeployWebhook(req.DeployID, req.ProjectID, "deploying", nil); ackErr != nil || (ackCode != http.StatusNoContent && ackCode != http.StatusOK && ackCode != http.StatusConflict) {
		log.Printf("Handshake webhook failed for deploy_id=%s: code=%d err=%v", req.DeployID, ackCode, ackErr)
		deploymentDataMutex.Lock()
		delete(deploymentDataStore, uniqueID)
		deploymentDataMutex.Unlock()
		c.JSON(http.StatusBadGateway, gin.H{"detail": "Agent cannot deliver webhook to API; aborting deploy."})
		return
	}

	var outputMu sync.Mutex
	fullOutput := ""
	appendOutput := func(message string) {
		outputMu.Lock()
		fullOutput += message
		if !strings.HasSuffix(message, "\n") {
			fullOutput += "\n"
		}
		outputMu.Unlock()
	}
	getOutputSnapshot := func() string {
		outputMu.Lock()
		s := fullOutput
		outputMu.Unlock()
		return s
	}

	appendOutput(fmt.Sprintf("Starting deployment %s", fullDeploymentID))

	stopUpdates := make(chan struct{})
	var stopOnce sync.Once
	safeStopUpdates := func() {
		stopOnce.Do(func() { close(stopUpdates) })
	}

	// sendFinalWebhookWithRetry sends the terminal status webhook with retries
	// to ensure the API always receives the final deploy status.
	sendFinalWebhookWithRetry := func(deployID, codename, status string, output *string) {
		const maxRetries = 3
		for attempt := 1; attempt <= maxRetries; attempt++ {
			code, err := sendDeployWebhook(deployID, codename, status, output)
			if err != nil {
				log.Printf("Error sending deploy webhook (%s), attempt %d/%d: %v", status, attempt, maxRetries, err)
				if attempt < maxRetries {
					time.Sleep(time.Duration(attempt) * 2 * time.Second)
					continue
				}
				log.Printf("CRITICAL: Failed to send final deploy webhook (%s) after %d attempts for deploy_id=%s", status, maxRetries, deployID)
				return
			}
			if code == http.StatusNoContent || code == http.StatusOK || code == http.StatusConflict {
				return
			}
			log.Printf("Unexpected deploy webhook status (%s): %d for deploy_id=%s, attempt %d/%d", status, code, deployID, attempt, maxRetries)
			if attempt < maxRetries {
				time.Sleep(time.Duration(attempt) * 2 * time.Second)
			}
		}
	}

	go func() {
		ticker := time.NewTicker(3 * time.Second)
		defer ticker.Stop()
		for {
			select {
			case <-ticker.C:
				out := getOutputSnapshot()
				outPtr := &out
				code, err := sendDeployWebhook(req.DeployID, req.ProjectID, "deploying", outPtr)
				if err != nil {
					log.Printf("Error sending deploy webhook (deploying): %v", err)
					continue
				}
				if code == http.StatusConflict {
					log.Printf("Deploy webhook rejected as finalized (409) for deploy_id=%s", req.DeployID)
					safeStopUpdates()
					return
				}
				if code != http.StatusNoContent && code != http.StatusOK {
					log.Printf("Unexpected deploy webhook status (deploying): %d for deploy_id=%s", code, req.DeployID)
				}
			case <-stopUpdates:
				return
			}
		}
	}()

	go func() {
		// Recover from any panic to ensure final webhook is always sent
		defer func() {
			if r := recover(); r != nil {
				log.Printf("PANIC in deployment goroutine for deploy_id=%s: %v", req.DeployID, r)
				appendOutput(fmt.Sprintf("\nInternal error: %v", r))
				out := getOutputSnapshot()
				outPtr := &out
				sendFinalWebhookWithRetry(req.DeployID, req.ProjectID, "failed", outPtr)
				safeStopUpdates()
			}
		}()

		project, exists := cfg.Projects[req.ProjectID]
		if !exists {
			appendOutput(fmt.Sprintf("Project %s not found", req.ProjectID))
			out := getOutputSnapshot()
			outPtr := &out
			sendFinalWebhookWithRetry(req.DeployID, req.ProjectID, "failed", outPtr)
			safeStopUpdates()
			return
		}

		lock := getDeploymentLock(lockKey)
		lock.Lock()
		defer lock.Unlock()

		cb := func(message string) {
			appendOutput(message)
		}

		deployment := deploy.NewDeployment(req.ProjectID, req.Branch, req.Stand, project.RunAs, cb)
		result := deployment.ExecuteDeployment(project)

		finalStatus := "failed"
		if result.Success {
			finalStatus = "success"
		}
		out := getOutputSnapshot()
		outPtr := &out
		sendFinalWebhookWithRetry(req.DeployID, req.ProjectID, finalStatus, outPtr)
		safeStopUpdates()
	}()

	c.JSON(http.StatusOK, gin.H{
		"deployment_id": req.DeployID,
		"status":        "started",
	})
}

// Get config file endpoint
func GetConfigFile(c *gin.Context) {
	projectID := c.Param("project_id")
	configFileID := c.Param("config_file_id")

	cfg := config.GetConfig()
	if !cfg.ConfigEditingEnabled {
		c.JSON(http.StatusForbidden, gin.H{"detail": "Config editing is disabled on this agent"})
		return
	}

	project, exists := cfg.Projects[projectID]
	if !exists {
		c.JSON(http.StatusNotFound, gin.H{"detail": fmt.Sprintf("Project with ID %s was not found", projectID)})
		return
	}

	configFileIdx, err := strconv.Atoi(configFileID)
	if err != nil || configFileIdx < 0 || configFileIdx >= len(project.ConfigFiles) {
		c.JSON(http.StatusNotFound, gin.H{"detail": fmt.Sprintf("Config file %s was not found", configFileID)})
		return
	}

	configFile := project.ConfigFiles[configFileIdx]
	if configFile.Path == "" {
		c.JSON(http.StatusNotFound, gin.H{"detail": fmt.Sprintf("File %s was not found", configFile.Path)})
		return
	}

	if !configFile.Editable {
		c.JSON(http.StatusForbidden, gin.H{"detail": fmt.Sprintf("File %s is not editable", configFile.Path)})
		return
	}

	content, err := deploy.ReadConfigFile(configFile.Path)
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"project_id":     projectID,
		"config_file_id": configFileID,
		"name":           configFile.Name,
		"path":           configFile.Path,
		"content":        content,
	})
}

// Config update request model
type ConfigUpdateRequest struct {
	ProjectID    string `json:"project_id" binding:"required"`
	ConfigFileID string `json:"config_file_id" binding:"required"`
	Content      string `json:"content" binding:"required"`
}

// Update config file endpoint
func UpdateConfigFile(c *gin.Context) {
	var req ConfigUpdateRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": err.Error()})
		return
	}

	cfg := config.GetConfig()
	if !cfg.ConfigEditingEnabled {
		c.JSON(http.StatusForbidden, gin.H{"detail": "Config editing is disabled on this agent"})
		return
	}

	project, exists := cfg.Projects[req.ProjectID]
	if !exists {
		c.JSON(http.StatusNotFound, gin.H{"detail": fmt.Sprintf("Project with ID %s was not found", req.ProjectID)})
		return
	}

	const maxContentBytes = 1 << 20 // 1 MB
	if len(req.Content) > maxContentBytes {
		c.JSON(http.StatusRequestEntityTooLarge, gin.H{"detail": "Config file content exceeds 1MB limit"})
		return
	}

	configFileIdx, err := strconv.Atoi(req.ConfigFileID)
	if err != nil || configFileIdx < 0 || configFileIdx >= len(project.ConfigFiles) {
		c.JSON(http.StatusNotFound, gin.H{"detail": fmt.Sprintf("Config file %s was not found", req.ConfigFileID)})
		return
	}

	configFile := project.ConfigFiles[configFileIdx]
	if configFile.Path == "" {
		c.JSON(http.StatusNotFound, gin.H{"detail": "File path is not specified"})
		return
	}

	if !configFile.Editable {
		c.JSON(http.StatusForbidden, gin.H{"detail": fmt.Sprintf("File %s is not editable", configFile.Path)})
		return
	}

	if err := deploy.WriteConfigFile(configFile.Path, req.Content); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": err.Error()})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": fmt.Sprintf("File %s was updated successfully", configFile.Path),
	})
}

// Terminal command request model
type TerminalCommandRequest struct {
	Command   string `json:"command" binding:"required"`
	ProjectID string `json:"project_id"`
}

// Execute terminal command endpoint
func ExecuteTerminalCommand(c *gin.Context) {
	var req TerminalCommandRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": err.Error()})
		return
	}

	cfg := config.GetConfig()
	if !cfg.TerminalEnabled {
		c.JSON(http.StatusForbidden, gin.H{"detail": "Terminal is disabled on this agent"})
		return
	}
	command := strings.TrimSpace(req.Command)

	// Validate command
	validationResult := security.ValidateTerminalCommand(command, cfg.TerminalSecurity)
	if !validationResult.Valid {
		c.JSON(http.StatusBadRequest, gin.H{"detail": fmt.Sprintf("Command is not allowed: %s", validationResult.Error)})
		return
	}

	// Determine working directory
	var workingDir string
	if req.ProjectID != "" {
		if project, exists := cfg.Projects[req.ProjectID]; exists {
			workingDir = project.Path
		}
	}

	// Execute command
	cmd := exec.Command("sh", "-c", validationResult.SanitizedCommand)
	if workingDir != "" {
		cmd.Dir = workingDir
	}

	stdout, err := cmd.Output()
	var stderr string
	if exitErr, ok := err.(*exec.ExitError); ok {
		stderr = string(exitErr.Stderr)
	}

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else {
			exitCode = -1
		}
	}

	c.JSON(http.StatusOK, gin.H{
		"success":   exitCode == 0,
		"exit_code": exitCode,
		"stdout":    string(stdout),
		"stderr":    stderr,
		"command":   validationResult.SanitizedCommand,
	})
}

// Get crontab endpoint
func GetCrontab(c *gin.Context) {
	cmd := exec.Command("crontab", "-l")
	stdout, err := cmd.Output()

	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			stderr := string(exitErr.Stderr)
			if strings.Contains(strings.ToLower(stderr), "no crontab") {
				c.JSON(http.StatusOK, gin.H{
					"success": true,
					"crontab": "",
				})
				return
			}
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"error":   stderr,
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"detail": fmt.Sprintf("Failed to get crontab: %v", err)})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"crontab": string(stdout),
	})
}

// Crontab request model
type CrontabRequest struct {
	CrontabContent string `json:"crontab_content" binding:"required"`
}

// Update crontab endpoint
func UpdateCrontab(c *gin.Context) {
	var req CrontabRequest
	if err := c.ShouldBindJSON(&req); err != nil {
		c.JSON(http.StatusBadRequest, gin.H{"detail": err.Error()})
		return
	}

	// Create temp file
	tmpFile, err := os.CreateTemp("", "crontab-*.cron")
	if err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": fmt.Sprintf("Failed to create temporary file: %v", err)})
		return
	}
	defer os.Remove(tmpFile.Name())

	if _, err := tmpFile.WriteString(req.CrontabContent); err != nil {
		c.JSON(http.StatusInternalServerError, gin.H{"detail": fmt.Sprintf("Failed to write temporary file: %v", err)})
		return
	}
	tmpFile.Close()

	// Install crontab
	cmd := exec.Command("crontab", tmpFile.Name())
	if err := cmd.Run(); err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			c.JSON(http.StatusOK, gin.H{
				"success": false,
				"error":   string(exitErr.Stderr),
			})
			return
		}
		c.JSON(http.StatusInternalServerError, gin.H{"detail": fmt.Sprintf("Failed to update crontab: %v", err)})
		return
	}

	c.JSON(http.StatusOK, gin.H{
		"success": true,
		"message": "Crontab updated successfully",
	})
}
