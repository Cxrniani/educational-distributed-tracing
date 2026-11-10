#!/usr/bin/env bash
set -euo pipefail

BASE_URL="${BASE_URL:-http://localhost:8080}"
ROUNDS="${ROUNDS:-50}"
ITEMS=(book book book notebook monitor cable fail ghost)

for ((i = 1; i <= ROUNDS; i++)); do
  item="${ITEMS[RANDOM % ${#ITEMS[@]}]}"
  qty=$((RANDOM % 3 + 1))
  code=$(curl -s -o /dev/null -w '%{http_code}' "${BASE_URL}/checkout?item=${item}&qty=${qty}")
  printf '%3d  item=%-9s qty=%d  ->  %s\n' "$i" "$item" "$qty" "$code"
  sleep 0.2
done
