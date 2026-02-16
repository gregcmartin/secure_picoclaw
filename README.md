<div align="center">

  <h1>Secure PicoClaw</h1>

  <h3>A security-hardened fork of PicoClaw: Ultra-Efficient AI Assistant in Go</h3>

  <p>
    <img src="https://img.shields.io/badge/Go-1.21+-00ADD8?style=flat&logo=go&logoColor=white" alt="Go">
    <img src="https://img.shields.io/badge/Arch-x86__64%2C%20ARM64%2C%20RISC--V-blue" alt="Hardware">
    <img src="https://img.shields.io/badge/license-MIT-green" alt="License">
  </p>
</div>

---

Secure PicoClaw is a streamlined fork of [PicoClaw](https://github.com/sipeed/picoclaw) focused on security, simplicity, and international use. It strips out region-specific channels and providers, adds native WhatsApp support, and keeps only widely-used integrations.

## What Changed from Upstream

| Area | Removed | Kept / Added |
|------|---------|--------------|
| **Channels** | QQ, DingTalk, LINE, OneBot, Feishu/Lark, MaixCam | Telegram, Discord, Slack, **WhatsApp (native)** |
| **Providers** | Zhipu/GLM, Moonshot/Kimi, ShengSuanYun, DeepSeek | OpenAI, Anthropic, OpenRouter, Groq, Gemini, Nvidia, vLLM, GitHub Copilot |
| **WhatsApp** | External Node.js bridge required | **Native Go implementation** via whatsmeow (no bridge needed) |
| **Default model** | `glm-4.7` | `gpt-5.3` |
| **Docs** | Chinese and Japanese READMEs | English only |
| **Comments** | Chinese inline comments | Translated to English |

## Features

- **Ultra-lightweight**: <10MB memory footprint, single static binary
- **Cross-platform**: Runs on x86_64, ARM64, and RISC-V
- **Fast startup**: Boots in ~1 second on low-end hardware
- **Security sandbox**: Agent restricted to workspace by default with dangerous command blocking
- **Native WhatsApp**: Connects directly to WhatsApp Web via whatsmeow -- no external bridge
- **Voice transcription**: Groq Whisper integration across Telegram, Discord, Slack, and WhatsApp
- **Scheduled tasks**: Heartbeat system with cron-based reminders and async subagents

## Quick Start

### Install from source

```bash
git clone https://github.com/gregcmartin/secure_picoclaw.git
cd secure_picoclaw
make deps && make build
```

### Initialize

```bash
picoclaw onboard
```

### Configure (`~/.picoclaw/config.json`)

```json
{
  "agents": {
    "defaults": {
      "workspace": "~/.picoclaw/workspace",
      "model": "gpt-5.3",
      "max_tokens": 8192,
      "temperature": 0.7,
      "max_tool_iterations": 20
    }
  },
  "providers": {
    "openai": {
      "api_key": "sk-xxx"
    }
  }
}
```

### Chat

```bash
picoclaw agent -m "What is 2+2?"
```

## Channels

Talk to your agent through Telegram, Discord, Slack, or WhatsApp.

| Channel | Setup |
|---------|-------|
| **Telegram** | Easy (just a bot token) |
| **Discord** | Easy (bot token + message content intent) |
| **Slack** | Medium (bot token + app token, Socket Mode) |
| **WhatsApp** | Easy (scan QR code in terminal) |

<details>
<summary><b>Telegram</b></summary>

1. Message `@BotFather` on Telegram, send `/newbot`, copy the token
2. Get your user ID from `@userinfobot`
3. Configure:

```json
{
  "channels": {
    "telegram": {
      "enabled": true,
      "token": "YOUR_BOT_TOKEN",
      "allow_from": ["YOUR_USER_ID"]
    }
  }
}
```

4. Run `picoclaw gateway`

</details>

<details>
<summary><b>Discord</b></summary>

1. Create an app at https://discord.com/developers/applications
2. Create a Bot, copy the token, enable **MESSAGE CONTENT INTENT**
3. Get your User ID (enable Developer Mode, right-click your avatar)
4. Configure:

```json
{
  "channels": {
    "discord": {
      "enabled": true,
      "token": "YOUR_BOT_TOKEN",
      "allow_from": ["YOUR_USER_ID"]
    }
  }
}
```

5. Invite the bot via OAuth2 URL Generator (scope: `bot`, permissions: Send Messages, Read Message History)
6. Run `picoclaw gateway`

</details>

<details>
<summary><b>Slack</b></summary>

1. Create a Slack app with Socket Mode enabled
2. Get a Bot Token (`xoxb-...`) and App-Level Token (`xapp-...`)
3. Subscribe to events: `message.channels`, `message.im`, `app_mention`
4. Configure:

```json
{
  "channels": {
    "slack": {
      "enabled": true,
      "bot_token": "xoxb-YOUR-BOT-TOKEN",
      "app_token": "xapp-YOUR-APP-TOKEN",
      "allow_from": []
    }
  }
}
```

5. Run `picoclaw gateway`

</details>

<details>
<summary><b>WhatsApp (Native)</b></summary>

WhatsApp connects directly using the WhatsApp Web multi-device protocol. No external bridge is needed.

1. Configure:

```json
{
  "channels": {
    "whatsapp": {
      "enabled": true,
      "store_path": "~/.picoclaw/whatsapp.db",
      "allow_from": []
    }
  }
}
```

2. Run `picoclaw gateway`
3. Scan the QR code displayed in your terminal with WhatsApp on your phone
4. Session persists in the SQLite database -- you won't need to re-scan unless you log out

**Bridge mode (legacy):** If you still have an external WhatsApp bridge, set `"bridge_url": "ws://localhost:3001"` and it will use the old WebSocket bridge instead.

</details>

## Providers

| Provider | Purpose | API Key |
|----------|---------|---------|
| **OpenAI** | LLM (GPT) | [platform.openai.com](https://platform.openai.com) |
| **Anthropic** | LLM (Claude) | [console.anthropic.com](https://console.anthropic.com) |
| **OpenRouter** | LLM (access to many models) | [openrouter.ai](https://openrouter.ai) |
| **Gemini** | LLM (Gemini) | [aistudio.google.com](https://aistudio.google.com) |
| **Groq** | LLM + voice transcription (Whisper) | [console.groq.com](https://console.groq.com) |
| **Nvidia** | LLM (NIM) | [build.nvidia.com](https://build.nvidia.com) |
| **vLLM** | Self-hosted LLM | Your own endpoint |
| **GitHub Copilot** | LLM via Copilot | GitHub subscription |

> **Voice transcription**: If a Groq API key is configured, voice messages on Telegram, Discord, Slack, and WhatsApp are automatically transcribed via Whisper.

## Security Sandbox

PicoClaw runs agents in a sandboxed environment by default.

```json
{
  "agents": {
    "defaults": {
      "restrict_to_workspace": true
    }
  }
}
```

When enabled, all file and command tools are restricted to the workspace directory. Dangerous commands (`rm -rf`, `format`, `dd`, `shutdown`, fork bombs) are blocked regardless of sandbox setting.

The sandbox applies consistently across the main agent, subagents, and heartbeat tasks.

## Heartbeat (Periodic Tasks)

Create `HEARTBEAT.md` in your workspace with tasks the agent should run periodically:

```markdown
# Periodic Tasks
- Check my email for important messages
- Search the web for AI news and summarize
```

Configuration:

```json
{
  "heartbeat": {
    "enabled": true,
    "interval": 30
  }
}
```

The agent reads `HEARTBEAT.md` at the configured interval (minutes). Long-running tasks can be delegated to async subagents via the `spawn` tool.

## CLI Reference

| Command | Description |
|---------|-------------|
| `picoclaw onboard` | Initialize config and workspace |
| `picoclaw agent -m "..."` | Chat with the agent |
| `picoclaw agent` | Interactive chat mode |
| `picoclaw gateway` | Start the gateway (channels + heartbeat) |
| `picoclaw status` | Show status |
| `picoclaw cron list` | List scheduled jobs |
| `picoclaw cron add ...` | Add a scheduled job |

## Docker Compose

```bash
git clone https://github.com/gregcmartin/secure_picoclaw.git
cd secure_picoclaw

cp config/config.example.json config/config.json
# Edit config.json with your API keys and tokens

docker compose --profile gateway up -d
docker compose logs -f picoclaw-gateway
```

## Troubleshooting

**Telegram: "Conflict: terminated by other getUpdates"**
Only one `picoclaw gateway` instance can run at a time. Stop any other instances.

**Web search not working**
Configure a Brave Search API key (free tier: 2000 queries/month) or use the built-in DuckDuckGo fallback.

**WhatsApp QR code not showing**
Make sure `whatsapp.enabled` is `true` and `bridge_url` is empty (not set). The QR code prints to stdout on first run.

## License

MIT -- see [LICENSE](LICENSE) for details.

## Acknowledgments

Forked from [PicoClaw](https://github.com/sipeed/picoclaw) by [Sipeed](https://sipeed.com), which was inspired by [nanobot](https://github.com/HKUDS/nanobot).
