#!/bin/bash
set -e

cd "$(dirname "$0")"

echo "Building invest-api..."
go build -o invest-api .

echo "Starting invest-api on port ${PORT:-8090}..."
./invest-api
