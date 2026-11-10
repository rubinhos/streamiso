// A window over the page. A click on the dark area around it closes it, but not one on its scroll bar,
// which belongs to that same area.
import type { ComponentChildren, Ref } from "preact";

interface Props {
  id: string;
  labelledBy: string;
  open: boolean;
  onClose(): void;
  modalRef?: Ref<HTMLDivElement>;
  children: ComponentChildren;
}

export function Modal({ id, labelledBy, open, onClose, modalRef, children }: Props) {
  return (
    <div
      id={id}
      ref={modalRef}
      class="modal"
      hidden={!open}
      role="dialog"
      aria-modal="true"
      aria-labelledby={labelledBy}
      onMouseDown={(e) => {
        const el = e.currentTarget as HTMLElement;
        if (e.target === el && e.clientX < el.clientWidth && e.clientY < el.clientHeight) onClose();
      }}
    >
      {open && children}
    </div>
  );
}
