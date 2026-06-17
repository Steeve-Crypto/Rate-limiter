#!/usr/bin/env bash
# Generate self-signed CA + server + client certs for gRPC mTLS demo.
# Usage: ./scripts/gen-grpc-certs.sh [output-dir]
# Produces: ca.crt, server.crt, server.key, client.crt, client.key
set -euo pipefail

OUTDIR=${1:-certs}
mkdir -p "$OUTDIR"
cd "$OUTDIR"

echo "==> Generating CA"
openssl req -x509 -new -nodes -newkey rsa:2048 -keyout ca.key -out ca.crt \
  -subj "/CN=RateLimiterCA" -days 3650

echo "==> Generating server cert (for rate-limiter)"
openssl req -new -nodes -newkey rsa:2048 -keyout server.key -out server.csr \
  -subj "/CN=rate-limiter"
openssl x509 -req -in server.csr -CA ca.crt -CAkey ca.key -CAcreateserial \
  -out server.crt -days 365 -extfile <(echo "subjectAltName = DNS:localhost,IP:127.0.0.1")

echo "==> Generating client cert (for callers)"
openssl req -new -nodes -newkey rsa:2048 -keyout client.key -out client.csr \
  -subj "/CN=rate-limiter-client"
openssl x509 -req -in client.csr -CA ca.crt -CAkey ca.key -CAcreateserial \
  -out client.crt -days 365

rm -f *.csr *.srl

echo "==> Done. Files in $OUTDIR/"
echo "Server: server.crt + server.key"
echo "Client (mTLS): client.crt + client.key + ca.crt (trust)"
echo ""
echo "Run server example:"
echo "  ./rate-limiter -port 8080 -grpc-tls-cert certs/server.crt -grpc-tls-key certs/server.key -grpc-tls-ca certs/ca.crt"
echo ""
echo "Example Go client (mTLS):"
echo "  creds, _ := credentials.NewClientTLSFromFile(\"certs/ca.crt\", \"localhost\")"
echo "  conn, _ := grpc.Dial(\"localhost:8081\", grpc.WithTransportCredentials(creds), grpc.WithPerRPCCredentials(...)) # plus client cert if strict"
ls -l
