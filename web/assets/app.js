const collapsibleMenus = Array.from(document.querySelectorAll('details.nav-cascade, details.mobile-menu, .mobile-menu-section details'));

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
    closeMenu(menu);
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

