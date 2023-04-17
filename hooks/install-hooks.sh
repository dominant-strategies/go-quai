#!/bin/bash

# Change to the root directory of your project
cd $(git rev-parse --show-toplevel)

# Symlink the pre-commit hook
ln -sf $(pwd)/hooks/pre-commit .git/hooks/pre-commit

# Detect the operating system
OS="$(uname)"

# Check if golangci-lint is installed, and install it if necessary
if ! command -v golangci-lint >/dev/null 2>&1; then
  echo "golangci-lint not found. Installing..."

  case $OS in
    Linux)
      # binary will be $(go env GOPATH)/bin/golangci-lint
      curl -sSfL https://raw.githubusercontent.com/golangci/golangci-lint/master/install.sh | sh -s -- -b $(go env GOPATH)/bin v1.52.2
      ;;
    Darwin)
      # Install or upgrade golangci-lint for macOS
      if brew ls --versions golangci-lint >/dev/null; then
        brew upgrade golangci-lint
      else
        brew install golangci-lint
      fi
      ;;
    *)
      echo "Unsupported OS: $OS"
      exit 1
      ;;
  esac
fi

echo "Setup complete."

