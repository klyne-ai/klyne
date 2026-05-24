// Portal — Svelte action that moves a host element to document.body
// on mount and removes it on destroy. Use this for any element that
// relies on position:fixed but is rendered inside a tree whose
// ancestors may create a containing block (transform / filter /
// backdrop-filter / contain / will-change / running animation with
// fill-mode that lingers a transform at rest).
//
// Why this is needed: the dashboard `.page` element has an
// `animation: fadeUp … both;` rule whose end-frame leaves
// `transform: matrix(1, 0, 0, 1, 0, 0)` applied. Any descendant
// using `position: fixed` then anchors to `.page` rather than the
// viewport, so when the user has scrolled inside `.page` the fixed
// element appears above the visible area.
//
// Usage:
//   <div use:portal>...modal markup...</div>
export function portal(node: HTMLElement, target: HTMLElement | string = document.body) {
  const resolve = (t: HTMLElement | string): HTMLElement => {
    if (typeof t === 'string') {
      const el = document.querySelector(t);
      if (!(el instanceof HTMLElement)) throw new Error(`portal: target ${t} not found`);
      return el;
    }
    return t;
  };

  let current = resolve(target);
  current.appendChild(node);

  return {
    update(next: HTMLElement | string = document.body) {
      const nextEl = resolve(next);
      if (nextEl !== current) {
        nextEl.appendChild(node);
        current = nextEl;
      }
    },
    destroy() {
      node.remove();
    },
  };
}
