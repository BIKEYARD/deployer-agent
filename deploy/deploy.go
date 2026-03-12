package deploy

import (
	"bufio"
	"deployer-agent/config"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"sync"
)

type CommandResult struct {
	Command  string `json:"command"`
	ExitCode int    `json:"exit_code"`
	Stdout   string `json:"stdout"`
	Stderr   string `json:"stderr"`
	Success  bool   `json:"success"`
}

type DeploymentResult struct {
	Success           bool                     `json:"success"`
	ProjectID         string                   `json:"project_id"`
	Branch            string                   `json:"branch"`
	Stand             string                   `json:"stand"`
	Results           []map[string]interface{} `json:"results"`
	Error             string                   `json:"error,omitempty"`
	FullConsoleOutput string                   `json:"full_console_output,omitempty"`
}

type Deployment struct {
	ProjectID         string
	Branch            string
	Stand             string
	RunAs             string
	WebsocketCallback func(message string)
	fullConsoleOutput []string
}

func NewDeployment(projectID, branch, stand, runAs string, wsCallback func(message string)) *Deployment {
	return &Deployment{
		ProjectID:         projectID,
		Branch:            branch,
		Stand:             stand,
		RunAs:             runAs,
		WebsocketCallback: wsCallback,
		fullConsoleOutput: make([]string, 0),
	}
}

func (d *Deployment) Log(message string) {
	d.fullConsoleOutput = append(d.fullConsoleOutput, message)
	if d.WebsocketCallback != nil {
		d.WebsocketCallback(message)
	}
}

func (d *Deployment) RunCommand(command string, cwd string) CommandResult {
	d.Log(fmt.Sprintf("Executing: %s", command))

	var cmd *exec.Cmd
	if d.RunAs != "" {
		// Run command as specified user with login shell to load SSH keys and environment
		cmd = exec.Command("sudo", "-u", d.RunAs, "-i", "sh", "-c", fmt.Sprintf("cd %s && %s", cwd, command))
	} else {
		cmd = exec.Command("sh", "-c", command)
		if cwd != "" {
			cmd.Dir = cwd
		}
	}

	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return CommandResult{
			Command:  command,
			ExitCode: -1,
			Stderr:   err.Error(),
			Success:  false,
		}
	}

	stderr, err := cmd.StderrPipe()
	if err != nil {
		return CommandResult{
			Command:  command,
			ExitCode: -1,
			Stderr:   err.Error(),
			Success:  false,
		}
	}

	if err := cmd.Start(); err != nil {
		return CommandResult{
			Command:  command,
			ExitCode: -1,
			Stderr:   err.Error(),
			Success:  false,
		}
	}

	var stdoutData, stderrData strings.Builder
	var wg sync.WaitGroup

	// Read stdout
	wg.Add(1)
	stdoutScanner := bufio.NewScanner(stdout)
	go func() {
		defer wg.Done()
		for stdoutScanner.Scan() {
			line := stdoutScanner.Text()
			stdoutData.WriteString(line + "\n")
			d.Log(line)
		}
	}()

	// Read stderr
	wg.Add(1)
	stderrScanner := bufio.NewScanner(stderr)
	go func() {
		defer wg.Done()
		for stderrScanner.Scan() {
			line := stderrScanner.Text()
			stderrData.WriteString(line + "\n")
			d.Log(fmt.Sprintf("STDERR: %s", line))
		}
	}()

	// Wait for all output to be read before calling cmd.Wait()
	wg.Wait()

	err = cmd.Wait()
	exitCode := 0
	if err != nil {
		if exitError, ok := err.(*exec.ExitError); ok {
			exitCode = exitError.ExitCode()
		} else {
			exitCode = -1
		}
	}

	result := CommandResult{
		Command:  command,
		ExitCode: exitCode,
		Stdout:   stdoutData.String(),
		Stderr:   stderrData.String(),
		Success:  exitCode == 0,
	}

	if result.Success {
		d.Log(fmt.Sprintf("Command completed successfully (code %d)", exitCode))
	} else {
		d.Log(fmt.Sprintf("Command failed (code %d)", exitCode))
	}

	return result
}

func (d *Deployment) ExecuteDeployment(projectConfig config.Project) DeploymentResult {
	var results []map[string]interface{}
	success := true

	projectPath := projectConfig.Path
	if projectPath == "" || !pathExists(projectPath) {
		d.Log(fmt.Sprintf("Error: project path '%s' does not exist", projectPath))
		return DeploymentResult{
			Success: false,
			Error:   fmt.Sprintf("Project path does not exist: %s", projectPath),
			Results: results,
		}
	}

	deployCommands := projectConfig.DeployCommands
	if projectConfig.Stands != nil {
		if standCfg, ok := projectConfig.Stands[d.Stand]; ok {
			if len(standCfg.DeployCommands) > 0 {
				deployCommands = standCfg.DeployCommands
			}
			if standCfg.RunAs != "" {
				d.RunAs = standCfg.RunAs
			}
		}
	}

	if len(deployCommands) == 0 {
		d.Log(fmt.Sprintf("Error: deploy_commands are not configured for stand '%s'", d.Stand))
		return DeploymentResult{
			Success:   false,
			ProjectID: d.ProjectID,
			Branch:    d.Branch,
			Stand:     d.Stand,
			Error:     fmt.Sprintf("deploy_commands are not configured for stand '%s'", d.Stand),
			Results:   results,
		}
	}

	d.Log(fmt.Sprintf("\n=== Starting deployment for branch: %s, stand: %s ===", d.Branch, d.Stand))

	// Execute deploy commands (ONLY from config)
	for _, cmdTemplate := range deployCommands {
		// Format command with variables
		cmd := strings.ReplaceAll(cmdTemplate, "{project_path}", projectPath)
		cmd = strings.ReplaceAll(cmd, "{branch}", d.Branch)
		cmd = strings.ReplaceAll(cmd, "{stand}", d.Stand)

		result := d.RunCommand(cmd, projectPath)
		results = append(results, map[string]interface{}{
			"command":   result.Command,
			"exit_code": result.ExitCode,
			"stdout":    result.Stdout,
			"stderr":    result.Stderr,
			"success":   result.Success,
		})

		if !result.Success {
			success = false
			d.Log(fmt.Sprintf("Deployment aborted due to command failure: %s", cmd))
			break
		}
	}

	if success {
		d.Log("\n✅ Deployment completed successfully")
	} else {
		d.Log("\n❌ Deployment finished with errors")
	}

	return DeploymentResult{
		Success:           success,
		ProjectID:         d.ProjectID,
		Branch:            d.Branch,
		Stand:             d.Stand,
		Results:           results,
		FullConsoleOutput: strings.Join(d.fullConsoleOutput, "\n"),
	}
}

func pathExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}

func ReadConfigFile(filePath string) (string, error) {
	if !pathExists(filePath) {
		return "", fmt.Errorf("file not found: %s", filePath)
	}

	data, err := os.ReadFile(filePath)
	if err != nil {
		return "", fmt.Errorf("error reading file: %w", err)
	}

	return string(data), nil
}

func WriteConfigFile(filePath, content string) error {
	// Create backup
	if pathExists(filePath) {
		backupPath := filePath + ".bak"
		existingContent, err := os.ReadFile(filePath)
		if err == nil {
			if err := os.WriteFile(backupPath, existingContent, 0644); err != nil {
				return fmt.Errorf("error creating backup: %w", err)
			}
		}
	}

	// Write new content
	if err := os.WriteFile(filePath, []byte(content), 0644); err != nil {
		return fmt.Errorf("error writing file: %w", err)
	}

	return nil
}
