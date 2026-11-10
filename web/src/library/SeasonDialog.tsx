// The discs of one season (or one film on several discs), opened from its card.
import type { Ref } from "preact";
import type { Disc } from "../shared/types";
import type { DiscActions } from "./Cards";
import { DiscItem } from "./Cards";
import { groupHeading, seasonOf } from "./catalog";
import { Modal } from "./Modal";

interface Props {
  discs: Disc[]; // the season's discs that pass the filters, in disc order
  aliases: Record<string, string>;
  actions: DiscActions;
  cardsRef: Ref<HTMLDivElement>;
  closeRef: Ref<HTMLButtonElement>;
  modalRef: Ref<HTMLDivElement>;
  onClose(): void;
}

export function SeasonDialog({ discs, aliases, actions, cardsRef, closeRef, modalRef, onClose }: Props) {
  const s = discs.length ? seasonOf(discs[0]) : null;
  const seen = discs.filter((d) => d.watched).length;
  return (
    <Modal id="season" labelledBy="se-heading" open={discs.length > 0} onClose={onClose} modalRef={modalRef}>
      <div class="panel season-panel">
        <div class="season-head">
          <div>
            <h2 id="se-heading">{s ? groupHeading(s) : ""}</h2>
            <p id="se-info" class="muted small">
              {discs.length + (discs.length === 1 ? " disc" : " discs") + (seen ? " · " + seen + " watched" : "")}
            </p>
          </div>
          <div class="spacer" />
          <button type="button" id="se-close" ref={closeRef} title="Close" aria-label="Close" onClick={onClose}>&#x2715;</button>
        </div>
        <div id="se-cards" ref={cardsRef} class="cards" role="list">
          {discs.map((d) => <DiscItem key={d.id} d={d} aliases={aliases} actions={actions} />)}
        </div>
      </div>
    </Modal>
  );
}
