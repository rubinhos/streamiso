// The library's settings, kept in the browser between visits.
import type { Prefs, SortKey } from "./catalog";

const KEY = "streamisoPrefs";

export function loadPrefs(): Prefs {
  // hideSeen replaced "showSeen", which is ignored, so everyone starts with the switch on.
  const prefs: Prefs = { sort: "title", genre: "", kind: "", hideSeen: true };
  try {
    const p = JSON.parse(localStorage.getItem(KEY) || "{}");
    if (p.sort) prefs.sort = p.sort as SortKey;
    if (typeof p.genre === "string") prefs.genre = p.genre;
    if (typeof p.hideSeen === "boolean") prefs.hideSeen = p.hideSeen;
    if (typeof p.kind === "string") prefs.kind = p.kind;
  } catch {
    /* no localStorage: the defaults */
  }
  return prefs;
}

export function savePrefs(prefs: Prefs): void {
  try {
    localStorage.setItem(KEY, JSON.stringify(prefs));
  } catch {
    /* ignore */
  }
}

// The disc this tab opened, so coming back from the player (a TV's Back button) stops it. Only that one:
// it never cuts off someone watching on another device.
const PLAYING = "streamisoPlaying";

export function markPlaying(): void {
  try {
    sessionStorage.setItem(PLAYING, "1");
  } catch {
    /* ignore */
  }
}

export function stopIfWeStartedIt(): void {
  try {
    if (sessionStorage.getItem(PLAYING) === "1") {
      sessionStorage.removeItem(PLAYING);
      fetch("/api/stop", { method: "POST" });
    }
  } catch {
    /* sessionStorage unavailable: fine */
  }
}
