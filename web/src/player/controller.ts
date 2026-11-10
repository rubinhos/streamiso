// The player page's logic, apart from how it looks: it polls the player's status, sends the commands,
// talks to the Neko page in the iframe (neko/neko-inject.html), and decides when the controls show.
// The view (Player.tsx) draws its state and calls its methods.

import type { PlayerInfo, PlayerStatus, Track, TrackChoice } from "../shared/types";

export interface State {
  title: string;
  kind: "dvd" | "cd";
  artist: string;
  cover: string;
  tracks: Track[];
  frameSrc: string;
  status: PlayerStatus | null;
  volume: number; // last known volume (0-100)
  dragging: boolean;
  dragPct: number;
  /** Keeps the bar steady right after a seek. */
  hold: { time: number; until: number } | null;
  uiOpen: boolean;
  pop: "audio" | "subs" | null;
  needStart: boolean;
  started: boolean;
  resumeAt: number;
  loadingDone: boolean;
  toast: string;
  toastShown: boolean;
  fsSupported: boolean;
  fullscreen: boolean;
  /** CDs: the sound is blocked until the viewer clicks Neko's own play button. */
  unlock: boolean;
}

interface Answer<T> {
  ok: boolean;
  status: number;
  body: T;
}

async function call<T>(path: string, body?: unknown): Promise<Answer<T>> {
  const opts: RequestInit = { method: body === undefined ? "GET" : "POST", headers: {} };
  if (body !== undefined) {
    (opts.headers as Record<string, string>)["Content-Type"] = "application/json";
    opts.body = JSON.stringify(body);
  }
  const r = await fetch(path, opts);
  return { ok: r.ok, status: r.status, body: await r.json() };
}

export class PlayerController {
  s: State = {
    title: "", kind: "dvd", artist: "", cover: "", tracks: [], frameSrc: "", status: null, volume: 100,
    dragging: false, dragPct: 0, hold: null, uiOpen: true, pop: null, needStart: false, started: false, resumeAt: 0,
    loadingDone: false, toast: "", toastShown: false, fsSupported: false, fullscreen: false, unlock: false,
  };

  private listeners: (() => void)[] = [];
  private frame: HTMLIFrameElement | null = null;
  private premute = 100; // volume to restore when un-muting
  private hideTimer = 0;
  private toastTimer = 0;
  private volTimer = 0;
  private pointerOnUi = false;
  private ready = false; // the player answered the status call
  private videoReady = false; // the Neko stream delivered its first frame
  private resumePending = false;
  private resumeBusy = false;
  private resumedCd = false;
  private lastState: string | undefined;
  private polling = false;

  subscribe(fn: () => void): () => void {
    this.listeners.push(fn);
    return () => {
      this.listeners = this.listeners.filter((l) => l !== fn);
    };
  }

  private set(change: Partial<State>): void {
    this.s = { ...this.s, ...change };
    const b = document.body.classList;
    b.toggle("ui-open", this.s.uiOpen);
    b.toggle("started", this.s.started);
    b.toggle("audio", this.s.kind === "cd");
    b.toggle("unlock", this.s.unlock);
    b.toggle("nofs", !this.s.fsSupported);
    for (const l of this.listeners) l();
  }

  setFrame(el: HTMLIFrameElement | null): void {
    this.frame = el;
  }

  // ------------------------------------------------------------------ start-up
  start(): void {
    const keep = () => this.keepUi();
    document.addEventListener("mousemove", keep);
    document.addEventListener("touchstart", keep, { passive: true });
    document.addEventListener("keydown", keep);
    // A tap on the video reaches the iframe, not this page. All this page sees is that it lost the focus.
    window.addEventListener("blur", () => {
      if (this.s.pop) this.closePop();
    });
    window.addEventListener("message", (e) => this.onMessage(e));
    document.addEventListener("keydown", (e) => this.onKey(e));
    document.addEventListener("click", (e) => this.onDocumentClick(e));
    document.addEventListener("fullscreenchange", () => this.renderFs());
    document.addEventListener("webkitfullscreenchange", () => this.renderFs());
    document.addEventListener("visibilitychange", () => this.poll());

    // Where the page cannot go fullscreen (an Android WebView, iPhones), the button is left out.
    const root = document.documentElement as any;
    const enabled = document.fullscreenEnabled !== undefined ? document.fullscreenEnabled : (document as any).webkitFullscreenEnabled;
    this.set({ fsSupported: !!(root.requestFullscreen || root.webkitRequestFullscreen) && enabled !== false });
    this.renderFs();

    // The loading cover stays up until the player answers AND the stream has started, which also hides
    // Neko's own "connecting" screen. After a while it is lifted anyway, so a connection problem shows
    // Neko's error instead of an endless spinner.
    setTimeout(() => {
      this.videoReady = true;
      this.revealIfReady();
    }, 25000);

    call<PlayerInfo>("/api/player").then((r) => {
      const d = r.body;
      this.resumePending = !!d.resume;
      const change: Partial<State> = { resumeAt: d.resume_at || 0, frameSrc: d.url };
      if (d.kind === "cd") {
        Object.assign(change, { kind: "cd", tracks: d.tracks || [], artist: d.artist || "", cover: d.cover || "" });
      }
      if (d.title) {
        change.title = d.title;
        document.title = d.title + " – StreamISO";
      }
      this.set(change);
      if (d.kind === "cd") this.showUi(); // a CD's bar never collapses (the status may have come before the kind)
    });
    this.poll();
    setInterval(() => this.poll(), 1000);
  }

  // ------------------------------------------------------------------ show / hide the controls
  // Once a DVD plays, the bar stays collapsed: many discs show their own menu, and the bar would cover its
  // buttons. Only the handle at the bottom (or a playback shortcut) opens it. Moving the mouse or touching
  // the video only keeps an open bar from collapsing; it never brings the bar back over the disc's menu.
  private scheduleHide(): void {
    clearTimeout(this.hideTimer);
    const paused = this.s.status && this.s.status.state === "paused";
    if (!paused && !this.pointerOnUi && !this.s.pop && !this.s.dragging) {
      this.hideTimer = window.setTimeout(() => this.hideUi(), 5000);
    }
  }

  showUi(): void {
    this.set({ uiOpen: true });
    this.scheduleHide();
  }

  keepUi(): void {
    if (this.s.uiOpen) this.scheduleHide();
  }

  private hideUi(): void {
    if (this.s.kind === "cd") return; // nothing to look at behind the bar
    if (this.pointerOnUi || this.s.pop || this.s.dragging) return;
    if (this.s.status && this.s.status.state === "paused") return;
    this.set({ uiOpen: false });
  }

  /** The viewer asked for it, so this one also applies to a paused disc. The handle brings it back. */
  hideUiNow(): void {
    if (this.s.kind === "cd") return;
    this.closePop();
    clearTimeout(this.hideTimer);
    this.set({ uiOpen: false });
  }

  /** A real mouse over a bar (a tap also fires pointerenter, but the pointerleave never comes). */
  pointerOverUi(over: boolean, pointerType: string): void {
    if (pointerType !== "mouse") return;
    this.pointerOnUi = over;
    this.keepUi();
  }

  handle(): void {
    if (this.s.uiOpen) {
      this.hideUiNow();
      this.refocusVideo();
    } else this.showUi();
  }

  // ------------------------------------------------------------------ commands
  showToast(text: string): void {
    this.set({ toast: text, toastShown: true });
    clearTimeout(this.toastTimer);
    this.toastTimer = window.setTimeout(() => this.set({ toastShown: false }), 1600);
  }

  cmd(action: string, value?: unknown): Promise<unknown> {
    const body: Record<string, unknown> = { action };
    if (value !== undefined) body.value = value;
    return call<{ error?: string }>("/api/player/cmd", body).then((r) => {
      if (!r.ok) this.showToast(r.body && r.body.error ? r.body.error : "Command failed");
      setTimeout(() => this.poll(), 250);
      return r;
    }, () => this.showToast("Connection lost"));
  }

  seekTo(sec: number): void {
    const st = this.s.status;
    if (!st || !st.length) return;
    const to = Math.max(0, Math.min(st.length - 1, Math.round(sec)));
    this.set({ hold: { time: to, until: Date.now() + 1800 } });
    this.cmd("seek", to);
  }

  skip(delta: number): void {
    const st = this.s.status;
    if (!st || !st.length) return;
    const cur = this.s.hold && Date.now() < this.s.hold.until ? this.s.hold.time : st.time || 0;
    this.seekTo(cur + delta);
  }

  togglePlay(): void {
    if (this.s.needStart) {
      this.startDisc();
      return;
    }
    const st = this.s.status;
    if (st) this.applyStatus({ ...st, state: st.state === "paused" ? "playing" : "paused" });
    this.cmd("toggle");
  }

  setVolume(v: number, silent?: boolean): void {
    const vol = Math.max(0, Math.min(100, Math.round(v)));
    this.set({ volume: vol });
    if (!silent) this.showToast("Volume " + vol + "%");
    clearTimeout(this.volTimer);
    this.volTimer = window.setTimeout(() => this.cmd("volume", this.s.volume), 120);
  }

  toggleMute(): void {
    if (this.s.volume > 0) {
      this.premute = this.s.volume;
      this.setVolume(0, true);
      this.showToast("Muted");
    } else {
      this.setVolume(this.premute || 100, true);
      this.showToast("Volume " + this.s.volume + "%");
    }
  }

  menu(): void {
    this.cmd("menu");
    this.refocusVideo();
  }

  returnToMenu(): void {
    this.cmd("return");
    this.refocusVideo();
  }

  playTrack(i: number): void {
    this.cmd("track", i);
  }

  pickAudio(t: TrackChoice): void {
    this.cmd("audio", t.id);
    this.showToast("Audio: " + t.name);
    this.closePop();
  }

  pickSubtitle(t: TrackChoice): void {
    this.cmd("subtitle", t.id);
    this.showToast(t.id === -1 ? "Subtitles off" : "Subtitles: " + t.name);
    this.closePop();
  }

  closePop(): void {
    if (this.s.pop) this.set({ pop: null });
    this.keepUi();
  }

  togglePop(which: "audio" | "subs"): void {
    const open = this.s.pop === which;
    this.closePop();
    if (!open) {
      this.set({ pop: which });
      clearTimeout(this.hideTimer);
    }
  }

  private onDocumentClick(e: MouseEvent): void {
    if (!this.s.pop) return;
    const t = e.target as HTMLElement;
    if (t.closest && (t.closest(".pop") || t.closest("#audio") || t.closest("#subs"))) return;
    this.closePop();
  }

  // ------------------------------------------------------------------ status
  private applyStatus(st: PlayerStatus): void {
    const playing = st.state === "playing";
    const change: Partial<State> = { status: st };
    if (this.s.hold && Date.now() >= this.s.hold.until) change.hold = null;
    this.set(change);
    if (playing && this.s.needStart) this.discStarted();
    // A paused disc keeps an open bar up: start counting once it plays again.
    if (st.state !== this.lastState) {
      this.lastState = st.state;
      if (playing) this.keepUi();
    }
  }

  poll(): void {
    if (this.polling || document.hidden) return;
    this.polling = true;
    call<PlayerStatus>("/api/player/status").then((r) => {
      this.polling = false;
      const st = r.body;
      if (st.state === "idle") {
        location.replace("/");
        return;
      }
      if (st.state === "offline") return;
      if (!this.ready) {
        this.ready = true;
        if (this.resumePending) {
          // jump to the saved position while the disc is still paused, then reveal
          this.resumePending = false;
          this.resumeBusy = true;
          this.cmd("resume_apply").then(() => {
            this.resumeBusy = false;
            this.revealIfReady();
          }, () => {
            this.resumeBusy = false;
            this.revealIfReady();
          });
        }
        // Already running (page reopened): no start screen, and the bar starts collapsed.
        // Otherwise the bar shows with the start screen, which covers the video anyway.
        this.set({ status: st });
        if (st.state === "playing") this.discStarted();
        else this.showUi();
        this.revealIfReady();
        // The player may report 0 before its audio output exists, so only trust a real value
        if (st.volume) this.set({ volume: st.volume });
      }
      this.applyStatus(st);
    }, () => {
      this.polling = false;
    });
  }

  // ------------------------------------------------------------------ starting
  // Audio CDs open paused, so their first seconds are not played before the browser's audio is connected.
  // Called when the bridge reports audible sound, or as a fallback if it never does.
  private resumeCd(): void {
    if (this.s.kind !== "cd" || this.resumedCd) return;
    this.resumedCd = true;
    setTimeout(() => this.cmd("start"), 250);
  }

  // DVDs open paused and wait for a click on the big button. The click is a real user gesture, so the
  // browser allows sound, and the video gets the keyboard focus (and, through the bridge, control).
  private showStart(): void {
    if (this.s.kind !== "dvd" || this.s.needStart || this.s.started) return;
    this.set({ needStart: true });
  }

  /** The disc is running: the start screen goes, and so does the bar, leaving the disc's menu uncovered. */
  private discStarted(): void {
    this.set({ needStart: false, started: true });
    this.hideUiNow();
  }

  startDisc(): void {
    if (!this.s.needStart) return;
    this.discStarted();
    try {
      this.frame?.focus();
      this.frame?.contentWindow?.postMessage({ streamiso: true, type: "start" }, "*");
    } catch {
      /* ignore */
    }
    this.cmd("start");
  }

  private revealIfReady(): void {
    if (this.ready && this.videoReady && !this.resumeBusy) {
      this.set({ loadingDone: true });
      this.showStart();
      this.refocusVideo(); // keys and the remote go straight to the disc menu
      if (this.s.kind === "cd") setTimeout(() => this.resumeCd(), 8000); // the bridge never reported sound
    }
  }

  refocusVideo(): void {
    try {
      this.frame?.focus();
    } catch {
      /* ignore */
    }
  }

  // ------------------------------------------------------------------ seek bar
  dragStart(pct: number): void {
    if (!this.s.status || !this.s.status.length) return;
    this.set({ dragging: true, dragPct: pct });
  }

  dragMove(pct: number): void {
    if (this.s.dragging) this.set({ dragPct: pct });
  }

  dragEnd(commit: boolean): void {
    if (!this.s.dragging) return;
    this.set({ dragging: false });
    if (commit && this.s.status && this.s.status.length) this.seekTo((this.s.dragPct / 100) * this.s.status.length);
    this.showUi();
  }

  // ------------------------------------------------------------------ fullscreen
  // The whole page goes fullscreen, so the bar stays on top of the video.
  private inFullscreen(): boolean {
    return !!(document.fullscreenElement || (document as any).webkitFullscreenElement);
  }

  private renderFs(): void {
    this.set({ fullscreen: this.inFullscreen() });
  }

  toggleFullscreen(): void {
    if (!this.s.fsSupported) return;
    const root = document.documentElement as any;
    const doc = document as any;
    try {
      if (this.inFullscreen()) (doc.exitFullscreen || doc.webkitExitFullscreen).call(doc);
      else {
        const request = (root.requestFullscreen || root.webkitRequestFullscreen).call(root);
        if (request && request.catch) request.catch(() => this.set({ fsSupported: false }));
      }
    } catch {
      /* ignore */
    }
  }

  // ------------------------------------------------------------------ leaving
  /** Back: stops the disc and returns to the library. */
  goBack(): void {
    try {
      sessionStorage.removeItem("streamisoPlaying");
    } catch {
      /* ignore */
    }
    const go = () => location.replace("/");
    fetch("/api/stop", { method: "POST" }).then(go, go);
  }

  // ------------------------------------------------------------------ keys
  // Playback keys open the bar, which shows what they did. Menu navigation keys leave it collapsed.
  private action(name: string): void {
    switch (name) {
      case "toggle": this.showUi(); this.togglePlay(); break;
      case "forward": this.showUi(); this.skip(10); break;
      case "rewind": this.showUi(); this.skip(-10); break;
      case "nav-up": case "nav-down": case "nav-left": case "nav-right": case "nav-activate":
        if (this.s.kind === "dvd") this.cmd("nav", name.slice(4));
        break;
      case "next": this.showUi(); this.cmd("chapter_next"); break;
      case "prev": this.showUi(); this.cmd("chapter_prev"); break;
      case "stop":
      case "back": this.goBack(); break;
    }
  }

  // Keys pressed while the video (Neko) has the focus are reported by the bridge in the iframe.
  private onMessage(e: MessageEvent): void {
    if (!this.frame || e.source !== this.frame.contentWindow) return;
    const d = e.data;
    if (!d || d.streamiso !== true) return;
    if (d.type === "video-ready") {
      this.videoReady = true;
      this.revealIfReady();
    } else if (d.type === "audio-state" && this.s.kind === "cd") {
      // Sound is blocked until the viewer clicks Neko's own play/unmute button: show it.
      this.set({ unlock: !d.ok });
      // The CD opens paused; start it once sound can really be heard.
      if (d.ok) this.resumeCd();
    } else if (d.type === "activity") this.keepUi();
    else if (d.type === "key" && typeof d.action === "string") {
      this.keepUi();
      this.action(d.action);
    }
  }

  // Shortcuts when the page itself has the focus (outside the iframe).
  private onKey(e: KeyboardEvent): void {
    const t = e.target as HTMLElement | null;
    const tag = t && t.tagName;
    if (tag === "INPUT" || tag === "BUTTON" || (t && t.id === "seek")) {
      if (e.key === "Escape") this.closePop();
      return;
    }
    switch (e.key) {
      case " ": case "k": this.showUi(); this.togglePlay(); break;
      case "ArrowLeft": this.showUi(); this.skip(-10); break;
      case "ArrowRight": this.showUi(); this.skip(10); break;
      case "ArrowUp": this.setVolume(this.s.volume + 5); break;
      case "ArrowDown": this.setVolume(this.s.volume - 5); break;
      case "m": this.toggleMute(); break;
      case "f": this.toggleFullscreen(); break;
      case "Escape": this.closePop(); break;
      default: return;
    }
    e.preventDefault();
  }
}
