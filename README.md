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

# Show help
chat-agent --help
```

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
Create reusable chat configurations:

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
```

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
