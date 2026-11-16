#!/usr/bin/env bash
set -euo pipefail
cd "$(dirname "$0")"
APP="one-api-menubar"
VERSION="0.1.0"
DIST="dist"
STAGE="$(mktemp -d)"
cp -R "${DIST}/${APP}.app" "${STAGE}/${APP}.app"
ln -s /Applications "${STAGE}/Applications"
rm -f "${APP}-${VERSION}.dmg"
hdiutil create -volname "${APP}" -srcfolder "${STAGE}" -ov -format UDZO "${APP}-${VERSION}.dmg"
rm -rf "${STAGE}"
echo "built ${APP}-${VERSION}.dmg"
