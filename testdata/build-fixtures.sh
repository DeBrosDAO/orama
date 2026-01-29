#!/bin/bash
set -e

echo "Building E2E test fixtures..."

# Get the directory of this script
SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
cd "$SCRIPT_DIR"

# Create tarballs directory
mkdir -p tarballs

# Build React app (Vite static)
echo ""
echo "Building React app..."
cd apps/react-app
if [ ! -d "node_modules" ]; then
    echo "   Installing dependencies..."
    npm install
fi
echo "   Building..."
npm run build
echo "   Creating tarball..."
tar -czf "$SCRIPT_DIR/tarballs/react-app.tar.gz" -C dist .
cd "$SCRIPT_DIR"

# Build Next.js SSR app
echo ""
echo "Building Next.js SSR app..."
cd apps/nextjs-app
if [ ! -d "node_modules" ]; then
    echo "   Installing dependencies..."
    npm install
fi
echo "   Building..."
npm run build
echo "   Creating standalone tarball..."
# Copy static and public into standalone
cp -r .next/static .next/standalone/.next/static 2>/dev/null || true
cp -r public .next/standalone/public 2>/dev/null || true
tar -czf "$SCRIPT_DIR/tarballs/nextjs-app.tar.gz" -C .next/standalone .
cd "$SCRIPT_DIR"

# Build Go backend
echo ""
echo "Building Go backend..."
cd apps/go-api
echo "   Building Linux binary..."
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build -o app .
echo "   Creating tarball..."
tar -czf "$SCRIPT_DIR/tarballs/go-api.tar.gz" app
rm -f app
cd "$SCRIPT_DIR"

# Build Node.js backend
echo ""
echo "Building Node.js backend..."
cd apps/node-api
echo "   Creating tarball..."
tar -czf "$SCRIPT_DIR/tarballs/node-api.tar.gz" index.js package.json
cd "$SCRIPT_DIR"

echo ""
echo "All test fixtures built successfully!"
echo ""
echo "Generated tarballs:"
ls -lh tarballs/
echo ""
echo "Ready for E2E testing!"
