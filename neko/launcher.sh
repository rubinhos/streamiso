#!/bin/bash
# Runs inside the Neko container (as the "neko" user), replacing the stock VLC startup.
# Watches the files written by the catalog service:
#   state/current : path of an ISO -> open the disc in VLC with menus (dvdnav);
#                   "cd-s16l:<bin>" -> play a raw audio CD image (BIN/CUE) as one stream;
#                   empty/missing  -> close VLC (the screen goes blank)
# VLC's remote-control (rc) interface listens on port 4212 for the catalog service
# (play/pause, seek, volume, tracks, DVD menu). It is only reachable inside the compose network.

STATE_FILE="${STATE_FILE:-/state/current}"
MEDIA_PREFIX="${MEDIA_PREFIX:-/media/}"
RC_ADDR="0.0.0.0"
RC_PORT="4212"

CUR=""
VLC_PID=""

stop_vlc() {
  if [ -n "$VLC_PID" ] && kill -0 "$VLC_PID" 2>/dev/null; then
    kill "$VLC_PID" 2>/dev/null
    for _ in 1 2 3 4 5 6 7 8 9 10; do
      kill -0 "$VLC_PID" 2>/dev/null || break
      sleep 0.3
    done
    kill -9 "$VLC_PID" 2>/dev/null
  fi
  VLC_PID=""
}

trap 'stop_vlc; exit 0' TERM INT

echo "[launcher] watching $STATE_FILE"

while true; do
  WANT=""
  [ -r "$STATE_FILE" ] && { IFS= read -r WANT < "$STATE_FILE" || true; }

  if [ "$WANT" != "$CUR" ]; then
    stop_vlc
    CUR="$WANT"

    if [ -n "$WANT" ]; then
      # The state line is a plain ISO path (DVD), or "<mode>:<path>" for other media:
      #   cd-s16l:<bin>  raw audio CD image, little-endian samples (starts paused: the catalog page
      #                  resumes it once the browser's audio is connected, so no start is lost)
      #   cd-s16b:<bin>  raw audio CD image, big-endian samples
      #   file:<path>    an ordinary audio file (CUE sheets that point at WAV/FLAC)
      MODE="dvd"; TARGET="$WANT"; FOURCC=""
      case "$WANT" in
        cd-s16l:*) MODE="cd"; FOURCC="s16l"; TARGET="${WANT#*:}" ;;
        cd-s16b:*) MODE="cd"; FOURCC="s16b"; TARGET="${WANT#*:}" ;;
        file:*)    MODE="file"; TARGET="${WANT#*:}" ;;
      esac

      if [[ "$TARGET" == "$MEDIA_PREFIX"* ]] && [[ "$TARGET" != *"/../"* ]] && [ -f "$TARGET" ]; then
        echo "[launcher] opening ($MODE): $TARGET"
        EXT="${TARGET##*.}"; EXT="${EXT//[^A-Za-z0-9]/}"
        LINK="/tmp/current-media.${EXT:-bin}"
        ln -sf "$TARGET" "$LINK"
        COMMON=(--fullscreen --no-qt-fs-controller --qt-minimal-view --no-qt-system-tray --no-osd
                --no-video-title-show --mouse-hide-timeout=500 --no-qt-privacy-ask --no-qt-error-dialogs
                --extraintf=rc "--rc-host=$RC_ADDR:$RC_PORT")
        case "$MODE" in
          dvd)  vlc "${COMMON[@]}" --start-paused "dvdnav://$LINK" & ;;
          cd)   vlc "${COMMON[@]}" --start-paused --demux=rawaud --rawaud-channels=2 --rawaud-samplerate=44100 \
                    "--rawaud-fourcc=$FOURCC" "$LINK" & ;;
          file) vlc "${COMMON[@]}" "$LINK" & ;;
        esac
        VLC_PID=$!
      else
        echo "[launcher] path refused or missing: $WANT"
      fi
    else
      echo "[launcher] stopped"
    fi
  fi

  sleep 0.3
done
