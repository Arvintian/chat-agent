# Chat-Agent CLI

A command-line interface for interacting with LLM agents, featuring flexible model configurations and MCP support.

## Features

- **Multi-provider support**: Configure different LLM providers (DeepSeek, OpenAI, etc.)
- **Preset chat configurations**: Define reusable chat presets with custom system prompts
- **MCP integration**: Connect to Model Context Protocol for extended capabilities
- **Simple configuration**: YAML-based configuration with sensible defaults

## Quick Start

1. **Create configuration file** at `~/.chat-agent/config.yml`:

```yaml
# Model provider configuration
providers:
  deepseek:
    type: deepseek
    base_url: https://api.deepseek.com
    api_key: your-api-key-here

# Model configuration
models:
  deepseek-chat:
    provider: deepseek
    model: deepseek-chat
    temperature: 0.8

# Chat preset configuration
chats:
  default:
    model: deepseek-chat
    desc: "A friendly assistant"
    system: "You are a helpful assistant."
```

2. **Start a chat session**:

```bash
chat-agent --chat default
```

## Usage

```bash
# Basic usage with default chat preset
chat-agent

# Specify a chat preset
chat-agent --chat default

# Custom welcome message
chat-agent --welcome "Hello! How can I help you today?"

# Enable debug mode
chat-agent --debug

# Specify custom config file
chat-agent --config /path/to/config.yml

# One-time task (non-interactive)
chat-agent --once "List files in current directory"

# Show help
chat-agent --help

# Show version
chat-agent --version
```

### Interactive Commands

When in chat mode, you can use the following commands:

- `/help` or `/h` - Show help message
- `/history` or `/i` - Get conversation history
- `/clear` or `/c` - Clear conversation context
- `/tools` or `/l` - List loaded tools
- `/sys` or `/system` - Show current system prompt (with template variables rendered)
- `/t cmd` - Execute local command (e.g., `/t ls -la`)
- `/exit` or `/q` - Exit program

## Building from Source

### Prerequisites
- Go 1.25 or higher
- Git

### Build Commands

```bash
# Build for current platform
make build

# Build for all platforms (Linux, macOS, Windows)
make build-all

# Build for Linux only (x86_64 and ARM64)
make build-linux

# Build for macOS only (x86_64 and ARM64)
make build-darwin

# Create release archives
make release

# Run tests
make test

# Run linter
make lint

# Clean build artifacts
make clean

# Install to /usr/local/bin
make install
```

### Cross-Compilation
The Makefile supports cross-compilation for:
- **Linux**: x86_64 (amd64), ARM64
- **macOS**: x86_64 (amd64), ARM64 (Apple Silicon)
- **Windows**: x86_64 (amd64)

## Release Process

### Creating a Release
1. Update version using the release script:
   ```bash
   ./scripts/release.sh
   ```

2. Or manually create a git tag:
   ```bash
   git tag -a v1.0.0 -m "Release v1.0.0"
   git push origin v1.0.0
   ```

3. GitHub Actions will automatically:
   - Build binaries for all platforms
   - Create release archives (.tar.gz for Unix, .zip for Windows)
   - Publish to GitHub Releases

### Version Management
- Check current version: `./scripts/version.sh`
- View available binaries: `ls -la dist/`

## Configuration

### Providers
Configure LLM providers with API keys and base URLs:

```yaml
providers:
  deepseek:
    type: deepseek
    base_url: https://api.deepseek.com
    api_key: sk-your-api-key
```

### Models
Define model configurations with temperature and other parameters:

```yaml
models:
  deepseek-chat:
    provider: deepseek
    model: deepseek-chat
    temperature: 0.8
  deepseek-reasoner:
    provider: deepseek
    model: deepseek-reasoner
    temperature: 0.8
```

### Chat Presets
Create reusable chat configurations with Go template support:

```yaml
chats:
  default:
    model: deepseek-chat
    desc: "A friendly assistant"
    system: "You are a helpful assistant."
  reasoner:
    model: deepseek-reasoner
    desc: "Reasoning-focused assistant"
    system: "You are a reasoning assistant. Think step by step."
  template-example:
    model: deepseek-chat
    desc: "Assistant with template variables"
    system: |
      You are a helpful assistant.
      
      Current working directory: {{.Cwd}}
      Today's date: {{.Date}}
      Current time: {{.Now.Format "2006-01-02 15:04:05"}}
      
      Please help the user with tasks in the current directory.
```

**Available template variables:**
- `{{.Cwd}}` - Current working directory
- `{{.Date}}` - Today's date in YYYY-MM-DD format
- `{{.Now}}` - Current time (time.Time object, can be formatted)
  - Example: `{{.Now.Format "2006-01-02 15:04:05"}}`
  - Example: `{{.Now.Format "Monday, January 2, 2006"}}`
  - Example: `{{.Now.Year}}` - Current year
  - Example: `{{.Now.Month}}` - Current month
  - Example: `{{.Now.Day}}` - Current day of month
- `{{.User}}` - Current username
- `{{.Home}}` - User's home directory
- `{{env "VAR_NAME"}}` - Access environment variables
  - Example: `{{env "USER"}}` - Current user
  - Example: `{{env "HOME"}}` - Home directory
  - Example: `{{env "PATH"}}` - System PATH

### MCP Servers
Integrate with Model Context Protocol servers:

```yaml
mcp_servers:
  web_search:
    type: sse
    url: "https://your-mcp-host"

# Add MCP to chat presets
chats:
  assistant-with-search:
    model: deepseek-chat
    system: "You are a helpful assistant with web search capabilities."
    mcp_servers:
      - web_search
```
