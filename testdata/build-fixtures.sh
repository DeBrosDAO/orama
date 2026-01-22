#!/bin/bash
set -e

echo "🔨 Building E2E test fixtures..."

# Get the directory of this script
SCRIPT_DIR="$( cd "$( dirname "${BASH_SOURCE[0]}" )" && pwd )"
cd "$SCRIPT_DIR"

# Create tarballs directory
mkdir -p tarballs

# Build React Vite app
echo ""
echo "📦 Building React Vite app..."
cd apps/react-vite
if [ ! -d "node_modules" ]; then
    echo "   Installing dependencies..."
    npm install
fi
echo "   Building..."
npm run build
echo "   Creating tarball..."
tar -czf "$SCRIPT_DIR/tarballs/react-vite.tar.gz" -C dist .
cd "$SCRIPT_DIR"

# Build Next.js app
echo ""
echo "📦 Building Next.js app..."
cd apps/nextjs-ssr
if [ ! -d "node_modules" ]; then
    echo "   Installing dependencies..."
    npm install
fi
echo "   Building..."
npm run build
echo "   Creating tarball..."
tar -czf "$SCRIPT_DIR/tarballs/nextjs-ssr.tar.gz" .next/ package.json next.config.js
cd "$SCRIPT_DIR"

# Build Go backend
echo ""
echo "📦 Building Go backend..."
cd apps/go-backend
echo "   Building Linux binary..."
make build
echo "   Creating tarball..."
tar -czf "$SCRIPT_DIR/tarballs/go-backend.tar.gz" api
make clean
cd "$SCRIPT_DIR"

echo ""
echo "✅ All test fixtures built successfully!"
echo ""
echo "Generated tarballs:"
ls -lh tarballs/
echo ""
echo "Ready for E2E testing!"
