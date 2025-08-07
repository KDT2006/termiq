# 🎯 Termiq - Terminal-Based Kahoot Clone

A full-fledged terminal-based multiplayer quiz game inspired by Kahoot, built with Go. Host and join interactive quizzes with real-time scoring, leaderboards, and a modern terminal UI.

![Go Version](https://img.shields.io/badge/Go-1.24+-blue.svg)
![License](https://img.shields.io/badge/License-MIT-green.svg)

## Features

- **Multiplayer Quiz Games**: Host and join interactive quiz sessions
- **Real-time Leaderboards**: Live scoring and rankings during gameplay
- **Colored Terminal UI**: Beautiful, modern terminal interface with color coding
- **Customizable Questions**: YAML-based question configuration system with timers
- **Network Architecture**: Client-server architecture with matchmaking
- **Auto-cleanup**: Automatic game cleanup and resource management
- **Multiple Question Sets**: Support for different quiz categories

## Architecture

Termiq uses a distributed architecture with three main components:

- **Matchmaking Server**: Handles game creation, joining, and player coordination
- **Game Server**: Manages individual quiz sessions with real-time gameplay
- **Client**: Terminal-based interface for players to interact with the game

## Quick Start

### Prerequisites

- Go 1.24.2 or higher
- Network connectivity for multiplayer games

### Installation

1. **Clone the repository**

   ```bash
   git clone https://github.com/KDT2006/termiq.git
   cd termiq
   ```

2. **Build the project**
   ```bash
   go build -o bin/server ./cmd/server
   go build -o bin/client ./cmd/client
   ```

### Running the Game

1. **Start the matchmaking server**

   ```bash
   ./bin/server
   # Server will start on localhost:4000
   ```

2. **Start a client (in a new terminal)**

   ```bash
   ./bin/client
   ```

3. **Follow the interactive prompts to either:**
   - Create a new game (host)
   - Join an existing game (player)

## ⚙️ Configuration

### Question Sets

Questions are configured using a `questions.yaml` file. See `config/questions.yaml` for an example configuration

## License

This project is licensed under the MIT License - see the [LICENSE](LICENSE) file for details.