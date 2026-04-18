#!/bin/bash
set -e

cd "$(dirname "$0")"

# If .env already exists (local dev), use it directly
if [ -f .env ]; then
    echo "[start] Using local .env file"
else
    # Production: pull from AWS SSM Parameter Store
    echo "[start] Pulling secrets from AWS SSM..."
    aws ssm get-parameters-by-path \
        --path "/spread-arb/" \
        --with-decryption \
        --query "Parameters[].{Name:Name,Value:Value}" \
        --output text \
        | awk '{split($1,a,"/"); print a[3]"="$2}' > .env

    if [ ! -s .env ]; then
        echo "[start] ERROR: Failed to pull secrets from SSM. Is IAM role configured?"
        exit 1
    fi
    echo "[start] Secrets loaded from SSM"
fi

docker compose pull
docker compose up -d --build
echo "[start] Services started"
docker compose ps
