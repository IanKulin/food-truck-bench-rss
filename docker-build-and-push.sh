#!/usr/bin/env bash
# Build the image for linux/amd64 and push it to GitHub Container Registry.
# Requires a prior `docker login ghcr.io` (PAT with write:packages).
# Usage: ./push.sh [tag]   (default tag: latest)
set -euo pipefail

cd "$(dirname "$0")"

IMAGE=ghcr.io/iankulin/food-truck-bench-rss
TAG=${1:-latest}

docker buildx build \
  --platform linux/amd64 \
  --tag "$IMAGE:$TAG" \
  --push \
  .

echo "Pushed $IMAGE:$TAG"
