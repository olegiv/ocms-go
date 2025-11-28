#!/bin/bash
set -e

# oCMS Asset Build Script
# Compiles SCSS to CSS using dart-sass

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"

SCSS_DIR="$PROJECT_DIR/web/static/scss"
DIST_DIR="$PROJECT_DIR/web/static/dist"

# Ensure dist directory exists
mkdir -p "$DIST_DIR"

# Check if sass is installed
if ! command -v sass &> /dev/null; then
    echo "Error: sass (dart-sass) is not installed."
    echo "Install it with: brew install sass/sass/sass (macOS) or npm install -g sass"
    exit 1
fi

# Compile SCSS
echo "Compiling SCSS..."
sass "$SCSS_DIR/main.scss" "$DIST_DIR/main.css" --style=compressed --no-source-map

echo "Assets built successfully!"
echo "  -> $DIST_DIR/main.css"
