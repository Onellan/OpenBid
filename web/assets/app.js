const desktopMenus = Array.from(document.querySelectorAll("details.nav-cascade"));
const mobileDrawer = document.querySelector("details.mobile-menu");
const mobileSections = Array.from(
  document.querySelectorAll(".mobile-menu-section details"),
);
const collapsibleMenus = [
  ...desktopMenus,
  ...(mobileDrawer instanceof HTMLDetailsElement ? [mobileDrawer] : []),
  ...mobileSections,
];
const disclosureRoots = Array.from(document.querySelectorAll("details"));

function firstFocusableInside(root) {
  return root.querySelector(
    'a[href], button:not([disabled]), input:not([disabled]), select:not([disabled]), textarea:not([disabled]), summary, [tabindex]:not([tabindex="-1"])',
  );
}

for (const disclosure of disclosureRoots) {
  const summary = disclosure.querySelector(":scope > summary");
  if (!(summary instanceof HTMLElement)) continue;
  const controlled = disclosure.querySelector(
    ":scope > .nav-cascade-panel, :scope > .mobile-menu-panel, :scope > .section-disclosure-body, :scope > .danger-body",
  );
  if (controlled instanceof HTMLElement) {
    if (!controlled.id) {
      controlled.id = `disclosure-panel-${Math.random().toString(36).slice(2, 10)}`;
    }
    summary.setAttribute("aria-controls", controlled.id);
  }
  summary.setAttribute("aria-expanded", disclosure.open ? "true" : "false");
  disclosure.addEventListener("toggle", function () {
    summary.setAttribute("aria-expanded", disclosure.open ? "true" : "false");
  });
  summary.addEventListener("keydown", function (e) {
    if (e.key !== "ArrowDown" || !disclosure.open) return;
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

function closeMenus(menus, activeMenu) {
  for (const menu of menus) {
    if (menu !== activeMenu) {
      closeMenu(menu);
    }
  }
}

for (const menu of desktopMenus) {
  menu.addEventListener("toggle", function () {
    if (menu.open) {
      closeMenus(desktopMenus, menu);
      closeMenu(mobileDrawer);
    }
  });
}

if (mobileDrawer instanceof HTMLDetailsElement) {
  mobileDrawer.addEventListener("toggle", function () {
    if (mobileDrawer.open) {
      closeMenus(desktopMenus);
    } else {
      closeMenus(mobileSections);
    }
  });
}

document.addEventListener("click", function (e) {
  const target = e.target;
  if (!(target instanceof Element)) return;
  for (const menu of desktopMenus) {
    if (menu.open && !menu.contains(target)) closeMenu(menu);
  }
  if (
    mobileDrawer instanceof HTMLDetailsElement &&
    mobileDrawer.open &&
    !mobileDrawer.contains(target)
  ) {
    closeMenu(mobileDrawer);
  }
});

document.addEventListener("keydown", function (e) {
  if (e.key !== "Escape") return;
  const openMenus = collapsibleMenus.filter(function (menu) {
    return menu.open;
  });
  const summary = openMenus.length
    ? openMenus[0].querySelector(":scope > summary")
    : null;
  for (const menu of openMenus) {
    closeMenu(menu);
  }
  if (summary instanceof HTMLElement) {
    summary.focus();
  }
});

document.addEventListener("click", function (e) {
  const target = e.target;
  if (!(target instanceof Element)) return;
  const navLink = target.closest(".nav-cascade-link, .mobile-menu-links a");
  if (!navLink) return;
  const href = navLink.getAttribute("href") || "";
  if (href.startsWith("#")) return;
  for (const menu of desktopMenus) {
    if (menu.contains(navLink)) closeMenu(menu);
  }
  if (
    mobileDrawer instanceof HTMLDetailsElement &&
    mobileDrawer.contains(navLink)
  ) {
    window.setTimeout(function () {
      closeMenu(mobileDrawer);
    }, 0);
  }
});

document.addEventListener("submit", function (e) {
  const form = e.target;
  if (!(form instanceof HTMLFormElement)) return;
  const message = form.getAttribute("data-confirm");
  if (message && !window.confirm(message)) {
    e.preventDefault();
  }
});

function formSnapshot(form) {
  return new URLSearchParams(new FormData(form)).toString();
}

for (const form of document.querySelectorAll("form[data-dirty-form]")) {
  if (!(form instanceof HTMLFormElement)) continue;
  const initial = formSnapshot(form);
  const submitButtons = Array.from(
    form.querySelectorAll('button[type="submit"], input[type="submit"]'),
  );
  const resetButtons = Array.from(
    form.querySelectorAll('button[type="reset"], input[type="reset"]'),
  );
  function syncDirtyState() {
    const dirty = formSnapshot(form) !== initial;
    for (const button of [...submitButtons, ...resetButtons]) {
      button.disabled = !dirty;
    }
  }
  form.addEventListener("input", syncDirtyState);
  form.addEventListener("change", syncDirtyState);
  form.addEventListener("reset", function () {
    window.setTimeout(syncDirtyState, 0);
  });
  syncDirtyState();
}

(function markCurrentPageLinks() {
  const path = window.location.pathname;
  document
    .querySelectorAll(".mobile-menu-links a[href], .nav-cascade-link[href]")
    .forEach(function (link) {
      const href = link.getAttribute("href");
      if (!href) return;
      const isActive =
        href === path ||
        (href.length > 1 && path.startsWith(href));
      if (isActive) {
        link.setAttribute("aria-current", "page");
      }
    });
})();
