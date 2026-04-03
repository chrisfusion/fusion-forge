#!/usr/bin/env bash
set -euo pipefail

# Required env vars
: "${INDEX_BACKEND_URL:?INDEX_BACKEND_URL is required}"
: "${JOB_ID:?JOB_ID is required}"
: "${VERSION_NUMBER:?VERSION_NUMBER is required}"
: "${VENV_NAME:?VENV_NAME is required}"

WORKSPACE=/workspace
VENV_DIR="${WORKSPACE}/venv"
ARCHIVE="${WORKSPACE}/${VENV_NAME}.tar.gz"

echo "[forge-builder] Starting venv build: job=${JOB_ID} name=${VENV_NAME}"

# Create virtual environment
python3 -m venv "${VENV_DIR}"
echo "[forge-builder] venv created"

# Install packages
"${VENV_DIR}/bin/pip" install --no-cache-dir --quiet --upgrade pip
"${VENV_DIR}/bin/pip" install --no-cache-dir -r "${WORKSPACE}/requirements.txt"
echo "[forge-builder] pip install complete"

# Archive — portable: strip leading workspace path
tar czf "${ARCHIVE}" -C "${WORKSPACE}" venv
echo "[forge-builder] archive created: ${ARCHIVE} ($(du -sh "${ARCHIVE}" | cut -f1))"

# Upload artifact to index-backend
UPLOAD_URL="${INDEX_BACKEND_URL}/api/v1/jobs/${JOB_ID}/versions/${VERSION_NUMBER}/artifacts"
echo "[forge-builder] uploading to ${UPLOAD_URL}"

HTTP_STATUS=$(curl --silent --show-error --fail-with-body \
  -o /tmp/upload_response.txt \
  -w "%{http_code}" \
  -X POST "${UPLOAD_URL}" \
  -F "file=@${ARCHIVE};type=application/gzip")

if [ "${HTTP_STATUS}" -ge 200 ] && [ "${HTTP_STATUS}" -lt 300 ]; then
  echo "[forge-builder] artifact uploaded (HTTP ${HTTP_STATUS})"
else
  echo "[forge-builder] upload FAILED (HTTP ${HTTP_STATUS}):" >&2
  cat /tmp/upload_response.txt >&2
  exit 1
fi

echo "[forge-builder] build complete"
