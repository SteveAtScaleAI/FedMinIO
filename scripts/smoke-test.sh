#!/usr/bin/env bash
# smoke-test.sh — quick sanity check against a running FedMinIO server
#
# Prerequisites: aws CLI installed
# Usage:
#   ./scripts/smoke-test.sh
#
# Override defaults with env vars:
#   ENDPOINT=http://localhost:9000 MINIO_ROOT_USER=admin MINIO_ROOT_PASSWORD=password123 ./scripts/smoke-test.sh
#
# Does NOT touch ~/.aws — uses isolated temp credentials for the duration.

set -euo pipefail

ENDPOINT="${ENDPOINT:-http://localhost:9000}"
USER="${MINIO_ROOT_USER:-admin}"
PASS="${MINIO_ROOT_PASSWORD:-password123}"
BUCKET="smoke-test-$$"

# Isolated temp dir for AWS config — never touches ~/.aws
AWS_TMP=$(mktemp -d)
TMP_FILE=$(mktemp)

cleanup() {
    rm -rf "$AWS_TMP" "$TMP_FILE"
}
trap cleanup EXIT

# Write throwaway credentials into the temp dir
cat > "$AWS_TMP/credentials" <<EOF
[default]
aws_access_key_id = $USER
aws_secret_access_key = $PASS
EOF

cat > "$AWS_TMP/config" <<EOF
[default]
region = us-east-1
output = text
EOF

# All aws calls use the temp config — no global state modified
S3="aws --endpoint-url $ENDPOINT s3"
S3API="aws --endpoint-url $ENDPOINT s3api"

export AWS_CONFIG_FILE="$AWS_TMP/config"
export AWS_SHARED_CREDENTIALS_FILE="$AWS_TMP/credentials"

echo "endpoint : $ENDPOINT"
echo "bucket   : $BUCKET"
echo ""

echo "→ create bucket"
$S3 mb "s3://$BUCKET"

echo "→ upload /etc/hosts"
$S3 cp /etc/hosts "s3://$BUCKET/hosts.txt"

echo "→ list bucket"
$S3 ls "s3://$BUCKET/"

echo "→ download and verify"
$S3 cp "s3://$BUCKET/hosts.txt" "$TMP_FILE"
diff /etc/hosts "$TMP_FILE"
echo "  content matches ✓"

echo "→ upload a 1MB random object"
dd if=/dev/urandom bs=1024 count=1024 2>/dev/null | $S3 cp - "s3://$BUCKET/random.bin"

echo "→ list objects"
$S3 ls "s3://$BUCKET/"

echo "→ delete objects"
$S3 rm "s3://$BUCKET/hosts.txt"
$S3 rm "s3://$BUCKET/random.bin"

echo "→ remove bucket"
$S3 rb "s3://$BUCKET"

echo ""
echo "smoke test passed ✓"
