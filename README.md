# Deployer Agent
 
 [![License](https://img.shields.io/github/license/BIKEYARD/deployer-agent)](LICENSE)
 [![Release](https://img.shields.io/github/v/release/BIKEYARD/deployer-agent)](https://github.com/BIKEYARD/deployer-agent/releases)
 [![Stars](https://img.shields.io/github/stars/BIKEYARD/deployer-agent)](https://github.com/BIKEYARD/deployer-agent/stargazers)
 [![Forks](https://img.shields.io/github/forks/BIKEYARD/deployer-agent)](https://github.com/BIKEYARD/deployer-agent/network/members)
 [![Issues](https://img.shields.io/github/issues/BIKEYARD/deployer-agent)](https://github.com/BIKEYARD/deployer-agent/issues)
 [![Go Version](https://img.shields.io/github/go-mod/go-version/BIKEYARD/deployer-agent)](go.mod)

 A small HTTP agent for executing deployment workflows on a server.

## Features

- **Single binary**: ship and run without additional runtime dependencies.
- **Deployment execution**: run project-specific `deploy_commands` per stand.
- **Project config file management**: read and update whitelisted editable files.
- **Restricted terminal**: execute commands using an allowlist/denylist policy.
- **Crontab management**: read and update the current user crontab.
- **Optional S3 integration**: generate presigned upload/download URLs.

## License

Apache-2.0. See `LICENSE`.

## Building

### Prerequisites

- Go 1.21+

### Build commands

```bash
# Build for current platform
go build -o deployer-agent

# Linux (amd64)
GOOS=linux GOARCH=amd64 go build -o deployer-agent-linux

# Linux (arm64)
GOOS=linux GOARCH=arm64 go build -o deployer-agent-linux-arm64

# macOS (Apple Silicon)
GOOS=darwin GOARCH=arm64 go build -o deployer-agent-darwin-arm64

# macOS (Intel)
GOOS=darwin GOARCH=amd64 go build -o deployer-agent-darwin-amd64

# Windows (amd64)
GOOS=windows GOARCH=amd64 go build -o deployer-agent-windows-amd64.exe

# Smaller binary (strip symbols)
go build -ldflags="-s -w" -o deployer-agent
```

## Running

```bash
# Run with default config.yaml in the current directory
./deployer-agent

# Run with custom config path
./deployer-agent -config /path/to/config.yaml
```

## Configuration

Copy `config.example.yaml` to `config.yaml` and edit values:

```bash
cp config.example.yaml config.yaml
```

### Environment variables

Environment variables override config file values:

- `AGENT_HOST`
- `AGENT_PORT`
- `AGENT_DEBUG`
- `API_TO_AGENT_SIGNING_KEY`
- `AGENT_TO_API_SIGNING_KEY`
- `DEPLOYER_URL`

### Top-level fields

- **`host`**: bind address (default `0.0.0.0`).
- **`port`**: listen port (default `8000`).
- **`debug`**: enables debug logging.
- **`api_to_agent_signing_key`**: shared secret used to verify incoming requests to the agent (required).
- **`agent_to_api_signing_key`**: shared secret used to sign outgoing webhooks (optional).
- **`token_expiration_minutes`**: reserved for future use (default `60`).
- **`deployer_url`**: base URL for outgoing webhooks.
- **`terminal_security`**: restrictions for `/terminal/execute`.
- **`s3`**: optional S3 settings.
- **`projects`**: project definitions keyed by project id.

### `terminal_security`

- **`allowed_commands`**: allowed base commands (`ls`, `cat`, `systemctl`, ...).
- **`forbidden_commands`**: explicit denylist (takes priority).
- **`max_command_length`**: maximum request command length (default `500`).
- **`allow_arguments`**: if `false`, blocks arguments.
- **`block_command_chains`**: if `true`, blocks chains like `;`, `&&`, `|`.

### `s3`

S3 is considered configured only if all of the following are set: `bucket`, `region`, `access_key`, `secret_key`.

- **`endpoint`**: optional custom endpoint (useful for S3-compatible storages).
- **`region`**: AWS region.
- **`bucket`**: bucket name.
- **`access_key`**: access key id.
- **`secret_key`**: secret.
- **`use_path_style`**: path-style addressing toggle.

### `projects.<project_id>`

- **`name`**: display name.
- **`path`**: working directory for deploy commands and terminal execution.
- **`type`**: free-form string (reported via `/projects`).
- **`run_as`**: optional user to run deployment commands as.
- **`stands`**: map of stand name to overrides.
- **`config_files`**: list of files exposed via config endpoints.
- **`deploy_commands`**: list of shell commands executed during deploy.

`stands.<stand>` supports:

- **`run_as`**: override `run_as` for this stand.
- **`deploy_commands`**: override `deploy_commands` for this stand.

Each element of `config_files` supports:

- **`name`**: label exposed to API.
- **`path`**: absolute path to file on disk.
- **`editable`**: if `false`, file is not accessible via config endpoints.

## Authentication (HMAC)

All endpoints are protected by HMAC authentication middleware.

Required request headers:

- `X-Deployer-Timestamp`: unix seconds.
- `X-Deployer-Nonce`: random nonce.
- `X-Deployer-Content-SHA256`: lowercase hex SHA256 of the raw request body (for requests without body use the SHA256 of an empty string).
- `X-Deployer-Signature`: base64 HMAC-SHA256 of the canonical string.

Canonical string format:

```text
METHOD\nPATH\nTIMESTAMP\nNONCE\nCONTENT_SHA256
```

Where `PATH` is the HTTP request path only (no scheme/host/query string).

The signature is verified against `api_to_agent_signing_key`.

## HTTP API

Base URL: `http://<host>:<port>`

### `GET /health`

Response:

- **200**: `{ "status": "healthy", "version": "1.1" }`

### `GET /projects`

Response:

- **200**: `{ "projects": { "<project_id>": { "id": "...", "name": "...", "type": "...", "config_files": [ { "id": "0", "name": "..." } ] } } }`

### `POST /deploy`

Request body:

- `deploy_id` (string, required)
- `project_id` (string, required)
- `branch` (string, required)
- `stand` (string, required)
- `user_id` (any)
- `env_id` (any)

Response:

- **200**: `{ "deployment_id": "<deploy_id>", "status": "started" }`
- **404**: unknown project
- **409**: deployment already running for `project_id/stand`

During the deployment the agent sends status updates to:

- `POST {deployer_url}/api/v1/deploy/{deploy_id}/webhook`

Body:

- `codename` (string): project id
- `status` (string): `deploying` | `success` | `failed`
- `output` (string | null): current output

If `agent_to_api_signing_key` is set, outgoing webhook requests are signed with the same HMAC headers and canonical string format.

### `GET /config/:project_id/:config_file_id`

Response:

- **200**: `{ "project_id": "...", "config_file_id": "0", "name": "...", "path": "...", "content": "..." }`
- **403**: file not editable
- **404**: unknown project or file id

### `POST /config`

Request body:

- `project_id` (string, required)
- `config_file_id` (string, required)
- `content` (string, required)

Response:

- **200**: `{ "success": true, "message": "..." }`

### `POST /terminal/execute`

Request body:

- `command` (string, required)
- `project_id` (string, optional): if provided, sets working directory to `projects[project_id].path`

Response:

- **200**: `{ "success": true|false, "exit_code": 0, "stdout": "...", "stderr": "...", "command": "..." }`

### `GET /crontab`

Response:

- **200**: `{ "success": true, "crontab": "..." }` or `{ "success": false, "error": "..." }`

### `POST /crontab`

Request body:

- `crontab_content` (string, required)

Response:

- **200**: `{ "success": true, "message": "Crontab updated successfully" }` or `{ "success": false, "error": "..." }`

### S3 endpoints

These endpoints require S3 to be configured.

- `GET /s3/status`
  - **200**: `{ "configured": true|false, "bucket": "...", "region": "..." }`
- `POST /s3/presign-upload` body: `{ "key": "...", "content_type": "..." }`
  - **200**: `{ "upload_url": "...", "key": "...", "bucket": "..." }`
- `POST /s3/presign-download` body: `{ "key": "..." }`
  - **200**: `{ "download_url": "..." }`
- `POST /s3/head-object` body: `{ "key": "..." }`
  - **200**: `{ "exists": true, "size": 123 }`
  - **404**: `{ "exists": false, "detail": "Object not found" }`
- `POST /s3/delete-object` body: `{ "key": "..." }`
  - **200**: `{ "success": true }`

## Systemd service (Linux)

Create `/etc/systemd/system/deployer-agent.service`:

```ini
[Unit]
Description=Deployer Agent
After=network.target

[Service]
Type=simple
User=www-data
WorkingDirectory=/opt/deployer-agent
ExecStart=/opt/deployer-agent/deployer-agent -config /opt/deployer-agent/config.yaml
Restart=always
RestartSec=5

[Install]
WantedBy=multi-user.target
```

Enable and start:

```bash
sudo systemctl daemon-reload
sudo systemctl enable deployer-agent
sudo systemctl start deployer-agent
sudo systemctl status deployer-agent
```