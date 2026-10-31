#!/bin/bash
# Runs as root when the container starts: injects neko-inject.html into the Neko web page.
# Idempotent: a previously injected snippet (marked streamiso-patch) is removed first, so a
# container restart picks up an updated neko-inject.html.
IDX="${IDX:-/var/www/index.html}"
INJECT="${INJECT:-/opt/streamiso/neko-inject.html}"
MARK="streamiso-patch"
TAG="</head>"

[ -f "$IDX" ] || { echo "[patch] $IDX not found"; exit 0; }
[ -f "$INJECT" ] || { echo "[patch] $INJECT not found"; exit 0; }
shopt -u patsub_replacement 2>/dev/null
CONTENT="$(cat "$IDX")"
if [[ "$CONTENT" == *"$MARK"* ]]; then
  # drop everything from the marker comment up to the end of our script block
  BEFORE="${CONTENT%%"<!-- $MARK -->"*}"
  AFTER="${CONTENT#*"streamiso-patch-js"}"
  AFTER="${AFTER#*"</script>"}"
  CONTENT="$BEFORE$AFTER"
fi
SNIP="$(cat "$INJECT")"
case "$CONTENT" in
  *"$TAG"*) ;;
  *) echo "[patch] could not find $TAG in $IDX; nothing changed"; exit 0 ;;
esac
printf '%s\n' "${CONTENT/"$TAG"/"$SNIP$TAG"}" > "$IDX"
echo "[patch] applied to $IDX"
