# local-agent

A desktop AI programming assistant powered by Claude, built with Go, Vue3, and Wails. local-agent is a full-featured IDE-integrated AI agent designed for developers to collaborate with Claude directly in their workspace.

## About

**local-agent** is a comprehensive AI programming assistant featuring:

- 🤖 **Multi-Model LLM Support** - Seamless integration with Anthropic Claude, OpenAI, DeepSeek
- 🛠️ **Tool Execution** - MCP (Model Context Protocol) servers for file operations, terminal execution, and more
- 🎯 **Session Management** - Independent AI conversations with persistent history and context
- 📊 **Code Diff Tracking** - Real-time monitoring and visualization of workspace changes
- 🔒 **Permission System** - Fine-grained authorization control for sensitive operations
- 📁 **Cross-Session Memory** - Maintain knowledge and context across multiple sessions
- 💬 **Chat Interface** - Modern, responsive chat UI with markdown rendering and code highlighting
- 💭 **Extended Thinking** - Deep reasoning mode for complex problem-solving (Claude 3.5+)
- 📋 **Planning & Execution** - AI-generated task breakdown with step-by-step execution
- 🤝 **Sub-Agents** - Delegate specialized tasks to autonomous sub-agents

## Quick Start

### Prerequisites

- **Go** 1.21+
- **Node.js** 16+
- **npm** or **pnpm**

### Installation

```bash
# Clone the repository
git clone <repo-url>
cd local-agent

# Install frontend dependencies
npm install --prefix frontend

# Start development
wails dev

# Build for production
wails build
```

The application will be available at `http://localhost:5173` (frontend) and `http://localhost:34115` (backend API).

## Core Features

### 1. Multi-Model LLM Integration

Switch between different AI models with a single click:

```json
{
  "model": "claude-3-5-sonnet-20241022",  // Anthropic Claude
  "temperature": 1.0
}
```

Supports:
- Anthropic Claude (3, 3.5 Sonnet)
- OpenAI GPT-4, GPT-4o
- DeepSeek (via OpenAI protocol)

### 2. Session Management

Each conversation runs in an isolated session with:
- Full message history
- Context management with automatic compression
- Configuration per session
- File tracking and diff visualization

```bash
POST /api/sessions
{
  "title": "Project Analysis",
  "model": "claude-3-5-sonnet-20241022",
  "project": "/path/to/project"
}
```

### 3. Tool Integration via MCP

Execute tools directly from Claude conversations:

- **File Operations** - Read, write, edit files
- **Terminal Execution** - Run shell commands
- **Git Integration** - Version control operations
- **Custom Tools** - Define and integrate custom MCP servers

Example:
```bash
User: "List all Python files in this project"
  ↓
Claude executes file listing tool
  ↓
Results displayed in conversation
```

### 4. Code Diff Tracking

Real-time monitoring of workspace changes:

- Track all modifications per conversation turn
- Visual diff display with additions/deletions
- Revert specific turns or selective changes
- Integration with Git for baseline comparison

### 5. Permission Control

Fine-grained access control for tool execution:

```javascript
// Permission modes
- "manual"       // Confirm each operation
- "acceptEdits"  // Auto-approve file edits
- "plan"         // Read-only exploration
- "auto"         // Auto-approve safe operations
```

### 6. Context Management

Intelligent context handling:

- Automatic summarization of long conversations
- Configurable context windows per model
- Token counting and cost estimation
- Context compression with reversible summarization

### 7. Extended Thinking (NEW!)

Enable deep reasoning for complex problems:

```bash
POST /api/sessions
{
  "title": "Algorithm Design",
  "thinkingEnabled": true,
  "thinkingBudget": 5000
}
```

When thinking is enabled, Claude's reasoning process becomes visible in the UI:
- Transparent problem-solving steps
- Configurable thinking depth via token budget
- Thinking content preserved separately from responses

Benefits:
- Better accuracy for complex problems
- Visible reasoning for validation
- Suitable for code review, algorithm design, problem-solving

### 8. Memory System

Maintain knowledge across sessions:

- Extract key information after conversations
- Retrieve relevant context for new sessions
- User-defined memory rules
- Cross-session pattern recognition

### 9. Planning & Execution

AI-generated task breakdown:

1. User describes a complex task
2. Claude generates a step-by-step plan
3. Execute plan with user approval
4. Track progress and results
5. Adapt plan based on results

### 10. Sub-Agents

Delegate work to specialized autonomous agents:

- Create focused agents for specific tasks
- Parallel execution capability
- Result aggregation and reporting
- Resource isolation

## Architecture

### Backend (Go)

```
internal/
├── app/           # Main application logic & API handlers
├── llm/           # LLM protocols & model adapters
│   ├── llmtypes.go        # Unified LLM type definitions
│   ├── anthropic.go       # Anthropic protocol adapter
│   ├── openai.go          # OpenAI protocol adapter
│   └── llmretry.go        # Retry logic & error handling
├── store/         # Persistent storage layer
│   ├── sessions.go        # Session and message storage
│   └── ...
├── agent/         # Agent execution engine
│   ├── runcontrol.go      # Execution control & cancellation
│   └── ...
├── permission/    # Permission & authorization system
├── tool/          # Tool execution framework
├── diff/          # Code diff tracking
├── memory/        # Memory management system
└── ...
```

### Frontend (Vue 3 + TypeScript)

```
frontend/src/
├── components/
│   ├── business/
│   │   ├── MessageBubble.vue      # Message display with thinking blocks
│   │   ├── ChatPane.vue            # Main chat interface
│   │   ├── NewSessionDialog.vue    # Session creation
│   │   ├── ToolCallCard.vue        # Tool execution visualization
│   │   └── ...
│   └── layout/
│       ├── TopBar.vue              # Header
│       ├── SessionSidebar.vue      # Session list
│       └── ...
├── stores/        # Pinia state management
│   ├── session.js         # Session store
│   ├── chat.js            # Chat messages
│   └── ...
├── api/          # API clients
├── types/        # TypeScript definitions
└── views/        # Page components
```

### Design Patterns

#### Protocol Abstraction

Unified interface for multiple LLM providers:

```go
// Internal representation (protocol-agnostic)
type LLMReq struct {
  Model    string
  Messages []LLMMessage
  Tools    []LLMTool
}

// Provider-specific adapters
func ToAnthropicRequest(req *LLMReq) *anthropicReq
func ToOpenAIRequest(req *LLMReq) *openaiReq
```

#### Session Isolation

Each session maintains independent state:
- Messages
- Configuration
- Permissions
- Memory
- Diff tracking

#### Tool Framework

Extensible tool execution via MCP:

```go
type Tool interface {
  Execute(ctx context.Context, args map[string]interface{}) (string, error)
  Schema() ToolSchema
}
```

## Development

### Live Development Mode

```bash
wails dev
```

Features:
- Hot reload for frontend changes
- Direct Go method access in browser
- Development server at `http://localhost:34115`

### Building for Production

```bash
wails build
```

Output: `build/bin/local-agent[.exe]`

### Backend Development

```bash
# Build
go build ./cmd/app/...

# Test
go test ./...
go test -cover ./...

# Run tests for specific package
go test ./internal/llm/...
```

### Frontend Development

```bash
cd frontend

# Development with hot reload
npm run dev

# Build
npm run build

# Type checking
npm run type-check

# Lint
npm run lint
```

## Configuration

### wails.json

Main application configuration:

```json
{
  "name": "local-agent",
  "frontend": "frontend",
  "frontend:dev": "npm run dev",
  "frontend:build": "npm run build"
}
```

### Environment Variables

```bash
# LLM API Keys
export ANTHROPIC_API_KEY=ant-...
export OPENAI_API_KEY=sk-...

# Application
export LOCAL_AGENT_PORT=8080
export LOCAL_AGENT_HOME=~/.local-agent
```

## Platform Notes

### Windows: Console Window Handling

On Windows, `wails build` uses the GUI subsystem, so launched console programs create new windows.

**Convention:** All subprocess launches must call `hideConsoleWindow(cmd)` after constructing the cmd.
- Implementation: `internal/procx/procexec_windows.go`
- Non-Windows: no-op
- Enforced by: `procexec_test.go` structural test

## Project Structure

```
local-agent/
├── cmd/
│   └── app/              # Application entry point
├── internal/
│   ├── app/              # Core business logic
│   ├── llm/              # LLM integration
│   ├── store/            # Storage layer
│   ├── agent/            # Agent execution
│   ├── permission/       # Authorization
│   ├── tool/             # Tool framework
│   ├── diff/             # Diff tracking
│   ├── memory/           # Memory system
│   └── ...
├── frontend/
│   ├── src/
│   │   ├── components/   # Vue components
│   │   ├── stores/       # Pinia state
│   │   ├── api/          # API clients
│   │   ├── types/        # TypeScript types
│   │   └── ...
│   ├── package.json
│   └── vite.config.js
├── wails.json            # Wails configuration
├── go.mod/go.sum         # Go dependencies
└── README.md             # This file
```

## API Reference

### Sessions

```bash
# Create session
POST /api/sessions
{
  "title": "New Session",
  "model": "claude-3-5-sonnet-20241022",
  "project": "/path/to/project"
}

# Send message
POST /api/sessions/{sessionId}/chat
{
  "content": "Your question here"
}

# Response includes
{
  "reply": "...",
  "messages": [...],
  "thinking": "...",      # If thinking enabled
  "diff": [...]           # Workspace changes
}
```

## Documentation

Detailed documentation for specific topics:

- **[THINKING_IMPLEMENTATION.md](./THINKING_IMPLEMENTATION.md)** - Extended thinking technical details
- **[FRONTEND_IMPLEMENTATION_GUIDE.md](./FRONTEND_IMPLEMENTATION_GUIDE.md)** - Frontend architecture & components
- **[FRONTEND_CHANGES_COMPLETE.md](./FRONTEND_CHANGES_COMPLETE.md)** - Recent frontend updates
- **[START_HERE.md](./START_HERE.md)** - Quick start guide
- **[IMPLEMENTATION_CHECKLIST.md](./IMPLEMENTATION_CHECKLIST.md)** - Feature checklist

## Contributing

Contributions welcome! Please:

1. Fork the repository
2. Create a feature branch (`git checkout -b feature/your-feature`)
3. Commit changes (`git commit -am 'Add feature'`)
4. Push to branch (`git push origin feature/your-feature`)
5. Open a Pull Request

### Code Standards

- **Go**: Follow `gofmt` formatting, meaningful comments on complex logic
- **TypeScript**: Use ESLint configuration, type all functions
- **Comments**: Document public APIs and complex algorithms

## License

MIT License - see LICENSE file for details.

## Support

- 📖 **Documentation**: See documentation files above
- 🐛 **Bug Reports**: Open issue with reproduction steps
- 💡 **Feature Requests**: Discuss in issues
- 💬 **Questions**: Start a discussion

## Acknowledgments

- Built with [Wails](https://wails.io/) - Desktop app framework
- Powered by [Claude](https://www.anthropic.com/) - AI model
- UI built with [Vue 3](https://vuejs.org/) and [Vite](https://vitejs.dev/)
- Icons from [Lucide Vue Next](https://lucide.dev/)
- MCP Protocol support via [Model Context Protocol](https://modelcontextprotocol.io/)

---

**Version:** Latest (2026-10-13)  
**Status:** Active Development  
**License:** MIT

---

**Version:** Latest (2026-10-13)  
**Status:** Active Development  
**License:** MIT
