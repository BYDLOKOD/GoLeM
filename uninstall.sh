#!/usr/bin/env bash
set -euo pipefail

# One-liner: curl -sL https://raw.githubusercontent.com/veschin/GoLeM/main/uninstall.sh | bash
# Or: bash <(curl -sL https://raw.githubusercontent.com/veschin/GoLeM/main/uninstall.sh)

CLONE_DIR="${HOME}/.local/share/GoLeM"

RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m'

info()  { echo -e "${GREEN}[-]${NC} $1"; }
warn()  { echo -e "${YELLOW}[!]${NC} $1"; }
err()   { echo -e "${RED}[x]${NC} $1" >&2; }

ask_yn() {
    local prompt="$1" default="${2:-n}" yn
    if [[ "$default" == "y" ]]; then
        read -rp "$prompt [Y/n]: " yn; yn="${yn:-y}"
    else
        read -rp "$prompt [y/N]: " yn; yn="${yn:-n}"
    fi
    [[ "$yn" =~ ^[Yy] ]]
}

# --- Try glm _uninstall first ---
if command -v glm &>/dev/null; then
    info "Running glm _uninstall..."
    glm _uninstall
    exit 0
fi

# --- Fallback: manual cleanup if glm is not in PATH ---
warn "glm not found in PATH, performing manual cleanup..."

# Remove completions
for comp_dir in "/usr/share/bash-completion/completions" \
                "/usr/local/share/bash-completion/completions" \
                "${HOME}/.local/share/bash-completion/completions" \
                "${XDG_DATA_HOME:-$HOME/.local/share}/bash-completion/completions"; do
    if [[ -f "${comp_dir}/glm" ]]; then
        rm -f "${comp_dir}/glm"
        info "Removed: ${comp_dir}/glm"
    fi
done

FISH_COMP_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/fish/completions"
if [[ -f "${FISH_COMP_DIR}/glm.fish" ]]; then
    rm -f "${FISH_COMP_DIR}/glm.fish"
    info "Removed: ${FISH_COMP_DIR}/glm.fish"
fi

ZSH_COMP_DIR="${XDG_CONFIG_HOME:-$HOME/.config}/zsh/completions"
if [[ -f "${ZSH_COMP_DIR}/_glm" ]]; then
    rm -f "${ZSH_COMP_DIR}/_glm"
    info "Removed: ${ZSH_COMP_DIR}/_glm"
fi

# Remove GLM section from CLAUDE.md
CLAUDE_MD="${HOME}/.claude/CLAUDE.md"
MARKER_START="<!-- GLM-SUBAGENT-START -->"
MARKER_END="<!-- GLM-SUBAGENT-END -->"

if [[ -f "$CLAUDE_MD" ]]; then
    if grep -q "$MARKER_START" "$CLAUDE_MD"; then
        # Remove section between markers (inclusive)
        sed -i.bak "/$MARKER_START/,/$MARKER_END/d" "$CLAUDE_MD" 2>/dev/null || \
            sed -i "/$MARKER_START/,/$MARKER_END/d" "$CLAUDE_MD"
        rm -f "${CLAUDE_MD}.bak"
        info "Removed GLM section from $CLAUDE_MD"
    fi
fi

# Ask about config and credentials
CONFIG_DIR="${HOME}/.config/GoLeM"
if [[ -d "$CONFIG_DIR" ]]; then
    if ask_yn "Remove config directory ($CONFIG_DIR)?"; then
        rm -rf "$CONFIG_DIR"
        info "Removed: $CONFIG_DIR"
    fi
fi

# Ask about clone directory
if [[ -d "$CLONE_DIR" ]]; then
    if ask_yn "Remove source clone ($CLONE_DIR)?"; then
        rm -rf "$CLONE_DIR"
        info "Removed: $CLONE_DIR"
    fi
fi

info "Uninstall complete"
