#!/bin/bash

# Define the repo root and hooks directory
REPO_ROOT=$(git rev-parse --show-toplevel)
HOOKS_DIR="$REPO_ROOT/hooks"

# Create symbolic links for each hook
ln -sf "../../scripts/hooks/pre-commit" "$REPO_ROOT/.git/hooks/pre-commit"

# Make sure hooks are executable
chmod +x "$REPO_ROOT/.git/hooks/pre-commit"

echo "Git hooks installed successfully!"
