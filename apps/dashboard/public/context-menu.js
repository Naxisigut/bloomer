// One floating menu reused by every right-click action. It is appended to the
// body only while open, so pages that never open it pay no DOM cost.
//
// Items are `{ label, disabled, hint, onSelect }`; `hint` explains a disabled
// item and is shown as its title. Closing restores focus to the opener unless
// the close came from a press somewhere else (then focus belongs to the target).
export function createContextMenu() {
  let element = null, opener = null;

  function close(restoreFocus = true) {
    if (!element) return;
    element.remove();
    element = null;
    document.removeEventListener('pointerdown', onPointerDown, true);
    document.removeEventListener('keydown', onKeyDown, true);
    window.removeEventListener('scroll', onDismiss, true);
    window.removeEventListener('resize', onDismiss);
    const target = opener;
    opener = null;
    if (restoreFocus) target?.focus?.();
  }

  function onPointerDown(event) {
    if (element && !element.contains(event.target)) close(false);
  }

  function onDismiss() { close(false); }

  const itemsIn = () => [...(element?.querySelectorAll('[role="menuitem"]:not([disabled])') || [])];

  function onKeyDown(event) {
    if (!element) return;
    if (event.key === 'Escape' || event.key === 'Tab') { event.preventDefault(); close(); return; }
    if (event.key !== 'ArrowDown' && event.key !== 'ArrowUp' && event.key !== 'Home' && event.key !== 'End') return;
    const items = itemsIn();
    if (!items.length) return;
    event.preventDefault();
    const index = items.indexOf(document.activeElement);
    const next = { ArrowDown: index + 1, ArrowUp: index - 1, Home: 0, End: items.length - 1 }[event.key];
    items[((next % items.length) + items.length) % items.length].focus();
  }

  function place(x, y) {
    const gap = 8;
    const bounds = element.getBoundingClientRect();
    const left = Math.min(Math.max(gap, x), window.innerWidth - bounds.width - gap);
    const top = Math.min(Math.max(gap, y), window.innerHeight - bounds.height - gap);
    element.style.left = `${Math.max(gap, left)}px`;
    element.style.top = `${Math.max(gap, top)}px`;
  }

  function open({ x, y, label = '', items = [] }) {
    close(false);
    if (!items.length) return;
    opener = document.activeElement instanceof HTMLElement ? document.activeElement : null;
    element = document.createElement('div');
    element.className = 'context-menu';
    element.setAttribute('role', 'menu');
    if (label) element.setAttribute('aria-label', label);
    element.innerHTML = items.map(item => {
      const hint = item.disabled && item.hint ? ` title="${escapeAttribute(item.hint)}"` : '';
      return `<button type="button" class="context-menu-item" role="menuitem"${item.disabled ? ' aria-disabled="true" disabled' : ''}${hint}>${escapeAttribute(item.label)}${item.disabled && item.hint ? `<small>${escapeAttribute(item.hint)}</small>` : ''}</button>`;
    }).join('');
    [...element.querySelectorAll('[role="menuitem"]')].forEach((button, index) => {
      button.addEventListener('click', () => {
        const item = items[index];
        close();
        if (!item.disabled) item.onSelect?.();
      });
    });
    document.body.append(element);
    place(x, y);
    const first = itemsIn()[0];
    if (first) first.focus();
    else element.querySelector('[role="menuitem"]')?.focus();
    document.addEventListener('pointerdown', onPointerDown, true);
    document.addEventListener('keydown', onKeyDown, true);
    window.addEventListener('scroll', onDismiss, true);
    window.addEventListener('resize', onDismiss);
  }

  return { open, close: () => close() };
}

const escapeAttribute = value => String(value ?? '').replace(/[&<>"']/g, character => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;', "'": '&#39;' })[character]);
