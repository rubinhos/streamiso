#!/bin/bash
# Runs inside the Neko container (as the "neko" user), replacing the stock VLC startup.
# Watches the files written by the catalog service:
#   state/current : path of an ISO -> open the disc in VLC with menus (dvdnav);
#                   "bluray:<iso>" -> open a Blu-ray image with its menus (libbluray, BD-J in Java);
#                   "cd-s16l:<bin>" -> play a raw audio CD image (BIN/CUE) as one stream;
#                   empty/missing  -> close VLC (the screen goes blank)
# VLC's remote-control (rc) interface listens on port 4212 for the catalog service
# (play/pause, seek, volume, tracks, DVD menu). It is only reachable inside the compose network.

STATE_FILE="${STATE_FILE:-/state/current}"
MEDIA_PREFIX="${MEDIA_PREFIX:-/media/}"
RC_ADDR="0.0.0.0"
RC_PORT="4212"

# DVD_DEINTERLACE (from .env): VLC's deinterlace mode, or "off". "Automatic" leaves progressive discs alone.
DVD_DEINTERLACE="${DVD_DEINTERLACE:-yadif}"
case "$DVD_DEINTERLACE" in
  off|0|none) DEINTERLACE=(--deinterlace=0) ;;
  discard|blend|mean|bob|linear|x|yadif|yadif2x|phosphor|ivtc)
    DEINTERLACE=(--deinterlace=-1 "--deinterlace-mode=$DVD_DEINTERLACE") ;;
  *) echo "[launcher] unknown DVD_DEINTERLACE '$DVD_DEINTERLACE', using yadif"
     DEINTERLACE=(--deinterlace=-1 --deinterlace-mode=yadif) ;;
esac

# Language preferences (from .env), as two-letter codes (pt, es, fr...), like the settings of a DVD player.
# Many discs have one set of menus and pick their text from the player's menu language, which VLC sets to
# English when none is given. Empty leaves a setting as VLC and the disc choose:
#   DVD_MENU_LANGUAGE      the disc's menus
#   DVD_AUDIO_LANGUAGE     the audio track the disc starts with
#   DVD_SUBTITLE_LANGUAGE  turns subtitles on in that language
LANG_ARGS=()
add_lang() {  # $1 variable name, $2 VLC option
  local value="${!1:-}"
  [ -n "$value" ] || return 0
  if [[ "$value" =~ ^[A-Za-z]{2,3}$ ]]; then
    LANG_ARGS+=("--$2=$value")
  else
    echo "[launcher] ignoring $1 '$value': use a two-letter code such as pt"
  fi
}
add_lang DVD_MENU_LANGUAGE menu-language
add_lang DVD_AUDIO_LANGUAGE audio-language
add_lang DVD_SUBTITLE_LANGUAGE sub-language

# streamiso.lua: a VLC interface for what the rc port cannot do (a Blu-ray's pop-up menu). VLC finds Lua
# interfaces in the user's data folder.
LUA_INTF_DIR="${HOME:-/home/neko}/.local/share/vlc/lua/intf"
mkdir -p "$LUA_INTF_DIR" && cp "$(dirname "$0")/streamiso.lua" "$LUA_INTF_DIR/" \
  || echo "[launcher] could not install streamiso.lua: no Blu-ray pop-up menu"

# The audio and subtitle languages of a Blu-ray's main title, for the catalog's track lists. With menus, VLC
# names every track after the disc's first clip (often a studio logo in English only) and keeps those names
# for the film. Written to $PLAYER_INFO/bluray-tracks.json (the player-info volume, which the catalog reads):
#   {"iso": "<path>", "audio": ["eng", "fra", ...], "subtitles": ["eng", ...]}
PLAYER_INFO="${PLAYER_INFO:-/info}"
bluray_tracks() {
  local iso="$1" link="$2" out="$PLAYER_INFO/bluray-tracks.json"
  command -v bd_list_titles >/dev/null 2>&1 && [ -d "$PLAYER_INFO" ] || return 0
  bd_list_titles -l "$link" 2>/dev/null | awk -v iso="$iso" '
    function list(s,   n, a, i, r) {
      n = split(s, a, " "); r = ""
      for (i = 1; i <= n; i++) if (a[i] ~ /^[a-z][a-z][a-z]?$/) r = r (r == "" ? "" : ",") "\"" a[i] "\""
      return "[" r "]"
    }
    /^Main title:/ { main = $3 }
    /^index:/ { cur = $2 }
    cur != "" && cur == main && /^\tAUD:/ { sub(/^\tAUD: */, ""); aud = $0 }
    cur != "" && cur == main && /^\tPG :/ { sub(/^\tPG : */, ""); pg = $0 }
    END {
      gsub(/\\/, "\\\\", iso); gsub(/"/, "\\\"", iso)
      printf "{\"iso\":\"%s\",\"audio\":%s,\"subtitles\":%s}\n", iso, list(aud), list(pg)
    }' > "$out.tmp" && mv "$out.tmp" "$out" \
    && echo "[launcher] Blu-ray tracks: $(cat "$out")"
}

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

# VLC opens its own context menu on a right click. Besides the web page ignoring that button, turn
# the right and middle buttons off at the X server, so nothing can reach VLC even if a click gets
# through. Whichever tool the image has is used; failure is harmless (the log says what happened).
disable_secondary_buttons() {
  if command -v xmodmap >/dev/null 2>&1; then
    xmodmap -e "pointer = 1 0 0 4 5 6 7 8 9" && { echo "[launcher] right/middle buttons disabled (xmodmap)"; return; }
  fi
  if command -v xinput >/dev/null 2>&1; then
    for id in $(xinput list --id-only 2>/dev/null); do
      xinput set-button-map "$id" 1 0 0 4 5 6 7 2>/dev/null && echo "[launcher] buttons remapped on device $id (xinput)"
    done
    return
  fi
  echo "[launcher] no xmodmap/xinput in this image: right-click only blocked in the web page"
}
disable_secondary_buttons

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
      #   bluray:<iso>   a Blu-ray image, opened with its menus. Most are Java programs (BD-J), which
      #                  the image's Java 11 runs (see neko/Dockerfile); the disc's logos and trailers
      #                  come first, so its menu shows after a minute or more, as on a Blu-ray player
      MODE="dvd"; TARGET="$WANT"; FOURCC=""
      case "$WANT" in
        bluray:*)  MODE="bluray"; TARGET="${WANT#*:}" ;;
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
                "--rc-host=$RC_ADDR:$RC_PORT")
        case "$MODE" in
          dvd)  vlc "${COMMON[@]}" --extraintf=rc "${DEINTERLACE[@]}" "${LANG_ARGS[@]}" --start-paused "dvdnav://$LINK" & ;;
          bluray) bluray_tracks "$TARGET" "$LINK"
                  vlc "${COMMON[@]}" --extraintf=rc:luaintf --lua-intf=streamiso "${DEINTERLACE[@]}" "${LANG_ARGS[@]}" \
                    --start-paused "bluray://$LINK" & ;;
          cd)   vlc "${COMMON[@]}" --extraintf=rc --start-paused --demux=rawaud --rawaud-channels=2 --rawaud-samplerate=44100 \
                    "--rawaud-fourcc=$FOURCC" "$LINK" & ;;
          file) vlc "${COMMON[@]}" --extraintf=rc "$LINK" & ;;
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
