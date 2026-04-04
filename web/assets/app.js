const collapsibleMenus = Array.from(document.querySelectorAll('details.nav-cascade, details.mobile-menu, .mobile-menu-section details'));
const disclosureRoots = Array.from(document.querySelectorAll('details'));

function firstFocusableInside(root) {
  return root.querySelector('a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), summary, [tabindex]:not([tabindex="-1"])');
}

for (const disclosure of disclosureRoots) {
  const summary = disclosure.querySelector(':scope > summary');
  if (!(summary instanceof HTMLElement)) continue;
  const controlled = disclosure.querySelector(':scope > .nav-cascade-panel, :scope > .mobile-menu-panel, :scope > .section-disclosure-body, :scope > .danger-body');
  if (controlled instanceof HTMLElement) {
    if (!controlled.id) {
      controlled.id = `disclosure-panel-${Math.random().toString(36).slice(2, 10)}`;
    }
    summary.setAttribute('aria-controls', controlled.id);
  }
  summary.setAttribute('aria-expanded', disclosure.open ? 'true' : 'false');
  disclosure.addEventListener('toggle', function () {
    summary.setAttribute('aria-expanded', disclosure.open ? 'true' : 'false');
  });
  summary.addEventListener('keydown', function (e) {
    if (e.key !== 'ArrowDown' || !disclosure.open) return;
    const target = firstFocusableInside(controlled || disclosure);
    if (target && target !== summary) {
      e.preventDefault();
      target.focus();
    }
  });
}

function closeMenu(menu) {
  if (menu instanceof HTMLDetailsElement) {
    menu.open = false;
  }
}

function closeOtherMenus(activeMenu) {
  for (const menu of collapsibleMenus) {
    if (menu !== activeMenu) {
      closeMenu(menu);
    }
  }
}

for (const menu of collapsibleMenus) {
  menu.addEventListener('toggle', function () {
    if (menu.open) {
      closeOtherMenus(menu);
    }
  });
}

document.addEventListener('click', function(e){
  const target = e.target;
  if (!(target instanceof Element)) return;
  for (const menu of collapsibleMenus) {
    if (menu.open && !menu.contains(target)) {
      closeMenu(menu);
    }
  }
});

document.addEventListener('keydown', function(e){
  if (e.key !== 'Escape') return;
  for (const menu of collapsibleMenus) {
    const summary = menu.querySelector(':scope > summary');
    closeMenu(menu);
    if (summary instanceof HTMLElement) {
      summary.focus();
    }
  }
});

document.addEventListener('click', function(e){
  const target = e.target;
  if (!(target instanceof Element)) return;
  const navLink = target.closest('.nav-cascade-link, .mobile-menu-links a');
  if (!navLink) return;
  for (const menu of collapsibleMenus) {
    if (menu.contains(navLink)) {
      closeMenu(menu);
    }
  }
});

document.addEventListener('submit', function(e){
  const form = e.target;
  if (!(form instanceof HTMLFormElement)) return;
  const message = form.getAttribute('data-confirm');
  if (message && !window.confirm(message)) {
    e.preventDefault();
  }
});

