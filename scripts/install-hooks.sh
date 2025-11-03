#!/bin/bash

# Install git hooks from .githooks/ to .git/hooks/
# This ensures the pre-push hook runs automatically

set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
GITHOOKS_DIR="$REPO_ROOT/.githooks"
GIT_HOOKS_DIR="$REPO_ROOT/.git/hooks"

if [ ! -d "$GITHOOKS_DIR" ]; then
    echo "Error: .githooks directory not found at $GITHOOKS_DIR"
    exit 1
fi

if [ ! -d "$GIT_HOOKS_DIR" ]; then
    echo "Error: .git/hooks directory not found at $GIT_HOOKS_DIR"
    echo "Are you in a git repository?"
    exit 1
fi

echo "Installing git hooks..."

# Copy all hooks from .githooks/ to .git/hooks/
for hook in "$GITHOOKS_DIR"/*; do
    if [ -f "$hook" ]; then
        hook_name=$(basename "$hook")
        dest="$GIT_HOOKS_DIR/$hook_name"
        
        echo "  Installing $hook_name..."
        cp "$hook" "$dest"
        chmod +x "$dest"
        
        # Make sure the hook can find the repo root
        # The hooks already use relative paths, so this should work
    fi
done

echo "✓ Git hooks installed successfully!"
echo ""
echo "The following hooks are now active:"
ls -1 "$GIT_HOOKS_DIR"/* 2>/dev/null | xargs -n1 basename || echo "  (none)"

