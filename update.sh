#!/usr/bin/env bash
set -euo pipefail

PROJECT_DIR="/media/hamish/pi-storage/Projects/HealthDashboard"
GO="/usr/local/go/bin/go"
OUTPUT="/usr/local/bin/health-dashboard"
SERVICE="healthdashboard.service"

cd "$PROJECT_DIR"
git pull --ff-only
sudo "$GO" build -o "$OUTPUT" .
sudo systemctl restart "$SERVICE"
echo "Service updated and restarted"

