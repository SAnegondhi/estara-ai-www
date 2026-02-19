#!/bin/bash
# Install pre-commit hooks for www/
# Run this once after cloning the repository

set -e

echo "🔧 Installing Git hooks for www..."

# Ensure we're in www/ directory
if [ ! -f "go.mod" ]; then
    echo "❌ Must run from www/ directory"
    exit 1
fi

# Get the git directory (handles both .git and worktrees)
GIT_DIR=$(git rev-parse --git-dir)
HOOKS_DIR="$GIT_DIR/hooks"

# Create hooks directory if it doesn't exist
mkdir -p "$HOOKS_DIR"

# Copy pre-commit hook
HOOK_SOURCE="scripts/pre-commit"
HOOK_TARGET="$HOOKS_DIR/pre-commit"

if [ ! -f "$HOOK_SOURCE" ]; then
    echo "❌ Pre-commit hook script not found at $HOOK_SOURCE"
    exit 1
fi

# Check if hook already exists
if [ -f "$HOOK_TARGET" ]; then
    echo "⚠️  Pre-commit hook already exists at $HOOK_TARGET"
    read -p "Overwrite? (y/n) " -n 1 -r
    echo
    if [[ ! $REPLY =~ ^[Yy]$ ]]; then
        echo "Aborted"
        exit 1
    fi
    rm "$HOOK_TARGET"
fi

# Copy and make executable
cp "$HOOK_SOURCE" "$HOOK_TARGET"
chmod +x "$HOOK_TARGET"

echo "✅ Pre-commit hook installed successfully!"
echo ""
echo "The hook will run automatically before each commit and check:"
echo "  1. sqlc generate (validates queries against schema)"
echo "  2. go build ./... (ensures code compiles)"
echo "  3. Migration safety checks (warnings for common issues)"
echo "  4. go fmt (ensures consistent formatting)"
echo ""
echo "To bypass the hook (not recommended):"
echo "  git commit --no-verify"
echo ""
echo "To uninstall:"
echo "  rm $HOOK_TARGET"
echo ""
