/** 75 -> "1:15", 3725 -> "1:02:05". */
export function fmtTime(sec: number | null | undefined): string {
  const s0 = Math.max(0, Math.floor(sec || 0));
  const h = Math.floor(s0 / 3600);
  const m = Math.floor((s0 % 3600) / 60);
  const s = s0 % 60;
  return (h ? h + ":" + (m < 10 ? "0" : "") + m : m) + ":" + (s < 10 ? "0" : "") + s;
}

export function formatSize(bytes: number): string {
  return (bytes / 1073741824).toFixed(1) + " GB";
}

/** Focuses without scrolling, where the browser allows it. */
export function focusQuietly(el: HTMLElement | null | undefined): void {
  if (!el) return;
  try {
    el.focus({ preventScroll: true });
  } catch {
    el.focus();
  }
}
