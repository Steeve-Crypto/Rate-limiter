#!/usr/bin/env bash
# Generate clients from OpenAPI spec for multiple languages.
# Requires: openapi-generator-cli (npm or docker) or use the built-in manual client as fallback.
#
# Usage:
#   ./scripts/generate-clients.sh
#   ./scripts/generate-clients.sh python
#   ./scripts/generate-clients.sh typescript

set -euo pipefail

ROOT="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SPEC="$ROOT/openapi.yaml"
OUT="$ROOT/client/generated"

mkdir -p "$OUT"

LANG="${1:-python}"

echo "==> Generating $LANG client from $SPEC"

if command -v openapi-generator-cli >/dev/null 2>&1; then
  GEN="openapi-generator-cli"
elif command -v docker >/dev/null 2>&1; then
  GEN="docker run --rm -v ${ROOT}:${ROOT} -w ${ROOT} openapitools/openapi-generator-cli"
else
  echo "No openapi-generator found. Falling back to manual clients in client/ and client/python/"
  echo "Install with: npm i -g @openapitools/openapi-generator-cli"
  echo "or use the provided hand-written clients."
  exit 0
fi

case "$LANG" in
  python)
    $GEN generate -i "$SPEC" -g python -o "$OUT/python" --package-name rate_limiter_client_gen \
      --additional-properties=packageVersion=1.2.0,projectName=rate-limiter-client
    echo "Python client generated to $OUT/python"
    ;;
  typescript|ts)
    $GEN generate -i "$SPEC" -g typescript-fetch -o "$OUT/typescript" \
      --additional-properties=supportsES6=true,npmName=rate-limiter-client,npmVersion=1.2.0
    echo "TS client generated to $OUT/typescript"
    ;;
  go)
    $GEN generate -i "$SPEC" -g go -o "$OUT/go" --package-name ratelimiterclient
    echo "Go client generated to $OUT/go (note: prefer the maintained client/ package)"
    ;;
  *)
    echo "Supported: python, typescript, go"
    exit 1
    ;;
esac

echo "Done. Review and integrate generated code."
