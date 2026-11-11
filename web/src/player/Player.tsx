// The player page as it looks: the stream (Neko's page in an iframe), the CD view, the loading cover, the
// start button and the controls. The logic is in PlayerController.
import { useEffect, useRef, useState } from "preact/hooks";
import type { TrackChoice } from "../shared/types";
import { fmtTime } from "../shared/format";
import type { PlayerController } from "./controller";

const ICON = {
  play: <svg viewBox="0 0 24 24"><path d="M8 5v14l11-7z" /></svg>,
  pause: <svg viewBox="0 0 24 24"><path d="M6 5h4v14H6zm8 0h4v14h-4z" /></svg>,
  vol: <svg viewBox="0 0 24 24"><path d="M3 9v6h4l5 5V4L7 9zm13.5 3A4.5 4.5 0 0 0 14 8v8a4.5 4.5 0 0 0 2.5-4z" /></svg>,
  volHigh: <svg viewBox="0 0 24 24"><path d="M3 9v6h4l5 5V4L7 9zm13.5 3A4.5 4.5 0 0 0 14 8v8a4.5 4.5 0 0 0 2.5-4zM14 3.2v2.1a7 7 0 0 1 0 13.4v2.1a9 9 0 0 0 0-17.6z" /></svg>,
  mute: <svg viewBox="0 0 24 24"><path d="M16.5 12A4.5 4.5 0 0 0 14 8v2.2l2.4 2.4c.1-.2.1-.4.1-.6zM19 12c0 .9-.2 1.8-.5 2.6l1.5 1.5A9 9 0 0 0 21 12a9 9 0 0 0-7-8.8v2.1c2.9.9 5 3.5 5 6.7zM4.3 3L3 4.3 7.7 9H3v6h4l5 5v-6.7l4.3 4.3c-.7.5-1.4.9-2.3 1.2v2.1a9 9 0 0 0 3.7-1.8l2 2L21 19.7l-9-9zM12 4L9.9 6.1 12 8.2z" /></svg>,
  fs: <svg viewBox="0 0 24 24"><path d="M7 14H5v5h5v-2H7zm-2-4h2V7h3V5H5zm12 7h-3v2h5v-5h-2zM14 5v2h3v3h2V5z" /></svg>,
  fsExit: <svg viewBox="0 0 24 24"><path d="M5 16h3v3h2v-5H5zm3-8H5v2h5V5H8zm6 11h2v-3h3v-2h-5zm2-11V5h-2v5h5V8z" /></svg>,
};

function Menu({ heading, list, onPick }: { heading: string; list: TrackChoice[]; onPick(t: TrackChoice): void }) {
  return (
    <>
      <h4>{heading}</h4>
      {list.length === 0 && <div class="empty">Not available</div>}
      {list.map((t) => (
        <button key={t.id} type="button" class={"item" + (t.selected ? " sel" : "")} onClick={() => onPick(t)}>
          {t.id === -1 ? "Off" : t.name}
        </button>
      ))}
    </>
  );
}

export function Player({ ctl }: { ctl: PlayerController }) {
  const [, redraw] = useState(0);
  useEffect(() => {
    const unsubscribe = ctl.subscribe(() => redraw((n) => n + 1));
    redraw((n) => n + 1); // what changed before this view listened
    return unsubscribe;
  }, []);
  const s = ctl.s;
  const st = s.status || ({ state: "" } as NonNullable<typeof s.status>);
  const seekRef = useRef<HTMLDivElement>(null);
  const startRef = useRef<HTMLButtonElement>(null);
  const tracksRef = useRef<HTMLOListElement>(null);
  const [tip, setTip] = useState<{ pct: number; text: string } | null>(null);

  const playing = st.state === "playing";
  const length = st.length || 0;
  const hasTime = length > 0;
  let now = st.time || 0;
  if (s.hold && Date.now() < s.hold.until) now = s.hold.time;
  const pct = hasTime ? Math.min(100, s.dragging ? s.dragPct : (now / length) * 100) : 0;
  if (s.dragging && hasTime) now = (s.dragPct / 100) * length;
  const cd = s.kind === "cd";
  const currentTrack = typeof st.track === "number" ? st.track : -1;

  useEffect(() => {
    if (s.needStart) {
      try {
        startRef.current?.focus();
      } catch {
        /* ignore */
      }
    }
  }, [s.needStart]);

  // The playing track: in view, and in the window's title.
  useEffect(() => {
    if (!cd || currentTrack < 0) return;
    const li = tracksRef.current?.children[currentTrack] as HTMLElement | undefined;
    if (li) li.scrollIntoView({ block: "nearest" });
    const t = s.tracks[currentTrack];
    if (t) document.title = t.title + " – " + s.title + " – StreamISO";
  }, [cd, currentTrack]);

  function pctFrom(e: PointerEvent): number {
    const rect = seekRef.current!.getBoundingClientRect();
    return Math.max(0, Math.min(100, ((e.clientX - rect.left) / rect.width) * 100));
  }
  function showTip(e: PointerEvent) {
    if (!hasTime) return;
    const p = pctFrom(e);
    setTip({ pct: p, text: fmtTime((p / 100) * length) });
  }

  const handleLabel = s.uiOpen ? "Hide controls" : "Show controls";
  const fsLabel = s.fullscreen ? "Exit fullscreen" : "Fullscreen";
  const over = (o: boolean) => (e: PointerEvent) => ctl.pointerOverUi(o, e.pointerType);

  return (
    <>
      <iframe
        id="player"
        ref={(el) => ctl.setFrame(el)}
        class={cd && s.unlock ? "front" : ""}
        title="DVD player"
        allow="autoplay; fullscreen"
        allowFullScreen
        src={s.frameSrc || undefined}
        onLoad={() => ctl.refocusVideo()}
      />

      <div id="audioview" hidden={!cd}>
        <div class="av-bg" id="av-bg" style={s.cover ? { backgroundImage: "url(" + JSON.stringify(s.cover) + ")" } : undefined} />
        <div id="av-hint"><span>Click the play button in the middle of the screen to enable sound</span></div>
        <div class="av-main">
          <div class="av-cover"><img id="av-cover" alt="" src={s.cover || undefined} /></div>
          <div class="av-side">
            <div class="av-album" id="av-album">{s.title}</div>
            <div class="av-artist" id="av-artist">{s.artist}</div>
            <ol id="av-tracks" ref={tracksRef}>
              {s.tracks.map((t, i) => (
                <li
                  key={i}
                  tabIndex={0}
                  class={i === currentTrack ? "cur" : ""}
                  onClick={() => ctl.playTrack(i)}
                  onKeyDown={(e) => {
                    if (e.key === "Enter" || e.key === " ") {
                      e.preventDefault();
                      e.stopPropagation();
                      ctl.playTrack(i);
                    }
                  }}
                >
                  <span class="n">{String(t.n)}</span>
                  <span class="t">{t.title}</span>
                  <span class="d">{t.length ? fmtTime(t.length) : ""}</span>
                </li>
              ))}
            </ol>
          </div>
        </div>
      </div>

      <div id="loading" class={s.loadingDone && !s.bdOpening ? "done" : ""}>
        <div class="spinner" />
        <p id="loading-title">{s.title}</p>
        {s.bdOpening ? (
          <>
            <p>Opening the Blu-ray…</p>
            <p class="hint">Its menu shows after the studio logos, in about a minute.</p>
          </>
        ) : (
          <p>Loading…</p>
        )}
      </div>

      <button id="start" ref={startRef} type="button" aria-label="Play" class={s.needStart ? "show" : ""} onClick={() => ctl.startDisc()}>
        <span class="disc"><svg viewBox="0 0 24 24"><path d="M8 5v14l11-7z" /></svg></span>
        <span class="name" id="start-name">{s.title}</span>
        <span class="sub" id="start-sub">{s.resumeAt ? "Continue from " + fmtTime(s.resumeAt) : ""}</span>
      </button>

      <div id="ui" class={s.uiOpen ? "" : "hidden"}>
        <div class="bar top" onPointerEnter={over(true)} onPointerLeave={over(false)}>
          <button id="back" type="button" title="Back to library" aria-label="Back to library" onClick={() => ctl.goBack()}>
            <svg viewBox="0 0 24 24"><path d="M20 11H7.8l5.6-5.6L12 4l-8 8 8 8 1.4-1.4L7.8 13H20z" /></svg>
          </button>
          <div id="title">{s.title}</div>
        </div>

        <div class="bar bottom" onPointerEnter={over(true)} onPointerLeave={over(false)}>
          <div class="seekrow">
            <div class="time" id="t-now">{hasTime ? fmtTime(now) : ""}</div>
            <div
              id="seek"
              ref={seekRef}
              role="slider"
              tabIndex={0}
              aria-label="Seek"
              aria-valuemin={0}
              aria-valuemax={length}
              aria-valuenow={Math.floor(now)}
              class={(hasTime ? "" : "off") + (s.dragging ? " drag" : "")}
              onPointerDown={(e) => {
                if (!hasTime) return;
                try {
                  seekRef.current!.setPointerCapture(e.pointerId);
                } catch {
                  /* ignore */
                }
                showTip(e);
                ctl.dragStart(pctFrom(e));
              }}
              onPointerMove={(e) => {
                showTip(e);
                ctl.dragMove(pctFrom(e));
              }}
              onPointerUp={() => ctl.dragEnd(true)}
              onPointerCancel={() => ctl.dragEnd(false)}
              onKeyDown={(e) => {
                if (e.key === "ArrowLeft") { ctl.skip(-10); e.preventDefault(); e.stopPropagation(); }
                else if (e.key === "ArrowRight") { ctl.skip(10); e.preventDefault(); e.stopPropagation(); }
              }}
            >
              <div class="track"><div class="fill" id="seek-fill" style={{ width: pct + "%" }} /></div>
              <div class="thumb" id="seek-thumb" style={{ left: pct + "%" }} />
              <div class="tip" id="seek-tip" style={{ left: (tip ? tip.pct : 0) + "%" }}>{tip ? tip.text : "0:00"}</div>
            </div>
            <div class="time right" id="t-left">{hasTime ? "-" + fmtTime(length - now) : ""}</div>
          </div>

          <div class="row" id="controls">
            <button id="toggle" class="big" type="button" title="Play / Pause (Space)" aria-label={playing ? "Pause" : "Play"} onClick={() => ctl.togglePlay()}>
              {playing ? ICON.pause : ICON.play}
            </button>
            <button id="prev" type="button" title={cd ? "Previous track" : "Previous chapter"} aria-label={cd ? "Previous track" : "Previous chapter"} onClick={() => ctl.cmd("chapter_prev")}>
              <svg viewBox="0 0 24 24"><path d="M6 6h2v12H6zM9.5 12l8.5 6V6z" /></svg>
            </button>
            <button id="rew" type="button" title="Back 10 seconds" aria-label="Back 10 seconds" disabled={!hasTime} onClick={() => ctl.skip(-10)}>
              <svg viewBox="0 0 24 24"><path d="M12 5V1L7 6l5 5V7a6 6 0 1 1-6 6H4a8 8 0 1 0 8-8z" /><text x="12" y="16.2" text-anchor="middle" font-size="7.5" font-weight="700" font-family="system-ui,sans-serif">10</text></svg>
            </button>
            <button id="fwd" type="button" title="Forward 10 seconds" aria-label="Forward 10 seconds" disabled={!hasTime} onClick={() => ctl.skip(10)}>
              <svg viewBox="0 0 24 24"><path d="M12 5V1l5 5-5 5V7a6 6 0 1 0 6 6h2a8 8 0 1 1-8-8z" /><text x="12" y="16.2" text-anchor="middle" font-size="7.5" font-weight="700" font-family="system-ui,sans-serif">10</text></svg>
            </button>
            <button id="next" type="button" title={cd ? "Next track" : "Next chapter"} aria-label={cd ? "Next track" : "Next chapter"} onClick={() => ctl.cmd("chapter_next")}>
              <svg viewBox="0 0 24 24"><path d="M16 6h2v12h-2zM6 18l8.5-6L6 6z" /></svg>
            </button>
            <div class="vol">
              <button id="mute" type="button" title="Mute (M)" aria-label={s.volume === 0 ? "Unmute" : "Mute"} onClick={() => ctl.toggleMute()}>
                {s.volume === 0 ? ICON.mute : s.volume < 50 ? ICON.vol : ICON.volHigh}
              </button>
              <input id="volume" type="range" min="0" max="100" step="1" value={s.volume} aria-label="Volume"
                onInput={(e) => ctl.setVolume(parseInt((e.target as HTMLInputElement).value, 10), true)} />
            </div>

            <div class="spacer" />

            <button id="dvdreturn" class="label hide-sm" type="button"
              title="Back to the menu of what is playing (the disc decides which: often the one it was started from)" onClick={() => ctl.returnToMenu()}>
              <svg viewBox="0 0 24 24"><path d="M10 9V5l-7 7 7 7v-4.1c5 0 8.5 1.6 11 5.1-1-5-4-10-11-11z" /></svg>Return
            </button>
            <button id="dvdmenu" class="label hide-sm" type="button"
              title={s.bluray ? "Open the disc's pop-up menu (it usually has a way back to the main menu)" : "Go to the DVD's main menu"}
              onClick={() => ctl.menu()}>
              <svg viewBox="0 0 24 24"><path d="M3 6h18v2H3zm0 5h18v2H3zm0 5h18v2H3z" /></svg>Disc menu
            </button>
            <button id="audio" type="button" title="Audio" aria-label="Audio track" disabled={!(st.audio && st.audio.length)} onClick={() => ctl.togglePop("audio")}>
              <svg viewBox="0 0 24 24"><path d="M3 9v6h4l5 5V4L7 9zm13.5 3A4.5 4.5 0 0 0 14 8v8a4.5 4.5 0 0 0 2.5-4zM14 3.2v2.1a7 7 0 0 1 0 13.4v2.1a9 9 0 0 0 0-17.6z" /></svg>
            </button>
            <button id="subs" type="button" title="Subtitles" aria-label="Subtitles" disabled={!(st.subtitles && st.subtitles.length > 1)} onClick={() => ctl.togglePop("subs")}>
              <svg viewBox="0 0 24 24"><path d="M20 4H4a2 2 0 0 0-2 2v12a2 2 0 0 0 2 2h16a2 2 0 0 0 2-2V6a2 2 0 0 0-2-2zM4 12h4v2H4zm10 6H4v-2h10zm6 0h-4v-2h4zm0-4h-10v-2h10z" /></svg>
            </button>
            <button id="full" type="button" title={fsLabel + " (F)"} aria-label={fsLabel} onClick={() => { ctl.toggleFullscreen(); ctl.refocusVideo(); }}>
              {s.fullscreen ? ICON.fsExit : ICON.fs}
            </button>

            <div class={"pop" + (s.pop === "audio" ? " open" : "")} id="pop-audio" style={{ right: "92px" }}>
              <Menu heading="Audio" list={st.audio || []} onPick={(t) => ctl.pickAudio(t)} />
            </div>
            <div class={"pop" + (s.pop === "subs" ? " open" : "")} id="pop-subs" style={{ right: "46px" }}>
              <Menu heading="Subtitles" list={st.subtitles || []} onPick={(t) => ctl.pickSubtitle(t)} />
            </div>
          </div>
        </div>
      </div>

      <button id="handle" type="button" title={handleLabel} aria-label={handleLabel} aria-expanded={s.uiOpen ? "true" : "false"} aria-controls="ui" onClick={() => ctl.handle()}>
        <svg viewBox="0 0 24 24"><path d="M7.4 15.4 12 10.8l4.6 4.6L18 14l-6-6-6 6z" /></svg>
      </button>

      <div id="toast" role="status" class={s.toastShown ? "show" : ""}>{s.toast}</div>
    </>
  );
}
