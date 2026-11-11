// The cards: one disc, or a group of discs (a season, a film on several discs).
import type { Disc } from "../shared/types";
import { formatSize } from "../shared/format";
import type { Group } from "./catalog";
import { cap, discTags, groupHeading, resumeText, seasonOf, seenWord, subText } from "./catalog";

export interface DiscActions {
  play(d: Disc, resume: boolean): void;
  toggleWatched(d: Disc): void;
  edit(d: Disc): void;
  forgetResume(d: Disc): void;
}

function Cover({ id, v, fill, children }: { id: string; v: number; fill: boolean; children?: any }) {
  const url = "/api/cover/" + id + "?v=" + v;
  return (
    <span class={"cover" + (fill ? " fill" : "")}>
      <img class="bg" alt="" loading="lazy" src={url} />
      <img class="fg" alt="" loading="lazy" src={url} />
      {children}
    </span>
  );
}

function stop(fn: () => void) {
  return (e: Event) => {
    e.stopPropagation();
    fn();
  };
}

/** inProgress: the card is in the "Continue watching" section, which gives it a button to leave it. */
export function DiscItem({ d, aliases, actions, inProgress }: { d: Disc; aliases: Record<string, string>; actions: DiscActions; inProgress?: boolean }) {
  const pct = d.resume && d.resume.pct !== null && d.resume.pct !== undefined ? d.resume.pct : null;
  return (
    <div class={"item" + (d.watched ? " watched" : "") + (d.kind === "cd" ? " cd" : "")} data-id={d.id} role="listitem">
      <button
        type="button"
        class="card"
        title={d.title + (d.synopsis ? "\n\n" + d.synopsis.slice(0, 300) : "") + "\n\n" + formatSize(d.size) + " · E: edit · W: " + seenWord(d)}
        onClick={() => actions.play(d, !!d.resume)}
      >
        <Cover id={d.id} v={d.cover_v} fill={d.cover_fill}>
          {pct !== null && (
            <span class="progress"><span style={{ width: Math.max(3, Math.min(100, pct)) + "%" }} /></span>
          )}
          <span class="kindtag">{d.kind === "cd" ? "🎵 CD" : d.format === "bluray" ? "💿 Blu-ray" : "🎞️ DVD"}</span>
        </Cover>
        <span class="title">{d.title}</span>
        <span class="sub">{subText(d) || formatSize(d.size)}</span>
        {d.genres.length > 0 && <span class="genres">{discTags(aliases, d).slice(0, 3).join(" · ")}</span>}
        {d.resume && <span class="resume">{"▶ " + resumeText(d)}</span>}
      </button>
      <span class="ribbon">{"✓ " + cap(seenWord(d))}</span>
      <div class="tools">
        {d.resume && (
          <button type="button" class="tool" title="Start over" onClick={stop(() => actions.play(d, false))}>{"↺"}</button>
        )}
        {inProgress && (
          <button
            type="button"
            class="tool"
            title={"Remove from Continue " + (d.kind === "cd" ? "listening" : "watching") + " (starts over next time)"}
            onClick={stop(() => actions.forgetResume(d))}
          >{"✕"}</button>
        )}
        <button
          type="button"
          class="tool"
          title={d.watched ? "Mark as not " + seenWord(d) : "Mark as " + seenWord(d)}
          aria-pressed={d.watched ? "true" : "false"}
          onClick={stop(() => actions.toggleWatched(d))}
        >✓</button>
        <button type="button" class="tool" title="Edit details" onClick={stop(() => actions.edit(d))}>✎</button>
      </div>
    </div>
  );
}

export function GroupItem({ g, aliases, open }: { g: Group; aliases: Record<string, string>; open(key: string): void }) {
  const first = g.discs[0];
  const watched = g.discs.every((d) => d.watched);
  const resumed = g.discs.find((d) => d.resume) || null;
  const seen = g.discs.filter((d) => d.watched).length;
  const rs = resumed ? seasonOf(resumed) : null;
  return (
    <div class={"item stack" + (watched ? " watched" : "")} data-group={g.key} role="listitem">
      <button
        type="button"
        class="card"
        title={groupHeading(g) + " · " + g.discs.length + " discs" + (first.synopsis ? "\n\n" + first.synopsis.slice(0, 300) : "")}
        onClick={() => open(g.key)}
      >
        <Cover id={first.id} v={first.cover_v} fill={first.cover_fill}>
          <span class="kindtag">{"📀 " + g.discs.length + " discs"}</span>
        </Cover>
        <span class="title">{g.name}</span>
        <span class="sub">
          {(first.year ? first.year + " · " : "") +
            (g.season === null ? g.discs.length + " discs" : "Season " + g.season) +
            (seen && !watched ? " · " + seen + "/" + g.discs.length + " watched" : "")}
        </span>
        {first.genres.length > 0 && <span class="genres">{discTags(aliases, first).slice(0, 3).join(" · ")}</span>}
        {resumed && (
          <span class="resume">{"▶ Continue" + (rs && rs.disc !== null && rs.disc !== undefined ? " disc " + rs.disc : "")}</span>
        )}
      </button>
      <span class="ribbon">{"✓ Watched"}</span>
    </div>
  );
}
