package security

import (
	"fmt"
	"regexp"
	"strings"

	"deployer-agent/config"
)

type ValidationResult struct {
	Valid            bool   `json:"valid"`
	Error            string `json:"error,omitempty"`
	SanitizedCommand string `json:"sanitized_command,omitempty"`
}

type CommandSecurityValidator struct {
	config            config.TerminalSecurity
	allowedCommands   map[string]bool
	forbiddenCommands map[string]bool
	chainPatterns     []*regexp.Regexp
	safePipeCommands  map[string]bool
}

func NewCommandSecurityValidator(securityConfig config.TerminalSecurity) *CommandSecurityValidator {
	validator := &CommandSecurityValidator{
		config:            securityConfig,
		allowedCommands:   make(map[string]bool),
		forbiddenCommands: make(map[string]bool),
		safePipeCommands:  make(map[string]bool),
	}

	for _, cmd := range securityConfig.AllowedCommands {
		validator.allowedCommands[cmd] = true
	}

	for _, cmd := range securityConfig.ForbiddenCommands {
		validator.forbiddenCommands[cmd] = true
	}

	// Safe pipe commands
	safePipe := []string{"grep", "head", "tail", "sort", "uniq", "wc", "cat"}
	for _, cmd := range safePipe {
		validator.safePipeCommands[cmd] = true
	}

	// Chain patterns
	patterns := []string{
		`;`,    // semicolon
		`&&`,   // logical AND
		`\|\|`, // logical OR
		`\|`,   // pipe
		"`",    // backticks
		`\$\(`, // command substitution $(...)
		`>`,    // output redirect
		`>>`,   // append to file
		`<`,    // input redirect
	}

	for _, p := range patterns {
		validator.chainPatterns = append(validator.chainPatterns, regexp.MustCompile(p))
	}

	return validator
}

func (v *CommandSecurityValidator) ValidateCommand(command string) ValidationResult {
	result := ValidationResult{
		Valid: false,
	}

	// Check for empty command
	command = strings.TrimSpace(command)
	if command == "" {
		result.Error = "Command cannot be empty"
		return result
	}

	// Check command length
	if len(command) > v.config.MaxCommandLength {
		result.Error = fmt.Sprintf("Command is too long (maximum %d characters)", v.config.MaxCommandLength)
		return result
	}

	// Check for command chains
	if v.config.BlockCommandChains && v.containsCommandChains(command) {
		result.Error = "Command chains and redirections are not allowed"
		return result
	}

	// Parse command
	parts := parseCommand(command)
	if len(parts) == 0 {
		result.Error = "Command cannot be empty"
		return result
	}

	// Get main command
	mainCommand := parts[0]

	// Check forbidden commands (priority)
	if v.forbiddenCommands[mainCommand] {
		result.Error = fmt.Sprintf("Command '%s' is forbidden for security reasons", mainCommand)
		return result
	}

	// Check allowed commands
	if !v.allowedCommands[mainCommand] {
		allowed := make([]string, 0, len(v.allowedCommands))
		for cmd := range v.allowedCommands {
			allowed = append(allowed, cmd)
		}
		result.Error = fmt.Sprintf("Command '%s' is not allowed. Available commands: %s", mainCommand, strings.Join(allowed, ", "))
		return result
	}

	// Check if arguments are forbidden
	if !v.config.AllowArguments && len(parts) > 1 {
		result.Error = "Command arguments are not allowed"
		return result
	}

	// Validate arguments
	if len(parts) > 1 {
		if err := v.validateArguments(parts[1:]); err != nil {
			result.Error = err.Error()
			return result
		}
	}

	result.Valid = true
	result.SanitizedCommand = command
	return result
}

func (v *CommandSecurityValidator) containsCommandChains(command string) bool {
	for i, pattern := range v.chainPatterns {
		if pattern.MatchString(command) {
			// Special case for pipe - allow only with safe commands
			if i == 3 { // pipe pattern index
				if v.isSafePipe(command) {
					continue
				}
			}
			return true
		}
	}
	return false
}

func (v *CommandSecurityValidator) isSafePipe(command string) bool {
	parts := strings.Split(command, "|")
	if len(parts) != 2 {
		return false
	}

	for _, part := range parts {
		part = strings.TrimSpace(part)
		if part == "" {
			return false
		}

		cmdParts := parseCommand(part)
		if len(cmdParts) == 0 {
			return false
		}

		mainCmd := cmdParts[0]
		if !v.safePipeCommands[mainCmd] && !v.allowedCommands[mainCmd] {
			return false
		}
	}

	return true
}

func (v *CommandSecurityValidator) validateArguments(args []string) error {
	suspiciousPattern := regexp.MustCompile(`[` + "`" + `$()]`)

	for _, arg := range args {
		// Check for suspicious characters
		if suspiciousPattern.MatchString(arg) {
			return fmt.Errorf("Argument contains suspicious characters: %s", arg)
		}

		// Check for path traversal
		if strings.Contains(arg, "../") || strings.Contains(arg, "..\\") {
			return fmt.Errorf("Argument contains a path traversal attempt: %s", arg)
		}
	}

	return nil
}

func parseCommand(command string) []string {
	var parts []string
	var current strings.Builder
	inQuote := false
	quoteChar := rune(0)

	for _, r := range command {
		switch {
		case r == '"' || r == '\'':
			if !inQuote {
				inQuote = true
				quoteChar = r
			} else if r == quoteChar {
				inQuote = false
				quoteChar = 0
			} else {
				current.WriteRune(r)
			}
		case r == ' ' && !inQuote:
			if current.Len() > 0 {
				parts = append(parts, current.String())
				current.Reset()
			}
		default:
			current.WriteRune(r)
		}
	}

	if current.Len() > 0 {
		parts = append(parts, current.String())
	}

	return parts
}

func ValidateTerminalCommand(command string, securityConfig config.TerminalSecurity) ValidationResult {
	validator := NewCommandSecurityValidator(securityConfig)
	return validator.ValidateCommand(command)
}
