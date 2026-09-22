'use strict';

document.addEventListener('contextmenu', (e) => e.preventDefault());

// ---------- утилиты ----------
const $ = (s, el = document) => el.querySelector(s);
const $$ = (s, el = document) => [...el.querySelectorAll(s)];

function h(tag, attrs, ...kids) {
  const el = document.createElement(tag);
  for (const [k, v] of Object.entries(attrs || {})) {
    if (v == null || v === false) continue;
    if (k === 'class') el.className = v;
    else if (k.startsWith('on')) el.addEventListener(k.slice(2), v);
    else el.setAttribute(k, v === true ? '' : v);
  }
  for (const kid of kids.flat()) {
    if (kid == null || kid === false) continue;
    el.append(kid instanceof Node ? kid : document.createTextNode(String(kid)));
  }
  return el;
}

function svg(paths) {
  const el = document.createElementNS('http://www.w3.org/2000/svg', 'svg');
  el.setAttribute('viewBox', '0 0 24 24');
  el.innerHTML = paths;
  return el;
}

const esc = (s) => s.replace(/[&<>]/g, (c) => ({ '&': '&amp;', '<': '&lt;', '>': '&gt;' }[c]));

function fmtBytes(n) {
  if (!n) return '0 Б';
  const u = ['Б', 'КБ', 'МБ', 'ГБ', 'ТБ'];
  let i = 0;
  while (n >= 1024 && i < u.length - 1) { n /= 1024; i++; }
  return (i === 0 ? n : n.toFixed(n < 10 ? 2 : n < 100 ? 1 : 0)).toString().replace('.', ',') + ' ' + u[i];
}
const fmtSpeed = (n) => fmtBytes(n) + '/с';

function fmtDuration(ms) {
  const s = Math.max(0, Math.floor(ms / 1000));
  const hh = Math.floor(s / 3600), mm = Math.floor((s % 3600) / 60), ss = s % 60;
  const p = (x) => String(x).padStart(2, '0');
  return (hh ? hh + ':' : '') + p(mm) + ':' + p(ss);
}

function toast(msg, type = 'info', action) {
  const box = h('div', { class: 'toast ' + type }, h('span', {}, msg));
  if (action) box.append(h('button', { class: 'btn small', onclick: () => { action.fn(); box.remove(); } }, action.label));
  $('#toasts').append(box);
  setTimeout(() => box.remove(), action ? 9000 : 4500);
}

// ---------- API ----------
const token = (() => {
  const m = location.hash.match(/t=([0-9a-f]+)/);
  if (m) {
    try { sessionStorage.setItem('deskToken', m[1]); } catch (e) { /* без storage токен живёт до перезагрузки */ }
    history.replaceState(null, '', location.pathname);
    return m[1];
  }
  try { return sessionStorage.getItem('deskToken') || ''; } catch (e) { return ''; }
})();

// новый токен в адресе без перезагрузки страницы (окно открыли заново)
window.addEventListener('hashchange', () => { if (/t=[0-9a-f]+/.test(location.hash)) location.reload(); });

let failures = 0;
function showOverlay(title, text) {
  $('#overlayTitle').textContent = title;
  $('#overlayText').textContent = text;
  $('#overlay').hidden = false;
}

async function api(path, { method = 'GET', body } = {}) {
  const opt = { method, headers: { 'X-Desk-Token': token } };
  if (body !== undefined) {
    opt.headers['Content-Type'] = 'application/json';
    opt.body = JSON.stringify(body);
  }
  let r;
  try {
    r = await fetch('/api' + path, opt);
  } catch (e) {
    if (++failures >= 3) showOverlay('Программа закрыта', 'Mejgorod больше не отвечает. Запустите её снова.');
    throw new Error('Программа не отвечает');
  }
  if (failures) { failures = 0; if ($('#overlayTitle').textContent === 'Программа закрыта') $('#overlay').hidden = true; }
  if (r.status === 401) {
    showOverlay('Окно устарело', 'Откройте Mejgorod заново из значка в трее.');
    throw new Error('unauthorized');
  }
  const text = await r.text();
  let data = {};
  try { data = text ? JSON.parse(text) : {}; } catch (e) { data = { error: text }; }
  if (!r.ok) throw new Error(data.error || data.message || 'HTTP ' + r.status);
  return data;
}

// ---------- навигация ----------
let page = 'home';
function go(p) {
  page = p;
  $$('.nav-item').forEach((b) => b.classList.toggle('active', b.dataset.page === p));
  $$('.page').forEach((s) => s.classList.toggle('active', s.id === 'page-' + p));
  if (p === 'groups') { loadGroups(); loadBoard(); }
  if (p === 'home') { loadServers(); loadProfiles(); }
  if (p === 'logs') { $('#navLogDot').hidden = true; scrollLogs(true); loadCoreLevel(); }
  if (p === 'settings') loadSettings();
  if (p === 'servers') { srvAutoPinged = false; loadServers(); }
  if (p === 'config') { renderEditor(); ta.focus(); }
}
$$('.nav-item').forEach((b) => b.addEventListener('click', () => go(b.dataset.page)));
document.addEventListener('click', (e) => {
  const g = e.target.closest('[data-goto]');
  if (g) go(g.dataset.goto);
});

// ---------- состояние ----------
let S = null;
let prevStatus = '';
const STATUS_TEXT = {
  stopped: 'Отключено', starting: 'Подключение', running: 'Подключено', stopping: 'Отключение', error: 'Ошибка',
};

// Кнопка питания: человечек поднимает флаг нужного цвета. Между цветами -
// гифка перехода (играет один раз с начала), в покое - статичная картинка.
// Гифка зациклена (иначе на медленном "starting" ей нечем тянуть время), поэтому
// на паузе после первого прохода подменяем её на статичную - не будет ни
// зацикливания обратно, ни лишней анимации, пока ничего не меняется.
let powerColor = 'white'; // сейчас показано: white | green | red
let powerFirst = true; // при первом рендере красим сразу, без анимации перехода
let powerSwapTimer = null;
const POWER_TARGET = { stopped: 'white', running: 'green', starting: 'green', stopping: 'white', error: 'red' };
const POWER_HOLD_MS = 1400; // все 6 гифок ~2.18с, дальше идут по кругу; кадр не меняется с 1.05 по 2.18с

function updatePowerArt(st) {
  clearTimeout(powerSwapTimer);
  const target = POWER_TARGET[st] || 'white';
  const img = $('#powerArt');
  const idle = '/power/idle-' + target + '.png';
  if (powerFirst || target === powerColor) {
    img.src = idle;
  } else {
    img.src = '/power/flag_' + powerColor + '_to_' + target + '.gif?t=' + Date.now();
    powerSwapTimer = setTimeout(() => { img.src = idle; }, POWER_HOLD_MS);
  }
  powerColor = target;
  powerFirst = false;
}

// Свёрнутое окно почти не опрашивает программу; при разворачивании - сразу обновляемся.
const timers = {};
function schedule(name, fn, ms) {
  clearTimeout(timers[name]);
  timers[name] = setTimeout(fn, ms);
}
document.addEventListener('visibilitychange', () => {
  if (document.hidden) return;
  pollState();
  pollLogs();
  if (page === 'groups') { loadGroups(); loadBoard(); }
  if (page === 'home' || page === 'servers') loadServers();
});

let loadedVersion = '';
async function pollState() {
  try {
    S = await api('/state');
    // программа обновилась и перезапустилась с тем же окном - берём новый интерфейс
    if (loadedVersion && S.app.version !== loadedVersion) { location.reload(); return; }
    loadedVersion = S.app.version;
    renderState();
  } catch (e) { /* покажет оверлей */ }
  const fast = S && (S.status === 'starting' || S.status === 'stopping' || S.download.active || S.update.busy);
  schedule('state', pollState, document.hidden ? 15000 : fast ? 400 : 1000);
}

function renderState() {
  const st = S.status;
  document.body.className = 'st-' + st;
  $('#brandStatus').textContent = STATUS_TEXT[st].toLowerCase();
  $('#heroStatus').textContent = STATUS_TEXT[st];
  $('#miniPowerText').textContent = st === 'running' || st === 'starting' ? 'Отключить' : st === 'stopping' ? 'Отключение' : 'Подключить';
  $('#power').setAttribute('aria-label', st === 'running' ? 'Отключить' : 'Подключить');

  let sub = '';
  if (st === 'running') sub = 'В сети ' + fmtDuration(Date.now() - S.startedAt);
  else if (st === 'starting') sub = S.stage || 'Подготовка';
  else if (st === 'stopping') sub = 'Останавливаю ядро';
  else if (st === 'error') sub = 'Нажмите, чтобы попробовать снова';
  else sub = 'Нажмите, чтобы подключиться';
  if (st === 'starting' && S.download.active && S.download.message) sub = S.download.message;
  $('#heroSub').textContent = sub;

  const prog = $('#heroProgress');
  prog.hidden = !(st === 'starting' && S.download.active && S.download.progress >= 0);
  if (!prog.hidden) prog.firstElementChild.style.width = Math.round(S.download.progress * 100) + '%';


  const err = $('#heroError');
  err.hidden = st !== 'error' || !S.error;
  err.textContent = S.error || '';

  renderNotice();

  const t = S.traffic || {};
  $('#stDown').textContent = fmtSpeed(t.down || 0);
  $('#stUp').textContent = fmtSpeed(t.up || 0);
  $('#stTotal').textContent = fmtBytes((t.downTotal || 0) + (t.upTotal || 0));
  $('#stConns').textContent = t.conns || 0;

  $('#versions').textContent = 'v' + S.app.version + ' · ' + (S.core.installed ? 'ядро ' + (S.core.version || '...') : 'ядро не скачано');
  $('#dataDir').textContent = S.app.dataDir;
  renderCoreBlock();
  renderAppUpdate();
  renderUpdatePrompt();

  if (st !== prevStatus) {
    updatePowerArt(st);
    if (page === 'groups') { loadGroups(); loadBoard(); }
    if (page === 'home') loadServers();
    if (page === 'servers') loadServers();
    if (st === 'error' && page !== 'logs') $('#navLogDot').hidden = false;
    $('#coreLevel').disabled = st !== 'running';
    if (st === 'running' && page === 'logs') loadCoreLevel();
    prevStatus = st;
  }
}

function renderNotice() {
  const n = $('#homeNotice');
  const u = S.update;
  const key = [S.elevated, S.config.exists, u.available, u.version, u.busy, u.message, u.error, Math.round((u.progress || 0) * 20)].join();
  if (n.dataset.key === key) return; // не пересоздаём кнопки каждую секунду
  n.dataset.key = key;
  n.replaceChildren();
  if (!S.elevated) {
    n.append(h('div', { class: 'notice-row' }, h('span', {}, 'Программа запущена без прав администратора: VPN (TUN) не заработает. Закройте её и запустите заново, подтвердив запрос Windows.')));
  }
  if (!S.config.exists) {
    n.append(h('div', { class: 'notice-row' },
      h('span', {}, 'Конфига ещё нет. Выберите готовый шаблон или вставьте свой.'),
      h('button', { class: 'btn small', onclick: () => { go('config'); openTemplates(); } }, 'Выбрать шаблон')));
  }
  if (u.available || u.busy) {
    const pct = u.busy && u.progress >= 0 && u.progress < 1 ? ' ' + Math.round(u.progress * 100) + '%' : '';
    const text = u.busy ? (u.message || 'Обновляю') + pct
      : u.error ? 'Не удалось обновить до ' + u.version + ': ' + u.error
        : 'Доступна новая версия ' + u.version;
    const row = h('div', { class: 'notice-row' }, h('span', {}, text));
    if (!u.busy) {
      const acts = h('div', { class: 'head-actions' });
      if (u.url) acts.append(h('button', { class: 'btn small', onclick: () => window.open(u.url, '_blank') }, 'Что нового'));
      acts.append(h('button', { class: 'btn small primary', onclick: startAppUpdate }, 'Обновить'));
      row.append(acts);
    }
    n.append(row);
  }
  n.hidden = !n.childElementCount;
}

// Вопрос «Обновить? Да / Нет» на всё окно: при запуске, как только проверка
// нашла новую версию. «Нет» - больше не спрашиваем до следующего запуска.
let updDismissed = (() => { try { return sessionStorage.getItem('updDismissed') || ''; } catch (e) { return ''; } })();
function renderUpdatePrompt() {
  const u = S.update;
  const m = $('#updModal');
  const want = u.busy || (u.available && u.version !== updDismissed);
  if (!want) { if (m.open) m.close(); return; }
  $('#updFrom').textContent = S.app.version;
  $('#updTo').textContent = u.version;
  const notes = $('#updNotes');
  if (notes.textContent !== (u.notes || '')) notes.textContent = u.notes || '';
  const bar = $('#updProgress');
  bar.hidden = !u.busy;
  bar.classList.toggle('indet', u.busy && u.progress < 0);
  bar.firstElementChild.style.width = u.progress >= 0 ? Math.round(u.progress * 100) + '%' : '';
  const msg = $('#updMsg');
  const pct = u.busy && u.progress >= 0 && u.progress < 1 ? ' ' + Math.round(u.progress * 100) + '%' : '';
  msg.textContent = u.busy ? (u.message || 'Обновляю') + pct : u.error ? 'Не получилось: ' + u.error : '';
  msg.classList.toggle('err', !u.busy && !!u.error);
  $('#updTitle').textContent = u.busy ? 'Обновляю Mejgorod' : 'Есть обновление';
  $('#updQ').hidden = u.busy;
  $('#updActions').hidden = u.busy;
  $('#updYes').textContent = u.error ? 'Повторить' : 'Да, обновить';
  if (!m.open) m.showModal();
}
function dismissUpdate() {
  updDismissed = S.update.version;
  try { sessionStorage.setItem('updDismissed', updDismissed); } catch (e) { /* до перезагрузки окна */ }
  $('#updModal').close();
}
$('#updNo').addEventListener('click', dismissUpdate);
$('#updYes').addEventListener('click', startAppUpdate);
$('#updModal').addEventListener('cancel', (e) => { e.preventDefault(); if (!S.update.busy) dismissUpdate(); });

async function startAppUpdate() {
  if (cfgDirty() && !confirm('В редакторе конфига есть несохранённые изменения, они пропадут при перезапуске. Обновить?')) return;
  try {
    await api('/app/update', { method: 'POST' });
    S.update.busy = true;
    S.update.message = 'Скачиваю обновление';
    renderUpdatePrompt();
  } catch (e) { toast(e.message, 'err'); }
}

function renderAppUpdate() {
  const u = S.update;
  $('#appVer').textContent = 'Mejgorod ' + S.app.version;
  const msg = $('#appUpdMsg');
  msg.textContent = u.busy ? (u.message || 'Обновляю')
    : u.error ? 'Ошибка: ' + u.error
      : u.available ? 'Доступна версия ' + u.version
        : u.message || 'Обновления берутся из релизов github.com/Trashfallen/mejgorod';
  msg.style.color = u.error ? 'var(--err)' : u.available ? 'var(--accent-text)' : '';
  const bar = $('#appUpdProgress');
  bar.hidden = !u.busy;
  bar.classList.toggle('indet', u.busy && u.progress < 0);
  bar.firstElementChild.style.width = u.progress >= 0 ? Math.round(u.progress * 100) + '%' : '';
  const b = $('#appUpdBtn');
  b.disabled = u.busy;
  b.textContent = u.available ? 'Обновить до ' + u.version : 'Проверить обновления';
}

$('#appUpdBtn').addEventListener('click', async (e) => {
  if (S && S.update.available) return startAppUpdate();
  const btn = e.currentTarget;
  busy(btn, true);
  try {
    const u = await api('/app/check', { method: 'POST' });
    if (u.error) toast(u.error, 'err');
    else if (u.available) toast('Доступна версия ' + u.version, 'ok');
    else toast(u.message || 'Установлена последняя версия', 'ok');
    S.update = u;
    renderAppUpdate();
    renderNotice();
  } catch (err) { toast(err.message, 'err'); }
  busy(btn, false);
});

async function togglePower() {
  if (!S) return;
  const st = S.status;
  try {
    if (st === 'running' || st === 'starting') S = await api('/disconnect', { method: 'POST' });
    else if (st === 'stopped' || st === 'error') {
      if (cfgDirty()) toast('В редакторе есть несохранённые изменения: подключаюсь с сохранённым конфигом', 'warn');
      S = await api('/connect', { method: 'POST' });
    }
    renderState();
  } catch (e) { toast(e.message, 'err'); }
}
$('#power').addEventListener('click', togglePower);
$('#miniPower').addEventListener('click', togglePower);

// ---------- группы ----------
let groupsData = null;
const delays = new Map(); // имя -> мс, 0 = не ответил

async function loadGroups() {
  if (!S || S.status !== 'running') {
    groupsData = null;
    return;
  }
  try {
    const d = await api('/mihomo/proxies');
    const px = d.proxies || {};
    const order = (px.GLOBAL && px.GLOBAL.all) || [];
    const seen = new Set();
    const groups = [];
    const add = (name) => {
      const p = px[name];
      if (!p || !Array.isArray(p.all) || p.hidden || name === 'GLOBAL' || seen.has(name)) return;
      seen.add(name);
      groups.push(p);
    };
    order.forEach(add);
    Object.keys(px).forEach(add);
    groupsData = { groups, px };
  } catch (e) {
    groupsData = null;
  }
}

// Явный пинг показываем как есть (0 = «нет»). Из истории ядра берём только
// удачные замеры: автопроверка сразу после запуска часто падает, пока сеть
// поднимается, и «нет» у рабочего сервера только путает.
function delayOf(name) {
  if (delays.has(name)) return delays.get(name);
  const p = groupsData && groupsData.px[name];
  const hist = p && p.history;
  const last = hist && hist.length ? hist[hist.length - 1].delay : 0;
  return last > 0 ? last : null;
}

function delayEl(name) {
  const d = delayOf(name);
  if (d == null) return h('span', { class: 'delay' }, '');
  if (d === 0) return h('span', { class: 'delay fail' }, 'нет');
  return h('span', { class: 'delay ' + (d < 250 ? 'good' : d < 600 ? 'mid' : 'bad') }, d + ' мс');
}

function groupIcon(g) {
  const letter = h('div', { class: 'gletter' }, (g.name.trim()[0] || '?').toUpperCase());
  if (!g.icon) return letter;
  const img = h('img', { class: 'gicon', src: g.icon, alt: '', loading: 'lazy', referrerpolicy: 'no-referrer' });
  img.addEventListener('error', () => img.replaceWith(letter));
  return img;
}


async function selectProxy(group, name) {
  try {
    await api('/mihomo/proxies/' + encodeURIComponent(group), { method: 'PUT', body: { name } });
    await loadGroups();
    if (page === 'groups') loadBoard();
  } catch (e) { toast('Не удалось переключить: ' + e.message, 'err'); }
}

const PING_URL = 'https://www.gstatic.com/generate_204';
async function pingGroup(name, btn) {
  if (btn) btn.classList.add('busy');
  try {
    const d = await api('/mihomo/group/' + encodeURIComponent(name) + '/delay?url=' + encodeURIComponent(PING_URL) + '&timeout=5000');
    const g = groupsData && groupsData.groups.find((x) => x.name === name);
    if (g) g.all.forEach((n) => delays.set(n, d[n] || 0));
  } catch (e) {
    // группа целиком не ответила (например, все серверы недоступны)
    const g = groupsData && groupsData.groups.find((x) => x.name === name);
    if (g) g.all.forEach((n) => { if (!delays.has(n)) delays.set(n, 0); });
  }
  if (btn) btn.classList.remove('busy');
}

setInterval(() => {
  if (document.hidden) return;
  if (page === 'groups') { loadGroups(); loadBoard(); }
  if (page === 'servers' || page === 'home') loadServers();
}, 5000);

// ---------- доска «Через VPN / Напрямую» ----------
let BOARD = null;
let dragInfo = null;
const ICON_GLOBE = '<circle cx="12" cy="12" r="9"/><path d="M3 12h18"/><path d="M12 3a14 14 0 0 1 0 18a14 14 0 0 1 0-18"/>';
const ICON_APP = '<rect x="3" y="4" width="18" height="16" rx="2"/><path d="M3 9h18"/>';
const ICON_LOCK = '<rect x="5" y="11" width="14" height="10" rx="2"/><path d="M8 11V7a4 4 0 0 1 8 0v4"/>';
const ICON_SWAP = '<path d="M4 8h14l-4-4"/><path d="M20 16H6l4 4"/>';
const OPT_TEXT = { DIRECT: 'Напрямую', PASS: 'По общим правилам', REJECT: 'Блокировать', 'REJECT-DROP': 'Блокировать молча' };

async function loadBoard() {
  if (dragInfo) return; // не перерисовываем посреди перетаскивания
  try { BOARD = await api('/board'); } catch (e) { BOARD = null; }
  renderBoard();
  if ($('#grpModal').open) renderGroupModal();
}

function colOf(route) { return route === 'vpn' ? 'vpn' : 'direct'; }

function groupSub(g) {
  if (g.route === 'direct') return 'напрямую';
  if (g.route === 'pass') return 'по общим правилам (дальше в конфиге)';
  if (g.route === 'block') return 'заблокировано';
  return 'через ' + g.now;
}

function renderBoard() {
  const empty = $('#boardEmpty');
  const noGroups = BOARD && !BOARD.groups.length && !BOARD.items.length;
  empty.hidden = !!BOARD && !noGroups;
  $('#board').hidden = !BOARD;
  if (!BOARD) { empty.textContent = 'Не удалось прочитать конфиг'; return; }
  if (noGroups) empty.textContent = 'В конфиге нет групп сервисов. Добавьте сайты и программы кнопкой «+» или выберите шаблон на вкладке «Конфиг».';
  $('#boardHint').textContent = 'Перетащите сервис в нужную колонку или нажмите на него, чтобы выбрать сервер. «+» добавляет свой сайт или программу.' +
    (BOARD.running ? '' : ' VPN выключен: выбор сохранится и применится при подключении.');

  const q = $('#groupSearch').value.trim().toLowerCase();
  const svc = { vpn: [], direct: [] };
  const own = { vpn: [], direct: [] };
  for (const g of BOARD.groups) {
    if (!q || g.name.toLowerCase().includes(q)) svc[colOf(g.route)].push(groupCard(g));
  }
  for (const it of BOARD.items) {
    if (!q || it.label.toLowerCase().includes(q)) own[colOf(it.route)].push(itemCard(it));
  }
  for (const r of ['vpn', 'direct']) {
    const kids = [...svc[r]];
    if (own[r].length) kids.push(h('div', { class: 'col-sub' }, 'Свои сайты и программы'), ...own[r]);
    if (!kids.length) {
      kids.push(h('div', { class: 'col-empty' }, q ? 'Ничего не найдено' :
        r === 'vpn' ? 'Перетащите сюда то, что должно идти через VPN' : 'Перетащите сюда то, что должно идти напрямую'));
    }
    $(r === 'vpn' ? '#colVpn' : '#colDirect').replaceChildren(...kids);
    const n = svc[r].length + own[r].length;
    $(r === 'vpn' ? '#cntVpn' : '#cntDirect').textContent = n ? String(n) : '';
  }
}

function groupCard(g) {
  const to = colOf(g.route) === 'vpn' ? 'direct' : 'vpn';
  const canMove = !!(to === 'vpn' ? g.vpn : g.direct);
  const el = h('div', {
    class: 'bcard' + (canMove ? '' : ' locked'),
    draggable: canMove ? 'true' : null,
    title: canMove ? 'Перетащите в другую колонку или нажмите, чтобы выбрать сервер'
      : 'У группы нет варианта ' + (to === 'vpn' ? 'через VPN' : '«напрямую»') + '. Нажмите, чтобы выбрать сервер',
  },
  groupIcon(g),
  h('div', { class: 'btext' }, h('b', {}, g.name), h('span', {}, groupSub(g))),
  canMove
    ? h('button', { class: 'icon-btn', title: to === 'vpn' ? 'Через VPN' : 'Напрямую', onclick: (e) => { e.stopPropagation(); moveGroup(g, to); } }, svg(ICON_SWAP))
    : h('span', { class: 'bicon lock' }, svg(ICON_LOCK)));
  el.addEventListener('click', () => openGroupModal(g.name));
  if (canMove) dragSource(el, { type: 'group', g, from: colOf(g.route) });
  return el;
}

function itemCard(it) {
  const to = it.route === 'vpn' ? 'direct' : 'vpn';
  const el = h('div', { class: 'bcard custom', draggable: 'true', title: 'Перетащите в другую колонку' },
    h('span', { class: 'bicon' }, svg(it.kind === 'app' ? ICON_APP : ICON_GLOBE)),
    h('div', { class: 'btext' }, h('b', {}, it.label), h('span', {}, it.detail)),
    h('button', { class: 'icon-btn', title: to === 'vpn' ? 'Через VPN' : 'Напрямую', onclick: () => moveItem(it, to) }, svg(ICON_SWAP)),
    h('button', { class: 'icon-btn danger', title: 'Удалить', onclick: () => deleteItem(it) }, svg(ICON_TRASH)));
  dragSource(el, { type: 'item', it, from: it.route });
  return el;
}

function dragSource(el, info) {
  el.addEventListener('dragstart', (e) => {
    dragInfo = info;
    el.classList.add('dragging');
    e.dataTransfer.effectAllowed = 'move';
    e.dataTransfer.setData('text/plain', info.type);
  });
  el.addEventListener('dragend', () => {
    dragInfo = null;
    el.classList.remove('dragging');
    $$('.col').forEach((c) => c.classList.remove('drop'));
  });
}

$$('.col').forEach((col) => {
  const route = col.dataset.route;
  col.addEventListener('dragover', (e) => {
    if (!dragInfo || dragInfo.from === route) return;
    e.preventDefault();
    e.dataTransfer.dropEffect = 'move';
    col.classList.add('drop');
  });
  col.addEventListener('dragleave', (e) => { if (!col.contains(e.relatedTarget)) col.classList.remove('drop'); });
  col.addEventListener('drop', (e) => {
    e.preventDefault();
    col.classList.remove('drop');
    const info = dragInfo;
    dragInfo = null;
    if (!info || info.from === route) return;
    if (info.type === 'group') moveGroup(info.g, route);
    else moveItem(info.it, route);
  });
});
$('#groupSearch').addEventListener('input', renderBoard);

async function moveGroup(g, route) {
  const option = route === 'vpn' ? g.vpn : g.direct;
  if (!option) return toast('У группы «' + g.name + '» нет варианта ' + (route === 'vpn' ? 'через VPN' : '«напрямую»'), 'warn');
  await chooseGroupOption(g.name, option);
}

async function chooseGroupOption(name, option) {
  try {
    BOARD = await api('/board/group', { method: 'PUT', body: { name, option } });
    renderBoard();
    if ($('#grpModal').open) renderGroupModal();
    loadGroups();
  } catch (e) { toast(e.message, 'err'); }
}

// свои сайты и программы меняют конфиг: редактор не должен держать старую копию
function configLocked() {
  if (!cfgDirty()) return false;
  toast('Сначала сохраните или отмените изменения на вкладке «Конфиг»', 'warn');
  return true;
}

function afterDeskChange(d, msg) {
  BOARD = d.board;
  renderBoard();
  loadConfig();
  toast(msg + (d.restarted ? '. Переподключаюсь' : ''), 'ok');
}

async function moveItem(it, route) {
  if (configLocked()) return;
  try {
    const d = await api('/board/items/' + encodeURIComponent(it.id), { method: 'PUT', body: { route } });
    afterDeskChange(d, it.label + (route === 'vpn' ? ': через VPN' : ': напрямую'));
  } catch (e) { toast(e.message, 'err'); }
}

async function deleteItem(it) {
  if (configLocked()) return;
  if (!confirm('Убрать «' + it.label + '» из правил?')) return;
  try {
    const d = await api('/board/items/' + encodeURIComponent(it.id), { method: 'DELETE' });
    afterDeskChange(d, 'Удалено: ' + it.label);
  } catch (e) { toast(e.message, 'err'); }
}

// ---------- выбор сервера в группе ----------
let grpName = null;
function openGroupModal(name) {
  grpName = name;
  renderGroupModal();
  $('#grpModal').showModal();
}
function renderGroupModal() {
  const g = BOARD && BOARD.groups.find((x) => x.name === grpName);
  if (!g) { $('#grpModal').close(); return; }
  $('#grpTitle').textContent = g.name;
  $('#grpNote').textContent = BOARD.running ? 'Куда идёт трафик этой группы:' : 'VPN выключен: выбор применится при подключении.';
  $('#grpOpts').replaceChildren(...g.options.map((o) =>
    h('button', { class: 'opt' + (o === g.now ? ' on' : ''), title: o, onclick: () => chooseGroupOption(g.name, o) },
      h('span', { class: 'oname' }, OPT_TEXT[o] || o), delayEl(o))));
  $('#grpPing').disabled = !BOARD.running;
}
$('#grpClose').addEventListener('click', () => $('#grpModal').close());
$('#grpPing').addEventListener('click', async (e) => {
  await loadGroups();
  await pingGroup(grpName, e.currentTarget);
  renderGroupModal();
});

// ---------- «+»: свой сайт или программа ----------
const addModal = $('#addModal');
let addState = { kind: 'site', route: 'vpn', found: null, foundFor: '', procs: null };

$$('[data-add]').forEach((b) => b.addEventListener('click', () => openAdd(b.dataset.add)));

function openAdd(route) {
  if (configLocked()) return;
  addState = { kind: 'site', route, found: null, foundFor: '', procs: null };
  $('#siteInput').value = '';
  $('#siteExtra').value = '';
  $('#appInput').value = '';
  $('#appSearch').value = '';
  $('#siteResult').replaceChildren();
  setAddResult('');
  renderAddTabs();
  addModal.showModal();
  $('#siteInput').focus();
}
function renderAddTabs() {
  $$('#addKind button').forEach((b) => b.classList.toggle('on', b.dataset.k === addState.kind));
  $$('#addRoute button').forEach((b) => b.classList.toggle('on', b.dataset.r === addState.route));
  $('#addSite').hidden = addState.kind !== 'site';
  $('#addApp').hidden = addState.kind !== 'app';
  if (addState.kind === 'app' && !addState.procs) loadProcs();
}
function setAddResult(text, fail) {
  const r = $('#addResult');
  r.className = 'result' + (text ? (fail ? ' fail' : ' ok') : '');
  r.textContent = text;
}
$$('#addKind button').forEach((b) => b.addEventListener('click', () => { addState.kind = b.dataset.k; setAddResult(''); renderAddTabs(); }));
$$('#addRoute button').forEach((b) => b.addEventListener('click', () => { addState.route = b.dataset.r; renderAddTabs(); }));
$('#addClose').addEventListener('click', () => addModal.close());

async function findSite() {
  const q = $('#siteInput').value.trim();
  if (!q) { setAddResult('Введите адрес сайта', true); return false; }
  const btn = $('#siteFind');
  busy(btn, true);
  setAddResult('');
  try {
    const d = await api('/lookup?site=' + encodeURIComponent(q));
    addState.found = d.result;
    addState.foundFor = q;
    renderFound(d.warning);
    return true;
  } catch (e) {
    addState.found = null;
    setAddResult(e.message, true);
    return false;
  } finally { busy(btn, false); }
}

function renderFound(warning) {
  const f = addState.found;
  const rows = [h('label', {}, h('input', { type: 'checkbox', checked: true, disabled: true }),
    h('span', {}, 'Домен ', h('b', {}, f.domain), ' и все его поддомены'))];
  if (f.geosite) {
    const more = f.geosite.count - f.geosite.sample.length;
    rows.push(h('label', {}, h('input', { type: 'checkbox', id: 'useGeosite', checked: f.geosite.contains }),
      h('span', {}, 'Готовый список «' + f.geosite.name + '»: доменов ' + f.geosite.count + ', обновляется сам' +
        (f.geosite.contains ? '' : '. В нём нет ' + f.domain + ': проверьте, тот ли это сайт'),
      h('div', { class: 'sample' }, f.geosite.sample.join('  ') + (more > 0 ? '  и ещё ' + more : '')))));
  }
  if (f.geoip) {
    rows.push(h('label', {}, h('input', { type: 'checkbox', id: 'useGeoip', checked: true }),
      h('span', {}, 'IP-адреса «' + f.geoip.name + '»: подсетей ' + f.geoip.count + ', для приложений, которые ходят без домена')));
  }
  if (!f.geosite) {
    rows.push(h('div', { class: 'muted small' }, 'Готового списка для этого сайта нет. Добавится домен со всеми поддоменами, другие его домены можно дописать ниже.'));
  }
  if (warning) rows.push(h('div', { class: 'muted small' }, warning));
  $('#siteResult').replaceChildren(h('div', { class: 'found' }, ...rows));
}

$('#siteFind').addEventListener('click', findSite);
$('#siteInput').addEventListener('keydown', (e) => { if (e.key === 'Enter') { e.preventDefault(); findSite(); } });

async function loadProcs() {
  $('#procList').replaceChildren(h('div', { class: 'empty small' }, 'Смотрю запущенные программы...'));
  try { addState.procs = await api('/processes'); } catch (e) { addState.procs = []; }
  renderProcs();
}
function renderProcs() {
  const q = $('#appSearch').value.trim().toLowerCase();
  const cur = $('#appInput').value.trim().toLowerCase();
  const list = (addState.procs || []).filter((p) => !q || p.name.toLowerCase().includes(q) || p.path.toLowerCase().includes(q));
  $('#procList').replaceChildren(...(list.length
    ? list.slice(0, 200).map((p) => h('button', {
      class: 'proc' + (p.name.toLowerCase() === cur ? ' on' : ''), title: p.path,
      onclick: () => { $('#appInput').value = p.name; renderProcs(); },
    }, h('b', {}, p.name), h('span', {}, p.path)))
    : [h('div', { class: 'empty small' }, q ? 'Ничего не найдено' : 'Запущенных программ не нашлось')]));
}
$('#appSearch').addEventListener('input', renderProcs);
$('#appInput').addEventListener('input', renderProcs);
$('#appFile').addEventListener('change', (e) => {
  const f = e.target.files[0];
  if (f) $('#appInput').value = f.name;
  e.target.value = '';
  renderProcs();
});

$('#addConfirm').addEventListener('click', async (e) => {
  const btn = e.currentTarget;
  let body;
  if (addState.kind === 'site') {
    const q = $('#siteInput').value.trim();
    if (!q) return setAddResult('Введите адрес сайта', true);
    // не искали или адрес поменяли - ищем и сразу добавляем с найденным
    if (!addState.found || addState.foundFor !== q) {
      if (!(await findSite())) return;
    }
    const f = addState.found;
    const gs = $('#useGeosite');
    const gi = $('#useGeoip');
    body = {
      kind: 'site', value: f.domain, route: addState.route,
      domains: $('#siteExtra').value.split(/[\s,;]+/).filter(Boolean),
      geosite: f.geosite && gs && gs.checked ? f.geosite.name : '',
      geoip: f.geoip && gi && gi.checked ? f.geoip.name : '',
    };
  } else {
    const v = $('#appInput').value.trim();
    if (!v) return setAddResult('Выберите программу из списка или впишите имя .exe', true);
    body = { kind: 'app', value: v, route: addState.route };
  }
  busy(btn, true);
  setAddResult('Проверяю и сохраняю...');
  try {
    const d = await api('/board/items', { method: 'POST', body });
    addModal.close();
    afterDeskChange(d, 'Добавлено ' + (addState.route === 'vpn' ? 'через VPN' : 'напрямую'));
  } catch (err) { setAddResult(err.message, true); }
  busy(btn, false);
});

// ---------- редактор конфига ----------
const ta = $('#cfgText');
const hlEl = $('#hl');
const gutter = $('#gutter');
const layer = $('#layer');
let cfgSaved = '';
let gutterLines = 0;
let errLine = 0;

function hlValue(s) {
  const re = /(\s#.*$|^#.*$)|("(?:[^"\\]|\\.)*"?|'(?:[^']|'')*'?)|([&*][\w@.-]+)|(<<)|([{}\[\],()])|((?<![\w-])(?:true|false|null)(?![\w-]))|((?<![\w.:\/-])-?\d+(?:\.\d+)?(?![\w.:\/-]))|([A-Z][A-Z0-9-]{2,}(?=[,)]|$|\s))/g;
  let out = '', last = 0, m;
  while ((m = re.exec(s))) {
    out += esc(s.slice(last, m.index));
    const cls = m[1] ? 't-com' : m[2] ? 't-str' : m[3] || m[4] ? 't-anc' : m[5] ? 't-punc' : m[6] ? 't-bool' : m[7] ? 't-num' : 't-kw';
    out += '<span class="' + cls + '">' + esc(m[0]) + '</span>';
    last = m.index + m[0].length;
    if (m[1]) break;
  }
  return out + esc(s.slice(last));
}

function hlLine(line) {
  const c = line.match(/^(\s*)(#.*)$/);
  if (c) return esc(c[1]) + '<span class="t-com">' + esc(c[2]) + '</span>';
  const ind = line.match(/^\s*(?:-\s+)*/)[0];
  let out = esc(ind).replace(/-/g, '<span class="t-dash">-</span>');
  let rest = line.slice(ind.length);
  const k = rest.match(/^("[^"]*"|'[^']*'|[^\s#'"{\[\],&*][^#:]*?)(\s*:)(?=\s|$)/);
  if (k) {
    out += '<span class="t-key">' + esc(k[1]) + '</span><span class="t-punc">' + esc(k[2]) + '</span>';
    rest = rest.slice(k[0].length);
  }
  return out + hlValue(rest);
}

const highlightYAML = (text) => text.split('\n').map(hlLine).join('\n');

let rafPending = false;
function renderEditor() {
  // пустое состояние и «не сохранён» - сразу, без ожидания кадра
  $('#editorEmpty').hidden = ta.value.length > 0;
  renderDirty();
  if (rafPending) return;
  rafPending = true;
  requestAnimationFrame(() => {
    rafPending = false;
    const text = ta.value;
    hlEl.innerHTML = highlightYAML(text) + '\n';
    const n = text.split('\n').length;
    if (n !== gutterLines) {
      gutterLines = n;
      gutter.textContent = Array.from({ length: n }, (_, i) => i + 1).join('\n');
    }
    syncScroll();
  });
}

function syncScroll() {
  layer.style.transform = 'translate(' + -ta.scrollLeft + 'px,' + -ta.scrollTop + 'px)';
  gutter.style.transform = 'translateY(' + -ta.scrollTop + 'px)';
}

const cfgDirty = () => ta.value !== cfgSaved;
function renderDirty() {
  const d = cfgDirty();
  $('#cfgDirty').hidden = !d;
  $('#navCfgDot').hidden = !d;
}

// Парсер YAML в ядре для части ошибок называет строку на одну раньше,
// поэтому подсвечиваем две: названную и следующую.
function setErrLine(n) {
  errLine = n;
  const m = $('#errMark');
  m.hidden = !n;
  if (n) m.style.top = 'calc(var(--pad) + ' + (n - 1) + ' * var(--lh))';
}

function gotoLine(n) {
  const lines = ta.value.split('\n');
  let pos = 0;
  for (let i = 0; i < n - 1 && i < lines.length; i++) pos += lines[i].length + 1;
  ta.focus();
  ta.setSelectionRange(pos, pos + (lines[n - 1] || '').length);
  const lh = parseFloat(getComputedStyle($('#editor')).getPropertyValue('--lh')) || 20;
  ta.scrollTop = Math.max(0, (n - 6) * lh);
  syncScroll();
}

// вставка через execCommand: работает Ctrl+Z
function replaceAll(text) {
  ta.focus();
  ta.select();
  if (!document.execCommand('insertText', false, text)) ta.value = text;
  ta.setSelectionRange(0, 0);
  ta.scrollTop = 0;
  ta.scrollLeft = 0;
  setErrLine(0);
  renderEditor();
  schedulePreview();
}

ta.addEventListener('input', () => { if (errLine) setErrLine(0); renderEditor(); schedulePreview(); });
ta.addEventListener('scroll', syncScroll);
ta.addEventListener('keydown', (e) => {
  if (e.key === 'Tab' && !e.ctrlKey && !e.altKey) {
    e.preventDefault();
    document.execCommand('insertText', false, '  ');
  }
});
document.addEventListener('keydown', (e) => {
  if ((e.ctrlKey || e.metaKey) && e.key.toLowerCase() === 's' && page === 'config') {
    e.preventDefault();
    saveConfig(false);
  }
});
window.addEventListener('beforeunload', (e) => {
  if (cfgDirty()) { e.preventDefault(); e.returnValue = ''; }
});

async function loadConfig() {
  try {
    const d = await api('/config');
    cfgSaved = d.text || '';
    ta.value = cfgSaved;
    renderEditor();
    schedulePreview(0);
  } catch (e) { /* оверлей */ }
}

let previewTimer = 0;
function schedulePreview(delay = 500) {
  clearTimeout(previewTimer);
  previewTimer = setTimeout(async () => {
    const list = $('#cfgChanges');
    if (!ta.value.trim()) { list.replaceChildren(h('li', { class: 'muted' }, 'Вставьте конфиг')); return; }
    try {
      const d = await api('/config/preview', { method: 'POST', body: { text: ta.value } });
      if (d.error) list.replaceChildren(h('li', { class: 'err' }, d.error));
      else list.replaceChildren(...d.changes.map((c) => h('li', {}, c)));
    } catch (e) { /* не критично */ }
  }, delay);
}

function showCheck(res, extra) {
  const box = $('#cfgResult');
  box.className = 'result ' + (res.ok ? 'ok' : 'fail');
  const title = res.ok ? (res.partial ? 'Структура в порядке' : 'Конфиг в порядке') : 'Ошибка в конфиге';
  box.replaceChildren(h('b', {}, title + (extra ? ' · ' + extra : '')));
  const out = (res.output || '').trim();
  if (out && out !== 'Конфиг в порядке') box.append(h('span', { class: 'out' }, out));
  if (res.noServers) box.append(h('button', { class: 'link', 'data-goto': 'servers' }, 'Добавить сервер'));
  const m = !res.ok && out.match(/line (\d+)/);
  if (m) {
    const n = +m[1];
    setErrLine(n);
    box.append(h('button', { class: 'link', onclick: () => gotoLine(n) }, 'Перейти к строке ' + n));
  } else setErrLine(0);
  if (res.changes) $('#cfgChanges').replaceChildren(...res.changes.map((c) => h('li', {}, c)));
}

function busy(btn, on) { btn.classList.toggle('busy', on); }

$('#cfgCheck').addEventListener('click', async (e) => {
  if (!ta.value.trim()) return toast('Конфиг пустой', 'warn');
  busy(e.currentTarget, true);
  try { showCheck(await api('/config/check', { method: 'POST', body: { text: ta.value } })); }
  catch (err) { toast(err.message, 'err'); }
  busy(e.currentTarget, false);
});

async function saveConfig(apply) {
  if (!ta.value.trim()) return toast('Конфиг пустой', 'warn');
  const btn = apply ? $('#cfgApply') : $('#cfgSave');
  busy(btn, true);
  try {
    const text = ta.value;
    const d = await api('/config', { method: 'PUT', body: { text, apply } });
    cfgSaved = text;
    renderDirty();
    showCheck(d.check, 'сохранён');
    if (apply && d.applied) toast('Сохранено, переподключаюсь', 'ok');
    else if (apply) toast('Сохранено, но не применено: в конфиге ошибка', 'err');
    else if (d.check.ok) toast('Конфиг сохранён', 'ok');
    else toast('Сохранено, но в конфиге ошибка', 'warn');
  } catch (err) { toast(err.message, 'err'); }
  busy(btn, false);
}
$('#cfgSave').addEventListener('click', () => saveConfig(false));
$('#cfgApply').addEventListener('click', () => saveConfig(true));

$('#cfgPaste').addEventListener('click', async () => {
  try {
    const text = await navigator.clipboard.readText();
    if (!text.trim()) return toast('Буфер обмена пуст', 'warn');
    replaceAll(text);
    toast('Конфиг вставлен. Нажмите «Применить», чтобы подключиться с ним', 'ok');
  } catch (e) {
    ta.focus();
    toast('Нет доступа к буферу: нажмите Ctrl+A, затем Ctrl+V в редакторе', 'warn');
  }
});

function loadFile(file) {
  if (!file) return;
  if (file.size > 8 << 20) return toast('Файл слишком большой', 'err');
  file.text().then((t) => { replaceAll(t); toast('Загружен ' + file.name, 'ok'); });
}
$('#cfgOpen').addEventListener('click', () => $('#cfgFile').click());
$('#cfgFile').addEventListener('change', (e) => { loadFile(e.target.files[0]); e.target.value = ''; });

const editorEl = $('#editor');
editorEl.addEventListener('dragover', (e) => { e.preventDefault(); editorEl.classList.add('drag'); });
editorEl.addEventListener('dragleave', (e) => { if (!editorEl.contains(e.relatedTarget)) editorEl.classList.remove('drag'); });
editorEl.addEventListener('drop', (e) => {
  e.preventDefault();
  editorEl.classList.remove('drag');
  loadFile(e.dataTransfer.files[0]);
});
// файл, брошенный мимо редактора, не должен открываться вместо программы
window.addEventListener('dragover', (e) => e.preventDefault());
window.addEventListener('drop', (e) => e.preventDefault());

$('#cfgPreview').addEventListener('click', async () => {
  if (!ta.value.trim()) return toast('Конфиг пустой', 'warn');
  try {
    const d = await api('/config/preview', { method: 'POST', body: { text: ta.value } });
    if (d.error) return toast(d.error, 'err');
    $('#previewCode').innerHTML = highlightYAML(d.text);
    $('#previewModal').showModal();
  } catch (e) { toast(e.message, 'err'); }
});
$('#previewClose').addEventListener('click', () => $('#previewModal').close());

// ---------- логи ----------
let logSeq = 0;
let logFilter = 'all';
const logView = $('#logView');
const SRC_TEXT = { app: 'программа', core: 'ядро' };
const LVL_TEXT = { debug: 'debug', info: 'info', warning: 'warn', error: 'error', fatal: 'fatal' };

function logMatches(el, q) { return !q || el.textContent.toLowerCase().includes(q); }

async function pollLogs() {
  try {
    const d = await api('/logs?after=' + logSeq);
    if (d.last < logSeq) { logView.replaceChildren(); logSeq = 0; }
    if (d.lines.length) {
      const nearBottom = logView.scrollHeight - logView.scrollTop - logView.clientHeight < 40;
      const q = $('#logSearch').value.trim().toLowerCase();
      const frag = document.createDocumentFragment();
      let hasErr = false;
      for (const l of d.lines) {
        const el = h('div', { class: 'll', 'data-l': l.level },
          h('span', { class: 'lt' }, l.time),
          h('span', { class: 'ls s-' + l.src }, SRC_TEXT[l.src] || l.src),
          h('span', { class: 'lv' }, LVL_TEXT[l.level] || l.level),
          h('span', { class: 'lm' }, l.msg));
        if (!logMatches(el, q)) el.classList.add('nomatch');
        if (l.level === 'error' || l.level === 'fatal') hasErr = true;
        frag.append(el);
      }
      logView.append(frag);
      while (logView.childElementCount > 3000) logView.firstElementChild.remove();
      if (hasErr && page !== 'logs') $('#navLogDot').hidden = false;
      if ($('#logFollow').checked && (page !== 'logs' || nearBottom)) scrollLogs();
    }
    logSeq = d.last;
  } catch (e) { /* оверлей */ }
  schedule('logs', pollLogs, document.hidden ? 20000 : page === 'logs' ? 800 : 2000);
}

function scrollLogs(force) {
  if (force || $('#logFollow').checked) logView.scrollTop = logView.scrollHeight;
}

$$('#logFilter button').forEach((b) => b.addEventListener('click', () => {
  logFilter = b.dataset.f;
  $$('#logFilter button').forEach((x) => x.classList.toggle('on', x === b));
  logView.className = 'logview' + (logFilter === 'all' ? '' : ' f-' + logFilter);
  scrollLogs();
}));
$('#logSearch').addEventListener('input', (e) => {
  const q = e.target.value.trim().toLowerCase();
  for (const el of logView.children) el.classList.toggle('nomatch', !logMatches(el, q));
});
$('#logClear').addEventListener('click', async () => {
  try { await api('/logs/clear', { method: 'POST' }); logView.replaceChildren(); } catch (e) { toast(e.message, 'err'); }
});
$('#logCopy').addEventListener('click', async () => {
  const text = [...logView.children].filter((el) => el.offsetParent !== null)
    .map((el) => [...el.children].map((c) => c.textContent).join('  ')).join('\n');
  try { await navigator.clipboard.writeText(text); toast('Логи скопированы', 'ok'); }
  catch (e) { toast('Не удалось скопировать', 'err'); }
});

async function loadCoreLevel() {
  if (!S || S.status !== 'running') return;
  try {
    const c = await api('/mihomo/configs');
    if (c['log-level']) $('#coreLevel').value = c['log-level'];
  } catch (e) { /* ядро ещё поднимается */ }
}
$('#coreLevel').addEventListener('change', async (e) => {
  try {
    await api('/mihomo/configs', { method: 'PATCH', body: { 'log-level': e.target.value } });
    toast('Уровень логов ядра: ' + e.target.selectedOptions[0].textContent, 'ok');
  } catch (err) { toast(err.message, 'err'); }
});

// ---------- настройки ----------
async function loadSettings() {
  try { fillSettings(await api('/settings')); } catch (e) { /* оверлей */ }
}

function fillSettings(s) {
  $('#setAutostart').checked = s.autostart;
  $('#setConnect').checked = s.connectOnLaunch;
  $('#setStack').value = s.tunStack;
  $('#setStrict').checked = s.strictRoute;
  $('#setRoute').value = s.tunRoute || 'auto';
  $('#setUpdCheck').checked = s.updateCheck !== false;
  $('#setPort').value = s.controllerPort;
}

async function saveSetting(patch, el) {
  if (el) el.disabled = true;
  try {
    const s = await api('/settings', { method: 'PUT', body: patch });
    fillSettings(s);
    if (s.restartRequired) toast('Изменение вступит в силу после переподключения', 'warn', { label: 'Переподключить', fn: () => api('/restart', { method: 'POST' }) });
    else toast('Сохранено', 'ok');
  } catch (e) {
    toast(e.message, 'err');
    loadSettings();
  }
  if (el) el.disabled = false;
}

$('#setAutostart').addEventListener('change', (e) => saveSetting({ autostart: e.target.checked }, e.target));
$('#setConnect').addEventListener('change', (e) => saveSetting({ connectOnLaunch: e.target.checked }, e.target));
$('#setStack').addEventListener('change', (e) => saveSetting({ tunStack: e.target.value }, e.target));
$('#setStrict').addEventListener('change', (e) => saveSetting({ strictRoute: e.target.checked }, e.target));
$('#setRoute').addEventListener('change', (e) => saveSetting({ tunRoute: e.target.value }, e.target));
$('#setUpdCheck').addEventListener('change', (e) => saveSetting({ updateCheck: e.target.checked }, e.target));
$('#setPort').addEventListener('change', (e) => saveSetting({ controllerPort: parseInt(e.target.value, 10) || 0 }, e.target));

function renderCoreBlock() {
  $('#coreVer').textContent = S.core.installed ? 'Установлено ' + (S.core.version || '') : 'Не скачано: скачается при первом подключении';
  const dl = S.download;
  const bar = $('#coreProgress');
  bar.hidden = !dl.active;
  bar.classList.toggle('indet', dl.active && dl.progress < 0);
  bar.firstElementChild.style.width = dl.progress >= 0 ? Math.round(dl.progress * 100) + '%' : '';
  const msg = $('#coreMsg');
  msg.textContent = dl.error ? 'Ошибка: ' + dl.error : dl.message || '';
  msg.style.color = dl.error ? 'var(--err)' : '';
  const b = $('#coreUpdate');
  b.disabled = dl.active;
  b.textContent = S.core.installed ? 'Обновить ядро' : 'Скачать ядро';
}

$('#coreUpdate').addEventListener('click', async () => {
  try { S = await api('/core/update', { method: 'POST' }); renderState(); }
  catch (e) { toast(e.message, 'err'); }
});
$('#openZash').addEventListener('click', async () => {
  try { await api('/open', { method: 'POST', body: { target: 'zashboard' } }); }
  catch (e) { toast(e.message === 'сначала подключитесь' ? 'Панель работает, только когда VPN подключён' : e.message, 'warn'); }
});
$('#openData').addEventListener('click', () => api('/open', { method: 'POST', body: { target: 'data' } }).catch((e) => toast(e.message, 'err')));
$('#quitApp').addEventListener('click', async () => {
  if (!confirm('Отключить VPN и закрыть Mejgorod?')) return;
  try { await api('/quit', { method: 'POST' }); } catch (e) { /* уже закрылась */ }
  showOverlay('Программа закрыта', 'VPN отключён. Это окно можно закрыть.');
});

// ---------- шаблоны ----------
const tplModal = $('#tplModal');
let tplChosen = '';

async function openTemplates() {
  tplChosen = '';
  $('#tplLoad').disabled = true;
  try {
    const d = await api('/templates');
    const list = $('#tplList');
    list.replaceChildren(...d.templates.map((t) => {
      const row = h('button', { class: 'tpl', 'data-id': t.id, onclick: () => {
        tplChosen = t.id;
        $$('.tpl', list).forEach((x) => x.classList.toggle('on', x === row));
        $('#tplLoad').disabled = false;
      }, ondblclick: () => loadTemplate(t.id) },
        h('span', { class: 'radio' }),
        h('div', { class: 'tpl-text' },
          h('b', {}, t.name, h('span', { class: 'badge' }, t.builtin ? 'встроенный' : 'свой')),
          h('span', {}, t.desc)));
      return row;
    }));
    $('#tplDir').textContent = 'Свои шаблоны: положите .yaml в папку ' + d.dir;
    tplModal.showModal();
  } catch (e) { toast(e.message, 'err'); }
}

async function loadTemplate(id) {
  if (!id) return;
  if (ta.value.trim() && !confirm('Заменить текущий текст в редакторе шаблоном?')) return;
  try {
    const d = await api('/template?id=' + encodeURIComponent(id));
    tplModal.close();
    replaceAll(d.text);
    toast('Шаблон загружен. Добавьте сервер на вкладке «Серверы» и нажмите «Применить»', 'ok');
  } catch (e) { toast(e.message, 'err'); }
}

$('#cfgTemplate').addEventListener('click', openTemplates);
$('#emptyTemplate').addEventListener('click', openTemplates);
$('#emptyPaste').addEventListener('click', () => $('#cfgPaste').click());
$('#tplClose').addEventListener('click', () => tplModal.close());
$('#tplLoad').addEventListener('click', () => loadTemplate(tplChosen));

// ---------- серверы ----------
let SRV = null;
let srvAutoPinged = false;
const ICON_COPY = '<rect x="9" y="9" width="12" height="12" rx="2"/><path d="M5 15V5a2 2 0 0 1 2-2h10"/>';
const ICON_EDIT = '<path d="M12 20h9"/><path d="M16.5 3.5a2.1 2.1 0 0 1 3 3L7 19l-4 1 1-4z"/>';
const ICON_TRASH = '<path d="M3 6h18"/><path d="M8 6V4h8v2"/><path d="M19 6l-1 14H6L5 6"/>';

async function loadServers() {
  try {
    SRV = await api('/servers');
    renderServers();
    renderHomeServer();
  } catch (e) { /* оверлей */ }
}

function srvMeta(s) {
  const parts = [s.type || 'vless', s.net, s.sec && s.sec !== 'none' ? s.sec : 'без tls'];
  if (s.host) parts.push(s.host + (s.port ? ':' + s.port : ''));
  return parts.filter(Boolean).join(' · ');
}

const ICON_GEAR = '<circle cx="12" cy="12" r="3"/><path d="M19.4 15a1.7 1.7 0 0 0 .3 1.8l.1.1a2 2 0 1 1-2.8 2.8l-.1-.1a1.7 1.7 0 0 0-1.8-.3 1.7 1.7 0 0 0-1 1.5V21a2 2 0 1 1-4 0v-.1a1.7 1.7 0 0 0-1.1-1.5 1.7 1.7 0 0 0-1.8.3l-.1.1a2 2 0 1 1-2.8-2.8l.1-.1a1.7 1.7 0 0 0 .3-1.8 1.7 1.7 0 0 0-1.5-1H3a2 2 0 1 1 0-4h.1a1.7 1.7 0 0 0 1.5-1.1 1.7 1.7 0 0 0-.3-1.8l-.1-.1a2 2 0 1 1 2.8-2.8l.1.1a1.7 1.7 0 0 0 1.8.3H9a1.7 1.7 0 0 0 1-1.5V3a2 2 0 1 1 4 0v.1a1.7 1.7 0 0 0 1 1.5 1.7 1.7 0 0 0 1.8-.3l.1-.1a2 2 0 1 1 2.8 2.8l-.1.1a1.7 1.7 0 0 0-.3 1.8V9a1.7 1.7 0 0 0 1.5 1H21a2 2 0 1 1 0 4h-.1a1.7 1.7 0 0 0-1.5 1z"/>';

function renderServers() {
  if (!SRV) return;
  const list = SRV.servers;
  const chosen = SRV.running ? (SRV.current || '') : SRV.active;
  const row = (s, own) => {
    const on = s.name === chosen;
    return h('div', { class: 'srv' + (on ? ' on' : '') + (own ? '' : ' readonly') },
      h('button', { class: 'srv-main', onclick: () => chooseServer(s.name) },
        h('span', { class: 'radio' }),
        h('div', { class: 'srv-text' }, h('b', {}, s.name), h('span', {}, srvMeta(s)))),
      on && SRV.running ? h('span', { class: 'badge now' }, 'используется') : null,
      own ? null : h('span', { class: 'badge', title: 'Этот сервер записан в конфиге: меняется на вкладке «Конфиг»' }, 'из конфига'),
      delayEl(s.name),
      own ? h('div', { class: 'srv-actions' },
        h('button', { class: 'icon-btn', title: 'Настроить параметры', onclick: () => editServer(s) }, svg(ICON_GEAR)),
        s.link ? h('button', { class: 'icon-btn', title: 'Скопировать ссылку', onclick: () => copyText(s.link, 'Ссылка скопирована') }, svg(ICON_COPY)) : null,
        h('button', { class: 'icon-btn', title: 'Переименовать', onclick: () => renameServer(s) }, svg(ICON_EDIT)),
        h('button', { class: 'icon-btn danger', title: 'Удалить', onclick: () => deleteServer(s) }, svg(ICON_TRASH))) : null);
  };
  $('#srvList').replaceChildren(...list.map((s) => row(s, true)), ...SRV.config.map((s) => row(s, false)));
  $('#srvEmpty').hidden = list.length + SRV.config.length > 0;
  $('#srvPing').disabled = !SRV.running || !(list.length + SRV.config.length);
  $('#srvNote').textContent = SRV.running && chosen && !list.some((s) => s.name === chosen) && !SRV.config.some((s) => s.name === chosen)
    ? 'Сейчас выбрано: ' + chosen + ' (автовыбор сервера)' : '';

  // один автоматический пинг при открытии вкладки
  if (SRV.running && (list.length || SRV.config.length) && !srvAutoPinged) {
    srvAutoPinged = true;
    pingServers();
  }
}

async function chooseServer(name) {
  try {
    SRV = await api('/servers/active', { method: 'PUT', body: { name } });
    renderServers();
    renderHomeServer();
    loadGroups();
    if (!SRV.running) toast('Сервер «' + name + '» будет использован при подключении', 'ok');
  } catch (e) { toast(e.message, 'err'); }
}

async function renameServer(s) {
  const name = prompt('Новое имя сервера', s.name);
  if (!name || name.trim() === s.name) return;
  try {
    const d = await api('/servers/' + s.id, { method: 'PUT', body: { name: name.trim() } });
    if (d.restarted) toast('Переименовано, переподключаюсь', 'ok');
    loadServers();
  } catch (e) { toast(e.message, 'err'); }
}

async function deleteServer(s) {
  if (!confirm('Удалить сервер «' + s.name + '»?')) return;
  try {
    const d = await api('/servers/' + s.id, { method: 'DELETE' });
    toast(d.restarted ? 'Удалено, переподключаюсь' : 'Удалено', 'ok');
    loadServers();
  } catch (e) { toast(e.message, 'err'); }
}

async function copyText(text, msg) {
  try { await navigator.clipboard.writeText(text); toast(msg, 'ok'); }
  catch (e) { toast('Не удалось скопировать', 'err'); }
}

async function pingServers() {
  if (!SRV || !SRV.running) return;
  const btn = $('#srvPing');
  btn.classList.add('busy');
  await Promise.all([...SRV.servers, ...SRV.config].map(async (s) => {
    try {
      const d = await api('/mihomo/proxies/' + encodeURIComponent(s.name) + '/delay?url=' + encodeURIComponent(PING_URL) + '&timeout=6000');
      delays.set(s.name, d.delay || 0);
    } catch (err) { delays.set(s.name, 0); }
  }));
  btn.classList.remove('busy');
  renderServers();
}
$('#srvPing').addEventListener('click', pingServers);

const srvModal = $('#srvModal');

// ---------- глубокая настройка сервера ----------
const srvEditModal = $('#srvEditModal');
let srvEditID = null;
async function editServer(s) {
  srvEditID = s.id;
  $('#srvEditTitle').textContent = 'Настройка: ' + s.name;
  $('#srvEditResult').replaceChildren();
  $('#srvEditResult').className = 'result';
  try {
    const d = await api('/servers/' + encodeURIComponent(s.id) + '/yaml');
    $('#srvEditYAML').value = d.yaml;
    $('#srvEditNote').textContent = 'Параметры сервера в формате mihomo: адрес, порт, uuid, SNI, fingerprint, flow, транспорт и т.д. Имя меняется кнопкой «Переименовать».' +
      (d.fromLink ? ' После сохранения ссылка vless:// у сервера пропадёт: она бы уже не совпадала с параметрами.' : '');
    srvEditModal.showModal();
  } catch (e) { toast(e.message, 'err'); }
}
$('#srvEditClose').addEventListener('click', () => srvEditModal.close());
$('#srvEditYAML').addEventListener('keydown', (e) => {
  if (e.key !== 'Tab') return;
  e.preventDefault();
  const t = e.target;
  t.setRangeText('  ', t.selectionStart, t.selectionEnd, 'end');
});
$('#srvEditSave').addEventListener('click', async (e) => {
  const btn = e.currentTarget;
  busy(btn, true);
  const res = $('#srvEditResult');
  res.className = 'result';
  res.textContent = 'Проверяю ядром...';
  try {
    const d = await api('/servers/' + encodeURIComponent(srvEditID) + '/yaml', { method: 'PUT', body: { yaml: $('#srvEditYAML').value } });
    srvEditModal.close();
    toast(d.restarted ? 'Сохранено, переподключаюсь' : 'Сохранено', 'ok');
    loadServers();
  } catch (err) {
    res.className = 'result fail';
    res.textContent = err.message;
  }
  busy(btn, false);
});

// ---------- главная: сервер и профиль ----------
let PROF = null;
const CHOICE_TEXT = { Fallback: 'Fallback (авто: первый рабочий)', Fastest: 'Fastest (авто: самый быстрый)' };

function renderHomeServer() {
  const sel = $('#homeServer');
  if (!SRV || document.activeElement === sel) return; // не мешаем, пока меню открыто
  const cur = SRV.running ? (SRV.current || SRV.active) : SRV.active;
  const names = SRV.choices && SRV.choices.length ? SRV.choices : [...SRV.servers, ...SRV.config].map((s) => s.name);
  const opts = names.map((n) => h('option', { value: n, selected: n === cur }, CHOICE_TEXT[n] || n));
  if (cur && !names.includes(cur)) opts.unshift(h('option', { value: cur, selected: true }, cur));
  if (!names.length && !cur) opts.push(h('option', { value: '', selected: true, disabled: true }, 'Серверов пока нет'));
  opts.push(h('option', { value: '__add' }, '+ Добавить сервер...'));
  sel.replaceChildren(...opts);
  sel.dataset.cur = cur || '';
}

$('#homeServer').addEventListener('change', (e) => {
  const v = e.target.value;
  if (v === '__add') {
    e.target.value = e.target.dataset.cur;
    e.target.blur();
    openImport();
    return;
  }
  e.target.blur();
  chooseServer(v).then(() => {
    toast(S && S.status === 'running' ? 'Сервер: ' + v : 'Сервер «' + v + '» будет использован при подключении', 'ok');
  });
});

async function loadProfiles() {
  try { PROF = await api('/profiles'); } catch (e) { PROF = null; }
  renderHomeProfile();
  renderProfileList();
}

function renderHomeProfile() {
  const sel = $('#homeProfile');
  if (!PROF || document.activeElement === sel) return;
  const kids = [];
  if (!PROF.active) kids.push(h('option', { value: '', selected: true, disabled: true }, 'Профиль не выбран'));
  if (PROF.profiles.length) {
    kids.push(h('optgroup', { label: 'Профили' }, ...PROF.profiles.map((p) => h('option', { value: 'p:' + p.name, selected: p.active }, p.name))));
  }
  if (PROF.templates.length) {
    kids.push(h('optgroup', { label: 'Новый из шаблона' }, ...PROF.templates.map((t) => h('option', { value: 't:' + t.id }, t.name))));
  }
  kids.push(h('option', { value: '__manage' }, 'Управление профилями...'));
  sel.replaceChildren(...kids);
  sel.dataset.cur = PROF.active ? 'p:' + PROF.active : '';
  $('#cfgProfileName').textContent = PROF.active ? '· ' + PROF.active : '';
}

async function switchProfile(body) {
  if (cfgDirty() && !confirm('В редакторе конфига есть несохранённые изменения, они пропадут. Переключить профиль?')) {
    renderHomeProfile();
    return;
  }
  try {
    PROF = await api('/profiles/activate', { method: 'POST', body });
    renderHomeProfile();
    renderProfileList();
    await loadConfig();
    loadServers();
    if (page === 'groups') loadBoard();
    toast('Профиль: ' + PROF.active + (S && (S.status === 'running' || S.status === 'starting') ? '. Переподключаюсь' : ''), 'ok');
  } catch (e) { toast(e.message, 'err'); renderHomeProfile(); }
}

$('#homeProfile').addEventListener('change', (e) => {
  const v = e.target.value;
  e.target.blur();
  if (v === '__manage') {
    e.target.value = e.target.dataset.cur;
    go('config');
    openProfiles();
  } else if (v.startsWith('p:')) switchProfile({ name: v.slice(2) });
  else if (v.startsWith('t:')) switchProfile({ template: v.slice(2) });
});

// ---------- управление профилями ----------
const profModal = $('#profModal');
function openProfiles() {
  $('#profNewName').value = '';
  loadProfiles();
  profModal.showModal();
}
$('#cfgProfiles').addEventListener('click', openProfiles);
$('#profClose').addEventListener('click', () => profModal.close());

function renderProfileList() {
  if (!PROF) return;
  $('#profList').replaceChildren(...(PROF.profiles.length ? PROF.profiles.map((p) =>
    h('div', { class: 'prof' + (p.active ? ' on' : '') },
      h('button', { class: 'prof-main', onclick: () => { if (!p.active) switchProfile({ name: p.name }); } },
        h('span', { class: 'radio' }), h('b', {}, p.name)),
      p.active ? h('span', { class: 'badge' }, 'активный') : null,
      h('button', { class: 'icon-btn', title: 'Переименовать', onclick: () => renameProfile(p.name) }, svg(ICON_EDIT)),
      h('button', { class: 'icon-btn danger', title: 'Удалить', onclick: () => deleteProfile(p.name) }, svg(ICON_TRASH))))
    : [h('div', { class: 'empty small' }, 'Профилей пока нет: выберите шаблон на главной или создайте профиль ниже')]));
}

async function renameProfile(name) {
  const n = prompt('Новое имя профиля', name);
  if (!n || n.trim() === name) return;
  try { PROF = await api('/profiles/' + encodeURIComponent(name), { method: 'PUT', body: { name: n.trim() } }); renderHomeProfile(); renderProfileList(); }
  catch (e) { toast(e.message, 'err'); }
}

async function deleteProfile(name) {
  if (!confirm('Удалить профиль «' + name + '»? Файл останется в папке data\\profiles с пометкой .deleted.')) return;
  try {
    const wasActive = PROF && PROF.active === name;
    PROF = await api('/profiles/' + encodeURIComponent(name), { method: 'DELETE' });
    renderHomeProfile();
    renderProfileList();
    if (wasActive) { loadConfig(); loadServers(); }
  } catch (e) { toast(e.message, 'err'); }
}

async function createProfile(copy) {
  const name = $('#profNewName').value.trim();
  if (!name) return toast('Введите название профиля', 'warn');
  if (cfgDirty() && !confirm('В редакторе конфига есть несохранённые изменения, они пропадут. Продолжить?')) return;
  try {
    PROF = await api('/profiles', { method: 'POST', body: { name, copy } });
    $('#profNewName').value = '';
    renderHomeProfile();
    renderProfileList();
    await loadConfig();
    loadServers();
    toast('Профиль «' + name + '» создан и выбран', 'ok');
  } catch (e) { toast(e.message, 'err'); }
}
$('#profCopy').addEventListener('click', () => createProfile(true));
$('#profEmpty').addEventListener('click', () => createProfile(false));
function srvImportTab(kind) {
  $$('#srvImportKind button').forEach((b) => b.classList.toggle('on', b.dataset.k === kind));
  $('#srvPaneLink').hidden = kind !== 'link';
  $('#srvPaneSub').hidden = kind !== 'sub';
  (kind === 'sub' ? $('#subUrl') : $('#srvInput')).focus();
}
$$('#srvImportKind button').forEach((b) => b.addEventListener('click', () => srvImportTab(b.dataset.k)));

async function openImport() {
  $('#srvAddResult').replaceChildren();
  $('#srvAddResult').className = 'result';
  $('#subFindResult').replaceChildren();
  $('#subFindResult').className = 'result';
  $('#subListWrap').hidden = true;
  srvImportTab('link');
  srvModal.showModal();
  const inp = $('#srvInput');
  inp.focus();
  // ссылка уже в буфере - подставим сами
  if (!inp.value.trim()) {
    try {
      const clip = (await navigator.clipboard.readText()).trim();
      if (/^vless:\/\//i.test(clip)) inp.value = clip;
    } catch (e) { /* нет доступа к буферу - вставят вручную */ }
  }
}
$('#srvAdd').addEventListener('click', openImport);
$('#srvEmptyAdd').addEventListener('click', openImport);
$('#srvModalClose').addEventListener('click', () => srvModal.close());
$('#srvPasteBtn').addEventListener('click', async () => {
  try { $('#srvInput').value = await navigator.clipboard.readText(); }
  catch (e) { toast('Нет доступа к буферу: нажмите Ctrl+V в поле', 'warn'); $('#srvInput').focus(); }
});
$('#srvAddConfirm').addEventListener('click', async (e) => {
  const text = $('#srvInput').value.trim();
  if (!text) return toast('Вставьте ссылку', 'warn');
  const btn = e.currentTarget;
  busy(btn, true);
  try {
    const d = await api('/servers', { method: 'POST', body: { text } });
    d.errors = d.errors || [];
    d.added = d.added || [];
    const box = $('#srvAddResult');
    box.className = 'result ' + (d.errors.length ? 'fail' : 'ok');
    box.replaceChildren();
    if (d.added.length) box.append(h('div', { class: 'ok' }, 'Добавлено: ' + d.added.join(', ')));
    for (const er of d.errors) box.append(h('div', {}, er));
    if (d.added.length) {
      $('#srvInput').value = '';
      toast('Импортировано: ' + d.added.join(', ') + (d.restarted ? '. Переподключаюсь' : ''), 'ok');
      srvAutoPinged = false;
      loadServers();
      if (!d.errors.length) setTimeout(() => srvModal.close(), 500);
    }
  } catch (err) { toast(err.message, 'err'); }
  busy(btn, false);
});

// ---------- импорт подписки ----------
let subFound = null; // последний результат /subscription/preview

function subRow(s, i) {
  return h('label', { class: 'sub-row' },
    h('input', { type: 'checkbox', class: 'subChk', 'data-i': i, checked: true }),
    h('div', { class: 'btext' }, h('b', {}, s.name), h('span', {}, srvMeta(s))));
}

$('#subFind').addEventListener('click', async (e) => {
  const url = $('#subUrl').value.trim();
  if (!url) return toast('Вставьте ссылку на подписку', 'warn');
  const btn = e.currentTarget;
  busy(btn, true);
  const res = $('#subFindResult');
  res.className = 'result';
  res.replaceChildren();
  $('#subListWrap').hidden = true;
  try {
    const d = await api('/servers/subscription/preview', { method: 'POST', body: { url } });
    subFound = d.servers;
    $('#subList').replaceChildren(...subFound.map(subRow));
    $('#subAll').checked = true;
    $('#subCount').textContent = 'Найдено: ' + subFound.length;
    $('#subListWrap').hidden = false;
    $('#subImportResult').replaceChildren();
    $('#subImportResult').className = 'result';
    if (d.errors && d.errors.length) { res.className = 'result fail'; res.replaceChildren(...d.errors.map((er) => h('div', {}, er))); }
  } catch (err) {
    subFound = null;
    res.className = 'result fail';
    res.textContent = err.message;
  }
  busy(btn, false);
});

$('#subAll').addEventListener('change', (e) => {
  $$('.subChk').forEach((c) => { c.checked = e.target.checked; });
});

$('#subImportConfirm').addEventListener('click', async (e) => {
  if (!subFound || !subFound.length) return;
  const items = [...document.querySelectorAll('.subChk')].filter((c) => c.checked).map((c) => subFound[+c.dataset.i]);
  if (!items.length) return toast('Отметьте хотя бы один сервер', 'warn');
  const btn = e.currentTarget;
  busy(btn, true);
  try {
    const d = await api('/servers/subscription/import', { method: 'POST', body: { items } });
    d.errors = d.errors || [];
    d.added = d.added || [];
    const box = $('#subImportResult');
    box.className = 'result ' + (d.errors.length ? 'fail' : 'ok');
    box.replaceChildren();
    if (d.added.length) box.append(h('div', { class: 'ok' }, 'Добавлено: ' + d.added.join(', ')));
    for (const er of d.errors) box.append(h('div', {}, er));
    if (d.added.length) {
      toast('Импортировано: ' + d.added.join(', ') + (d.restarted ? '. Переподключаюсь' : ''), 'ok');
      srvAutoPinged = false;
      loadServers();
      if (!d.errors.length) setTimeout(() => srvModal.close(), 700);
    }
  } catch (err) { toast(err.message, 'err'); }
  busy(btn, false);
});

// ---------- старт ----------
setInterval(() => { if (!document.hidden && S && S.status === 'running') $('#heroSub').textContent = 'В сети ' + fmtDuration(Date.now() - S.startedAt); }, 1000);
pollState();
pollLogs();
loadConfig();
loadServers();
loadProfiles();
