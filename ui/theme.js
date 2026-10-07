'use strict';

// Тема оформления: auto (как в Windows) | light | dark. Скрипт стоит в <head>
// без defer, чтобы цвета встали до первой отрисовки; выбор хранится в
// настройках программы, а здесь - его копия в localStorage.
(function () {
  const mq = window.matchMedia('(prefers-color-scheme: light)');
  let pref = 'auto';
  try { pref = localStorage.getItem('theme') || 'auto'; } catch (e) { /* без хранилища */ }

  function apply() {
    const t = pref === 'light' || pref === 'dark' ? pref : (mq.matches ? 'light' : 'dark');
    document.documentElement.setAttribute('data-theme', t);
  }

  window.mejTheme = {
    set(p) {
      pref = p === 'light' || p === 'dark' ? p : 'auto';
      try { localStorage.setItem('theme', pref); } catch (e) { /* без хранилища */ }
      apply();
    },
  };
  mq.addEventListener('change', apply);
  // на случай, если окно в фоне не получило смену темы Windows
  window.addEventListener('focus', apply);
  document.addEventListener('visibilitychange', apply);
  apply();
})();
