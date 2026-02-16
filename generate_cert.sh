#!/usr/bin/env bash
set -euo pipefail

CERT_DIR="certs"
mkdir -p "$CERT_DIR"

openssl req -x509 -newkey ec -pkeyopt ec_paramgen_curve:P-256 \
  -keyout "$CERT_DIR/key.pem" -out "$CERT_DIR/cert.pem" \
  -days 365 -nodes -subj "/O=WebTransport Dev/CN=localhost" \
  -addext "subjectAltName=DNS:localhost"

echo "Certificates written to $CERT_DIR/"
