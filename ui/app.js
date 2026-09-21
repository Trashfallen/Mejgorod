'use strict';

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
const ICON_BOLT = '<path d="M13 2 3 14h9l-1 8 10-12h-9l1-8z"/>';

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
    if (++failures >= 3) showOverlay('Программа закрыта', 'MihomoDesk больше не отвечает. Запустите её снова.');
    throw new Error('Программа не отвечает');
  }
  if (failures) { failures = 0; if ($('#overlayTitle').textContent === 'Программа закрыта') $('#overlay').hidden = true; }
  if (r.status === 401) {
    showOverlay('Окно устарело', 'Откройте MihomoDesk заново из значка в трее.');
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
  if (p === 'groups' || p === 'home') loadGroups();
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

async function pollState() {
  try {
    S = await api('/state');
    renderState();
  } catch (e) { /* покажет оверлей */ }
  const fast = S && (S.status === 'starting' || S.status === 'stopping' || S.download.active);
  setTimeout(pollState, fast ? 400 : 1000);
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

  const hs = $('#heroServer');
  hs.hidden = !S.server;
  $('#heroServerName').textContent = S.server ? (st === 'running' ? 'Сервер: ' : 'Будет выбран: ') + S.server : '';

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

  if (st !== prevStatus) {
    if (st === 'running' || prevStatus === 'running' || prevStatus === '') loadGroups();
    if (page === 'servers') loadServers();
    if (st === 'error' && page !== 'logs') $('#navLogDot').hidden = false;
    $('#coreLevel').disabled = st !== 'running';
    if (st === 'running' && page === 'logs') loadCoreLevel();
    prevStatus = st;
  }
}

function renderNotice() {
  const n = $('#homeNotice');
  const key = [S.elevated, S.config.exists].join();
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
  n.hidden = !n.childElementCount;
}

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
    renderGroups();
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
  renderGroups();
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

const TYPE_TEXT = { Selector: 'выбор', URLTest: 'авто: самый быстрый', Fallback: 'авто: резерв', LoadBalance: 'балансировка', Relay: 'цепочка' };

function renderGroups() {
  const list = $('#groupsList');
  const quick = $('#quickGroups');
  const on = !!(groupsData && groupsData.groups.length);
  $('#groupsEmpty').hidden = on;
  $('#quickEmpty').hidden = on;
  $('#pingAll').disabled = !on;
  if (!on) { list.replaceChildren(); quick.replaceChildren(); return; }

  const q = $('#groupSearch').value.trim().toLowerCase();
  const cards = [];
  for (const g of groupsData.groups) {
    if (q && !g.name.toLowerCase().includes(q) && !g.all.some((n) => n.toLowerCase().includes(q))) continue;
    const selectable = g.type === 'Selector';
    const opts = g.all.map((name) =>
      h('button', {
        class: 'opt' + (name === g.now ? ' on' : ''),
        title: name,
        disabled: !selectable,
        onclick: () => selectProxy(g.name, name),
      }, h('span', { class: 'oname' }, name), delayEl(name)));
    const pingBtn = h('button', { class: 'icon-btn', title: 'Проверить пинг', onclick: (e) => pingGroup(g.name, e.currentTarget) }, svg(ICON_BOLT));
    cards.push(h('div', { class: 'group' },
      h('div', { class: 'ghead' }, groupIcon(g),
        h('div', { class: 'gtitle' }, h('b', {}, g.name), h('span', {}, (TYPE_TEXT[g.type] || g.type) + ' · ' + (g.now || '-'))),
        pingBtn),
      h('div', { class: 'opts' }, opts)));
  }
  list.replaceChildren(...cards);

  const rows = groupsData.groups.filter((g) => g.type === 'Selector').map((g) => {
    const sel = h('select', { title: g.name, onchange: (e) => selectProxy(g.name, e.target.value) },
      g.all.map((n) => h('option', { value: n, selected: n === g.now }, n)));
    return h('div', { class: 'qrow' }, groupIcon(g), h('span', { class: 'gname', title: g.name }, g.name), sel);
  });
  quick.replaceChildren(...rows);
}
$('#groupSearch').addEventListener('input', renderGroups);

async function selectProxy(group, name) {
  try {
    await api('/mihomo/proxies/' + encodeURIComponent(group), { method: 'PUT', body: { name } });
    await loadGroups();
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
  renderGroups();
}

$('#pingAll').addEventListener('click', async (e) => {
  const btn = e.currentTarget;
  if (!groupsData) return;
  btn.classList.add('busy');
  delays.clear();
  // все группы разом: ядро само проверит каждый сервер один раз
  await Promise.all(groupsData.groups.map((g) => pingGroup(g.name)));
  btn.classList.remove('busy');
  await loadGroups();
});

setInterval(() => {
  if (page === 'groups' || page === 'home') loadGroups();
  if (page === 'servers') loadServers();
}, 5000);

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
  setTimeout(pollLogs, page === 'logs' ? 800 : 2000);
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
  if (!confirm('Отключить VPN и закрыть MihomoDesk?')) return;
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
  } catch (e) { /* оверлей */ }
}

function srvMeta(s) {
  const parts = [s.type || 'vless', s.net, s.sec && s.sec !== 'none' ? s.sec : 'без tls'];
  if (s.host) parts.push(s.host + (s.port ? ':' + s.port : ''));
  return parts.filter(Boolean).join(' · ');
}

function renderServers() {
  if (!SRV) return;
  const list = SRV.servers;
  const chosen = SRV.running ? (SRV.current || '') : SRV.active;
  const rows = list.map((s) => {
    const on = s.name === chosen;
    return h('div', { class: 'srv' + (on ? ' on' : '') },
      h('button', { class: 'srv-main', onclick: () => chooseServer(s.name) },
        h('span', { class: 'radio' }),
        h('div', { class: 'srv-text' }, h('b', {}, s.name), h('span', {}, srvMeta(s)))),
      on && SRV.running ? h('span', { class: 'badge now' }, 'используется') : null,
      delayEl(s.name),
      h('div', { class: 'srv-actions' },
        s.link ? h('button', { class: 'icon-btn', title: 'Скопировать ссылку', onclick: () => copyText(s.link, 'Ссылка скопирована') }, svg(ICON_COPY)) : null,
        h('button', { class: 'icon-btn', title: 'Переименовать', onclick: () => renameServer(s) }, svg(ICON_EDIT)),
        h('button', { class: 'icon-btn danger', title: 'Удалить', onclick: () => deleteServer(s) }, svg(ICON_TRASH))));
  });
  $('#srvList').replaceChildren(...rows);
  $('#srvEmpty').hidden = list.length > 0;
  $('#srvPing').disabled = !SRV.running || !list.length;

  const note = $('#srvNote');
  const cfgNames = SRV.config.map((s) => s.name);
  if (cfgNames.length && !list.some((s) => s.name === chosen)) {
    note.textContent = 'Сейчас работает сервер из конфига: ' + cfgNames.join(', ') + '.' +
      (list.length ? ' Нажмите на импортированный, чтобы переключиться на него.' : '');
  } else {
    note.textContent = '';
  }

  // один автоматический пинг при открытии вкладки
  if (SRV.running && list.length && !srvAutoPinged) {
    srvAutoPinged = true;
    pingServers();
  }
}

async function chooseServer(name) {
  try {
    SRV = await api('/servers/active', { method: 'PUT', body: { name } });
    renderServers();
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
  await Promise.all(SRV.servers.map(async (s) => {
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
async function openImport() {
  $('#srvAddResult').replaceChildren();
  $('#srvAddResult').className = 'result';
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

// ---------- старт ----------
setInterval(() => { if (S && S.status === 'running') $('#heroSub').textContent = 'В сети ' + fmtDuration(Date.now() - S.startedAt); }, 1000);
pollState();
pollLogs();
loadConfig();
