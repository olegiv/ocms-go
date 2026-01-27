#!/bin/bash
set -e

# oCMS Asset Build Script
# Compiles SCSS to CSS and copies JS dependencies

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
PROJECT_DIR="$(dirname "$SCRIPT_DIR")"

SCSS_DIR="$PROJECT_DIR/web/static/scss"
DIST_DIR="$PROJECT_DIR/web/static/dist"
JS_DIR="$PROJECT_DIR/web/static/dist/js"

# Ensure directories exist
mkdir -p "$DIST_DIR" "$JS_DIR"

# Install and copy npm dependencies
echo "Installing npm dependencies..."
cd "$PROJECT_DIR"
if ! command -v npm &> /dev/null; then
    echo "Error: npm is not installed."
    exit 1
fi
npm install --silent
npm run copy-deps --silent
echo "  -> $JS_DIR/htmx.min.js"
echo "  -> $JS_DIR/alpine.min.js"

# Copy source JS files to dist
echo "Copying source JS files..."
JS_SRC_DIR="$PROJECT_DIR/web/static/js"
if [ -d "$JS_SRC_DIR" ] && [ "$(ls -A "$JS_SRC_DIR" 2>/dev/null)" ]; then
    cp "$JS_SRC_DIR"/*.js "$JS_DIR/" 2>/dev/null || true
    for f in "$JS_SRC_DIR"/*.js; do
        [ -f "$f" ] && echo "  -> $JS_DIR/$(basename "$f")"
    done
fi

# Check for outdated packages
OUTDATED=$(npm outdated --json 2>/dev/null || true)
if [ -n "$OUTDATED" ] && [ "$OUTDATED" != "{}" ]; then
    echo ""
    echo "Outdated npm packages:"
    echo "$OUTDATED" | grep -E '"(current|wanted|latest)"' | head -20
    echo ""
fi

# Check if sass is installed
if ! command -v sass &> /dev/null; then
    echo "Error: sass (dart-sass) is not installed."
    echo "Install it with: brew install sass/sass/sass (macOS) or npm install -g sass"
    exit 1
fi

# Compile SCSS
echo "Compiling SCSS..."
sass "$SCSS_DIR/main.scss" "$DIST_DIR/main.css" --style=compressed --no-source-map
echo "  -> $DIST_DIR/main.css"

echo "Assets built successfully!"
