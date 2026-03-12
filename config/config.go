package config

import (
	"fmt"
	"net/url"
	"os"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

type ConfigFile struct {
	Name     string `yaml:"name" json:"name"`
	Path     string `yaml:"path" json:"path"`
	Editable bool   `yaml:"editable" json:"editable"`
}

type Project struct {
	Name           string                  `yaml:"name" json:"name"`
	Path           string                  `yaml:"path" json:"path"`
	Type           string                  `yaml:"type" json:"type"`
	RunAs          string                  `yaml:"run_as" json:"run_as"`
	Stands         map[string]ProjectStand `yaml:"stands" json:"stands"`
	ConfigFiles    []ConfigFile            `yaml:"config_files" json:"config_files"`
	DeployCommands []string                `yaml:"deploy_commands" json:"deploy_commands"`
}

type ProjectStand struct {
	RunAs          string   `yaml:"run_as" json:"run_as"`
	DeployCommands []string `yaml:"deploy_commands" json:"deploy_commands"`
}

type TerminalSecurity struct {
	AllowedCommands    []string `yaml:"allowed_commands" json:"allowed_commands"`
	ForbiddenCommands  []string `yaml:"forbidden_commands" json:"forbidden_commands"`
	MaxCommandLength   int      `yaml:"max_command_length" json:"max_command_length"`
	AllowArguments     bool     `yaml:"allow_arguments" json:"allow_arguments"`
	BlockCommandChains bool     `yaml:"block_command_chains" json:"block_command_chains"`
}

type S3Config struct {
	Endpoint     string `yaml:"endpoint" json:"endpoint"`
	Region       string `yaml:"region" json:"region"`
	Bucket       string `yaml:"bucket" json:"bucket"`
	AccessKey    string `yaml:"access_key" json:"-"`
	SecretKey    string `yaml:"secret_key" json:"-"`
	UsePathStyle bool   `yaml:"use_path_style" json:"use_path_style"`
}

func (s *S3Config) IsConfigured() bool {
	return s.Bucket != "" && s.Region != "" && s.AccessKey != "" && s.SecretKey != ""
}

type Config struct {
	Host                   string             `yaml:"host"`
	Port                   int                `yaml:"port"`
	Debug                  bool               `yaml:"debug"`
	APIToAgentSigningKey   string             `yaml:"api_to_agent_signing_key"`
	AgentToAPISigningKey   string             `yaml:"agent_to_api_signing_key"`
	TokenExpirationMinutes int                `yaml:"token_expiration_minutes"`
	DeployerURL            string             `yaml:"deployer_url"`
	TerminalSecurity       TerminalSecurity   `yaml:"terminal_security"`
	S3                     S3Config           `yaml:"s3"`
	Projects               map[string]Project `yaml:"projects"`
}

var AppConfig *Config

func LoadConfig(configPath string) error {
	data, err := os.ReadFile(configPath)
	if err != nil {
		return fmt.Errorf("failed to read config file: %w", err)
	}

	config := &Config{}
	if err := yaml.Unmarshal(data, config); err != nil {
		return fmt.Errorf("failed to parse config file: %w", err)
	}

	// Override with environment variables if set
	if host := os.Getenv("AGENT_HOST"); host != "" {
		config.Host = host
	}
	if port := os.Getenv("AGENT_PORT"); port != "" {
		if p, err := strconv.Atoi(port); err == nil {
			config.Port = p
		}
	}
	if debug := os.Getenv("AGENT_DEBUG"); debug != "" {
		config.Debug = debug == "true" || debug == "1"
	}
	if signingKey := os.Getenv("API_TO_AGENT_SIGNING_KEY"); signingKey != "" {
		config.APIToAgentSigningKey = signingKey
	}
	if signingKey := os.Getenv("AGENT_TO_API_SIGNING_KEY"); signingKey != "" {
		config.AgentToAPISigningKey = signingKey
	}
	if deployerURL := os.Getenv("DEPLOYER_URL"); deployerURL != "" {
		config.DeployerURL = deployerURL
	}

	// Set defaults
	if config.Host == "" {
		config.Host = "0.0.0.0"
	}
	if config.Port == 0 {
		config.Port = 8000
	}
	if config.TokenExpirationMinutes == 0 {
		config.TokenExpirationMinutes = 60
	}
	if config.TerminalSecurity.MaxCommandLength == 0 {
		config.TerminalSecurity.MaxCommandLength = 500
	}

	config.DeployerURL = strings.TrimSpace(config.DeployerURL)
	config.DeployerURL = strings.TrimRight(config.DeployerURL, "/")
	if config.DeployerURL != "" {
		parsed, err := url.Parse(config.DeployerURL)
		if err != nil {
			return fmt.Errorf("invalid deployer_url: %w", err)
		}
		if parsed.Scheme == "" {
			config.DeployerURL = "http://" + config.DeployerURL
		}
	}

	AppConfig = config
	return nil
}

func GetConfig() *Config {
	return AppConfig
}
