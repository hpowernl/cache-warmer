#!/bin/bash
set -e

# Cache Warmer Installer
# Automatic installation script for the latest release

REPO="hpowernl/cache-warmer"  # Update to your GitHub repo
INSTALL_DIR="/data/web/cache-warmer"
BINARY_NAME="cache-warmer"

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

echo -e "${GREEN}Cache Warmer Installer${NC}"
echo "========================================"

# Detect platform
OS=$(uname -s)
ARCH=$(uname -m)

if [ "$OS" = "Linux" ]; then
    if [ "$ARCH" = "x86_64" ]; then
        BINARY="cache-warmer-linux-amd64"
    elif [ "$ARCH" = "aarch64" ] || [ "$ARCH" = "arm64" ]; then
        BINARY="cache-warmer-linux-arm64"
    else
        echo -e "${RED}Error: Unsupported architecture: $ARCH${NC}"
        exit 1
    fi
else
    echo -e "${RED}Error: Unsupported OS: $OS${NC}"
    echo "Only Linux is supported by this script."
    exit 1
fi

echo -e "Platform: ${YELLOW}$OS $ARCH${NC}"
echo -e "Binary: ${YELLOW}$BINARY${NC}"
echo ""

# Fetch latest release
echo "Fetching latest release..."
LATEST_RELEASE=$(curl -s "https://api.github.com/repos/$REPO/releases/latest" | grep '"tag_name":' | sed -E 's/.*"([^"]+)".*/\1/')

if [ -z "$LATEST_RELEASE" ]; then
    echo -e "${RED}Error: Could not find latest release${NC}"
    echo "Ensure a release exists on GitHub."
    exit 1
fi

echo -e "Latest version: ${GREEN}$LATEST_RELEASE${NC}"

# Download URL
DOWNLOAD_URL="https://github.com/$REPO/releases/download/$LATEST_RELEASE/$BINARY"
echo -e "Download URL: ${YELLOW}$DOWNLOAD_URL${NC}"
echo ""

# Download binary
echo "Downloading..."
TEMP_FILE=$(mktemp)
if ! curl -L -o "$TEMP_FILE" "$DOWNLOAD_URL"; then
    echo -e "${RED}Error: Download failed${NC}"
    rm -f "$TEMP_FILE"
    exit 1
fi

# Check if download was successful
if [ ! -s "$TEMP_FILE" ]; then
    echo -e "${RED}Error: Downloaded file is empty${NC}"
    rm -f "$TEMP_FILE"
    exit 1
fi

# Make executable
chmod +x "$TEMP_FILE"

# Create install directory if it doesn't exist
if [ ! -d "$INSTALL_DIR" ]; then
    echo "Creating install directory..."
    mkdir -p "$INSTALL_DIR"
fi

# Install binary
echo "Installing to $INSTALL_DIR/$BINARY_NAME..."
mv "$TEMP_FILE" "$INSTALL_DIR/$BINARY_NAME"

echo ""
echo -e "${GREEN}✅ Installation successful!${NC}"
echo ""
echo "You can now use cache-warmer:"
echo -e "  ${YELLOW}$INSTALL_DIR/$BINARY_NAME init${NC}     # Create config"
echo -e "  ${YELLOW}$INSTALL_DIR/$BINARY_NAME status${NC}   # View status"
echo -e "  ${YELLOW}$INSTALL_DIR/$BINARY_NAME run${NC}      # Start warmer"
echo ""
echo "Installed version: $LATEST_RELEASE"
echo ""
echo "Consider adding to PATH:"
echo -e "  ${YELLOW}export PATH=\"\$PATH:$INSTALL_DIR\"${NC}"
