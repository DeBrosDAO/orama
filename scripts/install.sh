#!/usr/bin/env bash
set -euo pipefail

# Orama CLI installer
# Builds the CLI and adds `orama` to your PATH.
# Usage: ./scripts/install.sh [--shell fish|zsh|bash]

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROJECT_DIR="$(cd "$SCRIPT_DIR/.." && pwd)"
BIN_DIR="$HOME/.local/bin"
BIN_PATH="$BIN_DIR/orama"

# --- Parse args ---
SHELL_NAME=""
while [[ $# -gt 0 ]]; do
  case "$1" in
    --shell) SHELL_NAME="$2"; shift 2 ;;
    -h|--help)
      echo "Usage: ./scripts/install.sh [--shell fish|zsh|bash]"
      echo ""
      echo "Builds the Orama CLI and installs 'orama' to ~/.local/bin."
      echo "If --shell is not provided, auto-detects from \$SHELL."
      exit 0 ;;
    *) echo "Unknown option: $1"; exit 1 ;;
  esac
done

# Auto-detect shell
if [[ -z "$SHELL_NAME" ]]; then
  case "$SHELL" in
    */fish) SHELL_NAME="fish" ;;
    */zsh)  SHELL_NAME="zsh" ;;
    */bash) SHELL_NAME="bash" ;;
    *)      SHELL_NAME="unknown" ;;
  esac
fi

echo "==> Shell: $SHELL_NAME"

# --- Build ---
echo "==> Building Orama CLI..."
(cd "$PROJECT_DIR" && make build)

# --- Install binary ---
mkdir -p "$BIN_DIR"
cp -f "$PROJECT_DIR/bin/orama" "$BIN_PATH"
chmod +x "$BIN_PATH"
echo "==> Installed $BIN_PATH"

# --- Ensure PATH ---
add_to_path() {
  local rc_file="$1"
  local line="$2"

  if [[ -f "$rc_file" ]] && grep -qF "$line" "$rc_file"; then
    echo "==> PATH already configured in $rc_file"
  else
    echo "" >> "$rc_file"
    echo "$line" >> "$rc_file"
    echo "==> Added PATH to $rc_file"
  fi
}

case "$SHELL_NAME" in
  fish)
    FISH_CONFIG="$HOME/.config/fish/config.fish"
    mkdir -p "$(dirname "$FISH_CONFIG")"
    add_to_path "$FISH_CONFIG" "fish_add_path $BIN_DIR"
    ;;
  zsh)
    add_to_path "$HOME/.zshrc" "export PATH=\"$BIN_DIR:\$PATH\""
    ;;
  bash)
    add_to_path "$HOME/.bashrc" "export PATH=\"$BIN_DIR:\$PATH\""
    ;;
  *)
    echo "==> Unknown shell. Add this to your shell config manually:"
    echo "    export PATH=\"$BIN_DIR:\$PATH\""
    ;;
esac

# --- Verify ---
VERSION=$("$BIN_PATH" version 2>/dev/null || echo "unknown")
echo ""
echo "==> Orama CLI ${VERSION} installed!"
echo "    Run: orama --help"
echo ""
if [[ "$SHELL_NAME" != "unknown" ]]; then
  echo "    Restart your terminal or run:"
  case "$SHELL_NAME" in
    fish) echo "      source ~/.config/fish/config.fish" ;;
    zsh)  echo "      source ~/.zshrc" ;;
    bash) echo "      source ~/.bashrc" ;;
  esac
fi
