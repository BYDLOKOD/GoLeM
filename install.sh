#!/usr/bin/env bash
set -euo pipefail

# One-liner: curl -sL https://raw.githubusercontent.com/veschin/GoLeM/main/install.sh | bash
# Or: bash <(curl -sL https://raw.githubusercontent.com/veschin/GoLeM/main/install.sh)

REPO_URL="https://github.com/veschin/GoLeM.git"
CLONE_DIR="${HOME}/.local/share/GoLeM"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
BLUE='\033[0;34m'
NC='\033[0m'

info()  { echo -e "${GREEN}[+]${NC} $1"; }
warn()  { echo -e "${YELLOW}[!]${NC} $1"; }
err()   { echo -e "${RED}[x]${NC} $1" >&2; }
step()  { echo -e "${BLUE}==>${NC} $1"; }

ask_yn() {
    local prompt="$1" default="${2:-n}" yn
    if [[ "$default" == "y" ]]; then
        read -rp "$prompt [Y/n]: " yn; yn="${yn:-y}"
    else
        read -rp "$prompt [y/N]: " yn; yn="${yn:-n}"
    fi
    [[ "$yn" =~ ^[Yy] ]]
}

detect_os() {
    case "$(uname -s)" in
        Linux*)
            if grep -qi microsoft /proc/version 2>/dev/null; then echo "wsl"
            else echo "linux"; fi ;;
        Darwin*)  echo "macos" ;;
        MINGW*|MSYS*|CYGWIN*) echo "gitbash" ;;
        *)        echo "unknown" ;;
    esac
}

detect_shell() {
    local shell_name=""
    if [[ -n "${ZSH_VERSION:-}" ]]; then
        shell_name="zsh"
    elif [[ -n "${BASH_VERSION:-}" ]]; then
        shell_name="bash"
    elif [[ -n "${FISH_VERSION:-}" ]]; then
        shell_name="fish"
    fi
    echo "$shell_name"
}

install_bash_completion() {
    local comp_file="$1"
    local comp_dir=""
    
    # Try common completion directories
    for d in "/usr/share/bash-completion/completions" \
             "/usr/local/share/bash-completion/completions" \
             "${HOME}/.local/share/bash-completion/completions" \
             "${XDG_DATA_HOME:-$HOME/.local/share}/bash-completion/completions"; do
        if [[ -d "$d" ]] || mkdir -p "$d" 2>/dev/null; then
            comp_dir="$d"
            break
        fi
    done
    
    if [[ -n "$comp_dir" ]]; then
        cp "$comp_file" "${comp_dir}/glm"
        info "Bash completion installed to ${comp_dir}/glm"
        echo "  Source it: source ${comp_dir}/glm"
        echo "  Or restart your shell"
        return 0
    fi
    return 1
}

install_fish_completion() {
    local comp_file="$1"
    local comp_dir="${XDG_CONFIG_HOME:-$HOME/.config}/fish/completions"
    
    mkdir -p "$comp_dir"
    cp "$comp_file" "${comp_dir}/glm.fish"
    info "Fish completion installed to ${comp_dir}/glm.fish"
    return 0
}

install_zsh_completion() {
    local comp_file="$1"
    local comp_dir="${XDG_CONFIG_HOME:-$HOME/.config}/zsh/completions"
    
    mkdir -p "$comp_dir"
    cp "$comp_file" "${comp_dir}/_glm"
    info "Zsh completion installed to ${comp_dir}/_glm"
    echo "  Add to .zshrc: fpath=(${comp_dir} \$fpath)"
    echo "  Then run: autoload -U compinit && compinit"
    return 0
}

# ============================================================
# Main installation
# ============================================================

OS="$(detect_os)"
info "Detected OS: $OS"

if [[ "$OS" == "unknown" ]]; then
    err "Unsupported OS. Requires Linux, macOS, WSL, or Git Bash."
    exit 1
fi

if [[ "$OS" == "gitbash" ]]; then
    warn "Git Bash: background jobs may behave differently. WSL recommended."
fi

# --- Check dependencies ---
step "Checking dependencies..."

if ! command -v go &>/dev/null; then
    err "Go is not installed. Install from https://go.dev/dl/"
    exit 1
fi
info "Found go: $(go version | awk '{print $3}')"

if ! command -v claude &>/dev/null; then
    err "claude CLI not found in PATH."
    err "Install: https://docs.anthropic.com/en/docs/claude-code"
    exit 1
fi
info "Found claude: $(command -v claude)"

# --- Migrate from old /tmp location ---
OLD_CLONE_DIR="/tmp/GoLeM"
if [[ -d "$OLD_CLONE_DIR/.git" && ! -d "$CLONE_DIR" ]]; then
    step "Migrating clone from $OLD_CLONE_DIR to $CLONE_DIR..."
    mkdir -p "$(dirname "$CLONE_DIR")"
    mv "$OLD_CLONE_DIR" "$CLONE_DIR"
    info "Migration complete"
elif [[ -d "$OLD_CLONE_DIR" && -d "$CLONE_DIR" ]]; then
    rm -rf "$OLD_CLONE_DIR"
    info "Removed old clone at $OLD_CLONE_DIR"
fi

# --- Clone or update repo ---
step "Cloning GoLeM..."
if [[ -d "$CLONE_DIR" ]]; then
    info "Updating existing clone at $CLONE_DIR"
    git -C "$CLONE_DIR" pull --quiet 2>/dev/null || {
        warn "Pull failed, re-cloning..."
        rm -rf "$CLONE_DIR"
        git clone --quiet "$REPO_URL" "$CLONE_DIR"
    }
else
    mkdir -p "$(dirname "$CLONE_DIR")"
    info "Cloning repo to $CLONE_DIR"
    git clone --quiet "$REPO_URL" "$CLONE_DIR"
fi

# --- Install binary via go install ---
step "Installing glm binary..."
cd "$CLONE_DIR"
go install ./cmd/glm/
TARGET_BIN="$(go env GOPATH)/bin/glm"
info "Binary installed: $TARGET_BIN"

# --- Ensure GOPATH/bin in PATH ---
GOPATH_BIN="$(go env GOPATH)/bin"
if [[ ":$PATH:" != *":$GOPATH_BIN:"* ]]; then
    warn "$GOPATH_BIN is not in PATH"
    echo "  Add to your shell profile:"
    echo "    export PATH=\"\$GOPATH_BIN:\$PATH\""
fi

# --- Install shell completions ---
step "Installing shell completions..."
DETECTED_SHELL="$(detect_shell)"
COMPLETIONS_DIR="$CLONE_DIR/completions"

if [[ -d "$COMPLETIONS_DIR" ]]; then
    # Always try to install bash and fish completions if available
    if [[ -f "$COMPLETIONS_DIR/glm.bash" ]]; then
        if install_bash_completion "$COMPLETIONS_DIR/glm.bash"; then
            :
        else
            warn "Could not install bash completion"
        fi
    fi
    
    if [[ -f "$COMPLETIONS_DIR/glm.fish" ]]; then
        if command -v fish &>/dev/null; then
            install_fish_completion "$COMPLETIONS_DIR/glm.fish"
        fi
    fi
else
    warn "No completions directory found"
fi

# --- Delegate to glm _install for interactive setup ---
step "Running interactive setup..."
"$TARGET_BIN" _install

echo ""
info "Installation complete!"
echo "  Binary: $TARGET_BIN"
echo "  Source: $CLONE_DIR"
if command -v glm &>/dev/null; then
    echo "  Run: glm --help"
else
    echo "  Run: $TARGET_BIN --help"
fi
