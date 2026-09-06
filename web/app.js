(function () {
  'use strict';
  var $ = function (id) { return document.getElementById(id); };
  var S = {};            // последний снимок состояния
  var seq = 0, pending = {};

  var AL = window.AL = {
    call: function (method, args) {
      return new Promise(function (res, rej) {
        var id = ++seq;
        pending[id] = { res: res, rej: rej };
        try { window.chrome.webview.postMessage(JSON.stringify({ id: id, method: method, args: args || {} })); }
        catch (e) { delete pending[id]; rej(new Error('нет моста')); }
      });
    },
    _reply: function (id, result, err) {
      var p = pending[id]; if (!p) return; delete pending[id];
      if (err) p.rej(new Error(err)); else p.res(result);
    },
    _event: function (name, payload) {
      var h = handlers[name]; if (h) h(payload);
    }
  };
  var handlers = {};
  function on(name, fn) { handlers[name] = fn; }

  function fmtDur(s) {
    s = Math.max(0, Math.floor(s || 0));
    var h = Math.floor(s / 3600), m = Math.floor(s % 3600 / 60), sec = s % 60;
    return (h < 10 ? '0' : '') + h + ':' + (m < 10 ? '0' : '') + m + ':' + (sec < 10 ? '0' : '') + sec;
  }
  function esc(s) { return String(s == null ? '' : s).replace(/[&<>"]/g, function (c) { return { '&': '&amp;', '<': '&lt;', '>': '&gt;', '"': '&quot;' }[c]; }); }
  function show(el, v) { el.classList.toggle('hidden', !v); }

  // Таймер лекции крутим на странице: событий состояния мало, а секунды должны
  // идти ровно.
  var timerBase = 0, timerAt = 0, timerOn = false;
  function paintTimer() {
    if (!timerOn) return;
    $('pill-timer').textContent = fmtDur(timerBase + (Date.now() - timerAt) / 1000);
  }
  setInterval(paintTimer, 1000);

  // Свой выпадающий список: системный <select> рисует Windows, и он выбивается
  // из оформления. Значение живёт в data-value, наружу летит обычный change.
  function selectValue(root, v) {
    if (v === undefined) return root.dataset.value || '';
    var opt = root.querySelector('.select-opt[data-value="' + v + '"]') || root.querySelector('.select-opt');
    root.dataset.value = opt.dataset.value;
    root.querySelector('.select-label').textContent = opt.textContent;
    root.querySelectorAll('.select-opt').forEach(function (o) { o.classList.toggle('active', o === opt); });
    return root.dataset.value;
  }
  function closeSelects(except) {
    document.querySelectorAll('.select.open').forEach(function (r) {
      if (r === except) return;
      r.classList.remove('open');
      r.querySelector('.select-list').classList.add('hidden');
    });
  }
  function setupSelect(root) {
    var list = root.querySelector('.select-list');
    root.querySelector('.select-btn').onclick = function (e) {
      e.stopPropagation();
      var opening = !root.classList.contains('open');
      closeSelects(root);
      root.classList.toggle('open', opening);
      list.classList.toggle('hidden', !opening);
    };
    root.querySelectorAll('.select-opt').forEach(function (o) {
      o.onclick = function () {
        selectValue(root, o.dataset.value);
        closeSelects();
        root.dispatchEvent(new Event('change', { bubbles: true }));
      };
    });
  }
  document.querySelectorAll('.select').forEach(setupSelect);
  document.addEventListener('mousedown', function (e) { if (!e.target.closest('.select')) closeSelects(); });

  var currentTab = 0;
  function showTab(i) {
    currentTab = i;
    document.querySelectorAll('#tabs .tab').forEach(function (b) { b.classList.toggle('active', +b.dataset.tab === i); });
    for (var k = 0; k < 3; k++) show($('page-' + k), k === i);
    AL.call('tab', { index: i });
    if (i === 0) sendPreviewRect(true);
  }
  document.querySelectorAll('#tabs .tab').forEach(function (b) { b.addEventListener('click', function () { showTab(+b.dataset.tab); }); });

  function dragStart(e) {
    if (e.button !== 0) return;
    if (e.target.closest('button, input, .tabs')) return;
    if (e.detail === 2) { AL.call('window', { cmd: 'maximize' }); return; }
    AL.call('window', { cmd: 'drag' });
  }
  $('drag-zone').addEventListener('mousedown', dragStart);
  $('drag-zone2').addEventListener('mousedown', dragStart);
  $('btn-min').onclick = function () { AL.call('window', { cmd: 'minimize' }); };
  $('btn-tray').onclick = function () { AL.call('window', { cmd: 'tray' }); };
  $('btn-close').onclick = function () { AL.call('window', { cmd: 'close' }); };

  // Каждый вызов двигает нативное окно трансляции, поэтому шлём только
  // изменившийся прямоугольник — иначе окно моргает на каждом обновлении статуса.
  var lastRect = '';
  function sendPreviewRect(force) {
    var r = $('previewer').getBoundingClientRect(), dpr = window.devicePixelRatio || 1;
    var key = [r.left, r.top, r.width, r.height, dpr].join(':');
    if (key === lastRect && force !== true) return;
    lastRect = key;
    AL.call('previewRect', { x: r.left, y: r.top, w: r.width, h: r.height, dpr: dpr });
  }
  window.addEventListener('resize', function () { sendPreviewRect(); });

  var volOpen = false;
  $('btn-volume').onclick = function (e) { e.stopPropagation(); volOpen = !volOpen; $('volume-pop').classList.toggle('open', volOpen); show($('volume-pop'), true); };
  document.addEventListener('mousedown', function (e) { if (volOpen && !e.target.closest('.volume-wrap')) { volOpen = false; $('volume-pop').classList.remove('open'); } });
  var lastNonZero = 100;
  function applyVolumeUI(v) {
    $('volume').value = v; $('volume-val').textContent = v;
    var muted = v === 0;
    $('btn-volume').firstElementChild.className = 'ico ' + (muted ? 'ico-volume-muted' : 'ico-volume');
    $('btn-mute').firstElementChild.className = 'ico ' + (muted ? 'ico-volume-muted' : 'ico-volume');
    if (v > 0) lastNonZero = v;
  }
  $('volume').addEventListener('input', function () { var v = +this.value; applyVolumeUI(v); AL.call('setVolume', { volume: v }); });
  $('btn-mute').onclick = function () { var v = +$('volume').value === 0 ? lastNonZero : 0; applyVolumeUI(v); AL.call('setVolume', { volume: v }); };

  // очистку надо сообщить в Go
  $('url-clear').onclick = function () { $('url').value = ''; AL.call('setUrl', { url: '' }); $('url').focus(); };
  $('url').addEventListener('keydown', function (e) { if (e.key === 'Enter') AL.call('submitUrl', { url: this.value.trim() }); });
  $('url').addEventListener('change', function () { AL.call('setUrl', { url: this.value.trim() }); });
  $('btn-start').onclick = function () { AL.call(S.globalSession ? 'stop' : 'start', { url: $('url').value.trim() }); };
  $('btn-watch').onclick = function () { AL.call('watch'); };
  $('btn-eco').onclick = function () { AL.call('eco', { on: !(S.eco && S.eco.manual) }); };
  $('mail-toggle').addEventListener('change', function () { AL.call('mailToggle', { on: this.checked }); });
  $('btn-telegram').onclick = function () { openDialog('telegram'); telegramFlow(); };
  $('btn-esco').onclick = function () { AL.call(S.esco && S.esco.loginActive ? 'escoEnd' : 'escoBegin'); };
  $('btn-mail').onclick = function () { openDialog('mail'); loadMailForm(); };
  $('btn-account').onclick = function () { openDialog('account'); };
  $('btn-settings').onclick = function () { openDialog('settings'); fillSettings(); };
  $('btn-update').onclick = function () { AL.call('update'); };
  $('btn-nick-save').onclick = saveNickname;
  $('nick-input').addEventListener('keydown', function (e) { if (e.key === 'Enter') saveNickname(); });

  // Имя участника: форма входа на трансляцию не пускает без него.
  var nickAsked = false;
  function nickErr(t) { $('nick-err').textContent = t; show($('nick-err'), !!t); }
  function askNickname() {
    openDialog('nickname');
    nickErr('');
    $('nick-input').value = (S.settings || {}).nickname || '';
    setTimeout(function () { $('nick-input').focus(); }, 50);
  }
  function saveNickname() {
    var v = $('nick-input').value.trim();
    if (v.length < 2) return nickErr('Впишите имя, под которым вас увидят на трансляции');
    $('btn-nick-save').disabled = true;
    AL.call('setNickname', { nickname: v })
      .then(function () { closeDialog(); })
      .catch(function (e) { nickErr(e.message); })
      .finally(function () { $('btn-nick-save').disabled = false; });
  }

  var dialog = null;
  function openDialog(name) {
    dialog = name;
    ['account', 'settings', 'nickname', 'mail', 'telegram'].forEach(function (n) { show($('dlg-' + n), n === name); });
    show($('overlay'), true);
    AL.call('overlay', { open: true });
  }
  function closeDialog() {
    if (!dialog) return;
    if (dialog === 'settings') saveSettings();
    if (dialog === 'telegram') clearInterval(tgPoll);
    dialog = null;
    show($('overlay'), false);
    AL.call('overlay', { open: false });
  }
  document.querySelectorAll('[data-close]').forEach(function (b) { b.onclick = closeDialog; });
  $('overlay').addEventListener('mousedown', function (e) { if (e.target === this) closeDialog(); });
  document.addEventListener('keydown', function (e) {
    if (e.key === 'Escape') { closeSelects(); closeDialog(); }
    if (e.ctrlKey && e.shiftKey && e.code === 'KeyC') AL.call('copyLog');
  });

  // аккаунт
  var registerMode = false;
  $('btn-switch').onclick = function () {
    registerMode = !registerMode;
    show($('acc-pass2'), registerMode); show($('btn-signup'), registerMode); show($('btn-signin'), !registerMode);
    $('btn-switch').textContent = registerMode ? 'Уже есть аккаунт? Войти' : 'Нет аккаунта? Зарегистрироваться';
    setAccErr('');
  };
  function setAccErr(t) { $('acc-err').textContent = t; show($('acc-err'), !!t); }
  function attempt() {
    var login = $('acc-login').value.trim(), pass = $('acc-pass').value;
    if (login.length < 3) return setAccErr('Логин должен содержать не менее 3 символов');
    if (pass.length < 6) return setAccErr('Пароль должен содержать не менее 6 символов');
    if (registerMode && pass !== $('acc-pass2').value) return setAccErr('Пароли не совпадают');
    setAccErr('');
    $('btn-signin').disabled = $('btn-signup').disabled = true;
    AL.call(registerMode ? 'register' : 'login', { login: login, password: pass })
      .then(function () { $('acc-pass').value = $('acc-pass2').value = ''; closeDialog(); })
      .catch(function (e) { setAccErr(e.message); })
      .finally(function () { $('btn-signin').disabled = $('btn-signup').disabled = false; });
  }
  $('btn-signin').onclick = attempt; $('btn-signup').onclick = attempt;
  $('acc-pass').addEventListener('keydown', function (e) { if (e.key === 'Enter') attempt(); });
  $('btn-guest').onclick = function () { AL.call('guest'); closeDialog(); };
  $('btn-signout').onclick = function () { AL.call('logout'); };

  // админские действия
  function adminErr(t) { $('admin-err').textContent = t; show($('admin-err'), !!t); }
  var resetArmed = false;
  function armReset(on) {
    resetArmed = on;
    $('btn-reset').disabled = false;
    $('btn-reset').textContent = on ? 'Точно? Нажмите ещё раз' : 'Снести все данные и перезапустить';
    $('btn-reset').classList.toggle('armed', on);
  }
  $('btn-reset').onclick = function () {
    if (!resetArmed) return armReset(true);
    armReset(false);
    $('btn-reset').disabled = true;
    adminErr('');
    AL.call('adminReset').catch(function (e) { $('btn-reset').disabled = false; adminErr(e.message); });
  };
  $('btn-onboard').onclick = function () {
    adminErr('');
    onboardingStarted = true;   // иначе обучение стартует ещё и из обработчика состояния
    AL.call('adminOnboarding')
      .then(function () { closeDialog(); startOnboarding(); })
      .catch(function (e) { adminErr(e.message); });
  };

  // Сохраняем только то, что реально показали в форме: иначе закрытие диалога
  // затирало бы имя и адрес сервера пустыми значениями.
  var settingsLoaded = false;
  function fillSettings() {
    var s = S.settings || {};
    $('set-nick').value = s.nickname || ''; $('set-server').value = s.server || '';
    show($('admin-box'), !!S.admin);
    adminErr('');
    armReset(false);
    settingsLoaded = true;
  }
  function saveSettings() {
    if (!settingsLoaded) return;
    AL.call('saveSettings', {
      nickname: $('set-nick').value.trim(),
      server: S.admin ? $('set-server').value.trim() : ''
    });
  }

  // почта
  function loadMailForm() {
    var m = S.mail || {};
    $('mail-host').value = m.host || ''; $('mail-port').value = m.port || 993; $('mail-user').value = m.user || ''; $('mail-pass').value = '';
    $('mail-sender').value = m.sender || ''; selectValue($('mail-sec'), m.security || '');
    show($('mail-msg'), false); show($('btn-mail-disconnect'), !!m.configured); $('btn-mail-connect').disabled = false;
    // почта подключена — остаётся только «Отключить»
    show($('btn-mail-connect'), !m.configured);
  }
  // правка полей возвращает кнопку
  ['mail-host', 'mail-port', 'mail-user', 'mail-pass', 'mail-sender'].forEach(function (id) {
    $(id).addEventListener('input', function () { show($('btn-mail-connect'), true); });
  });
  $('mail-sec').addEventListener('change', function () { show($('btn-mail-connect'), true); });
  // Порт и шифрование ходят парой: меняешь одно — подсказываем второе.
  $('mail-sec').addEventListener('change', function () {
    var port = +$('mail-port').value, v = selectValue(this);
    if (v === 'starttls' && (port === 993 || !port)) $('mail-port').value = 143;
    if (v === 'ssl' && port === 143) $('mail-port').value = 993;
  });
  function mailMsg(text, ok) { $('mail-msg').textContent = text; $('mail-msg').style.color = ok ? 'var(--ok)' : (ok === null ? 'var(--muted)' : 'var(--danger)'); show($('mail-msg'), true); }
  $('mail-user').addEventListener('change', function () {
    if ($('mail-host').value.trim()) return;
    var d = (this.value.split('@')[1] || '').toLowerCase();
    var map = { 'mail.ru': 'imap.mail.ru', 'bk.ru': 'imap.mail.ru', 'inbox.ru': 'imap.mail.ru', 'list.ru': 'imap.mail.ru', 'gmail.com': 'imap.gmail.com', 'outlook.com': 'outlook.office365.com', 'hotmail.com': 'outlook.office365.com' };
    if (map[d]) $('mail-host').value = map[d]; else if (d.indexOf('yandex') >= 0 || d === 'ya.ru') $('mail-host').value = 'imap.yandex.ru'; else if (/mirea\.ru$/.test(d)) { $('mail-host').value = 'imap.mirea.ru'; $('mail-port').value = 993; selectValue($('mail-sec'), ''); }
  });
  $('btn-mail-connect').onclick = function () {
    var s = {
      host: $('mail-host').value.trim(), port: +$('mail-port').value || 993,
      user: $('mail-user').value.trim(), password: $('mail-pass').value,
      sender: $('mail-sender').value.trim(), security: selectValue($('mail-sec'))
    };
    if (!s.host || !s.user || !s.password) return mailMsg('Заполните сервер, логин и пароль', false);
    $('btn-mail-connect').disabled = true; mailMsg('Подключение к ' + s.host + '…', null);
    AL.call('mailTest', s).then(function () {
      mailMsg('Почта подключена, разбираются письма от «' + esc(s.sender || 'mts-link.ru') + '». Переключатель мониторинга появился на панели слева.', true);
      show($('btn-mail-connect'), false); show($('btn-mail-disconnect'), true);
    })
      .catch(function (e) { mailMsg(e.message, false); })
      .finally(function () { $('btn-mail-connect').disabled = false; });
  };
  $('btn-mail-disconnect').onclick = function () { AL.call('mailDisconnect'); closeDialog(); };

  // telegram: код уходит в deep-link, руками ничего не вводится
  var tgPoll = null, tgDeepLink = '';
  function tgStatus(text, cls) { $('tg-status').innerHTML = text; $('tg-status').style.color = cls === 'ok' ? 'var(--ok)' : cls === 'err' ? 'var(--danger)' : 'var(--muted)'; }
  function telegramFlow() {
    clearInterval(tgPoll); show($('btn-tg-open'), false); tgDeepLink = '';
    if (S.account && S.account.guest) return tgStatus('Привязка Telegram доступна только после входа в аккаунт', 'err');
    tgStatus('Открываю Telegram…');
    AL.call('telegramStatus').then(function (st) {
      if (st.linked) return tgStatus('✓ Telegram привязан' + (st.username ? ': @' + esc(st.username) : ''), 'ok');
      return AL.call('telegramCode').then(function (r) {
        tgDeepLink = r.deepLink;
        if (!tgDeepLink) return tgStatus('Telegram-бот на сервере не настроен', 'err');
        AL.call('openUrl', { url: tgDeepLink });
        tgStatus('Открылся чат с ботом — нажмите в нём <b>Start</b>, дальше всё подключится само.');
        show($('btn-tg-open'), true);   // на случай, если чат не открылся сам
        tgPoll = setInterval(function () {
          AL.call('telegramStatus').then(function (st) {
            if (st.linked) { clearInterval(tgPoll); show($('btn-tg-open'), false); tgStatus('✓ Telegram привязан' + (st.username ? ': @' + esc(st.username) : ''), 'ok'); }
          });
        }, 3000);
      });
    }).catch(function (e) { tgStatus(esc(e.message), 'err'); });
  }
  $('btn-tg-open').onclick = function () { if (tgDeepLink) AL.call('openUrl', { url: tgDeepLink }); };
  $('overlay').addEventListener('transitionend', function () {});

  $('btn-refresh').onclick = function () { AL.call('scheduleRefresh'); };
  function submitGroup() {
    var v = $('sch-group').value.trim().toUpperCase();
    $('sch-group').value = v;
    AL.call('setGroup', { group: v });
  }
  $('sch-group').addEventListener('change', submitGroup);
  $('sch-group').addEventListener('keydown', function (e) { if (e.key === 'Enter') { submitGroup(); this.blur(); } });
  var lastSig = '';
  var days = ['Вс', 'Пн', 'Вт', 'Ср', 'Чт', 'Пт', 'Сб'];
  function dayLabel(d) {
    var t = new Date(); t.setHours(0, 0, 0, 0);
    var dd = new Date(d); dd.setHours(0, 0, 0, 0);
    var diff = Math.round((dd - t) / 864e5);
    if (diff === 0) return 'СЕГОДНЯ'; if (diff === 1) return 'ЗАВТРА';
    return (days[dd.getDay()] + ', ' + ('0' + dd.getDate()).slice(-2) + '.' + ('0' + (dd.getMonth() + 1)).slice(-2)).toUpperCase();
  }
  var statusLook = { MARKED: ['✓ Отмечено', 's-marked'], LIVE: ['● В лекции', 's-live'], MISSED: ['Пропущено', 's-missed'], DONE: ['Завершено без отметки', 's-done'], PENDING: ['Ожидание', 's-pending'] };
  var sourceLabel = { email: 'из почты', manual: 'вручную', telegram: 'из Telegram', cloud: 'из облака', schedule: 'расписание' };
  on('schedule', function (d) {
    var st = d.stats || {};
    $('st-week').textContent = st.week_lessons || 0; $('st-attended').textContent = st.attended || 0; $('st-marked').textContent = st.marked || 0;
    var sec = st.seconds_inside || 0, h = Math.floor(sec / 3600), m = Math.floor(sec % 3600 / 60);
    $('st-time').textContent = h > 0 ? h + ' ч ' + m + ' мин' : m + ' мин';
    if (document.activeElement !== $('sch-group') && $('sch-group').value !== (d.group || '')) $('sch-group').value = d.group || '';
    $('sch-status').textContent = d.group ? (d.status || '')
      : ((d.entries || []).length ? 'Группа не указана: показаны сохранённые занятия, расписание МИРЭА не обновляется'
                                  : 'Впишите группу, чтобы загрузить расписание МИРЭА');
    var now = Date.now(), cutoff = now - 3 * 3600e3;
    var items = (d.entries || []).filter(function (e) { return e.id === d.activeId || new Date(e.end).getTime() >= cutoff; }).slice(0, 40);
    var sig = items.map(function (e) { return e.id + e.status + e.url; }).join('|') + d.activeId;
    // Таймер активной лекции обновляем без перестройки списка
    if (sig === lastSig) {
      var t = document.querySelector('.entry.active .timer');
      var a = items.filter(function (e) { return e.id === d.activeId; })[0];
      if (t && a) t.textContent = 'в лекции ' + fmtDur(a.seconds_inside);
      return;
    }
    lastSig = sig;
    var html = '', lastDay = '';
    if (!items.length) html = '<div class="muted">Лекций пока нет. Укажите группу для загрузки расписания или включите мониторинг почты.</div>';
    items.forEach(function (e) {
      var start = new Date(e.start), end = new Date(e.end);
      var day = start.toDateString();
      if (day !== lastDay) { lastDay = day; html += '<div class="day">' + dayLabel(start) + '</div>'; }
      var look = statusLook[e.status] || statusLook.PENDING;
      var active = e.id === d.activeId;
      var meta = [sourceLabel[e.source] || e.source, e.location, e.teacher].filter(Boolean).join(' · ');
      var hm = function (x) { return ('0' + x.getHours()).slice(-2) + ':' + ('0' + x.getMinutes()).slice(-2); };
      html += '<div class="glass entry' + (active ? ' active' : '') + '">'
        + '<div class="time">' + hm(start) + '<br>' + hm(end) + '</div>'
        + '<div class="name">' + esc(e.title) + '</div>'
        + '<div class="status ' + look[1] + '" title="Статус отметки присутствия">' + look[0] + '</div>'
        + '<div class="meta">' + esc(meta) + '</div>'
        + (active ? '<div class="meta mono timer">в лекции ' + fmtDur(e.seconds_inside) + '</div>' : (e.seconds_inside > 0 ? '<div class="meta">в лекции ' + fmtDur(e.seconds_inside) + '</div>' : '<div></div>'))
        + '<div class="actions">'
        + (e.url ? '<button class="btn subtle" data-open="' + esc(e.url) + '" title="' + esc(e.url) + '">Ссылка</button>'
                 + (active ? '' : '<button class="btn ghost" data-connect="' + esc(e.url) + '" data-title="' + esc(e.title) + '" title="Войти на эту трансляцию сейчас">Подключиться</button>')
                 : '<span class="dim">ссылка ещё не получена</span>')
        + (active || e.source === 'schedule' ? '' : '<button class="icon-btn danger" data-remove="' + esc(e.id) + '" title="Убрать эту запись из списка"><span class="ico ico-close"></span></button>')
        + '</div></div>';
    });
    $('sch-list').innerHTML = html;
  });
  $('sch-list').addEventListener('click', function (e) {
    var b = e.target.closest('button'); if (!b) return;
    if (b.dataset.open) AL.call('openUrl', { url: b.dataset.open });
    if (b.dataset.connect) { AL.call('connect', { url: b.dataset.connect, title: b.dataset.title }); showTab(0); }
    if (b.dataset.remove) AL.call('scheduleRemove', { id: b.dataset.remove });
  });

  var minLevel = 1, logTotal = 0, levelNames = ['ОТЛАДКА', 'ИНФО', 'ПРЕДУПР', 'ОШИБКА'];
  function addLog(e) {
    var div = document.createElement('div');
    div.className = 'l' + e.level; div.dataset.level = e.level;
    div.textContent = '[' + e.time + '] ' + levelNames[e.level] + ' · ' + e.source + ' · ' + e.message;
    div.hidden = e.level < minLevel;
    var log = $('log'), atBottom = log.scrollTop + log.clientHeight >= log.scrollHeight - 8;
    log.appendChild(div);
    while (log.children.length > 2000) log.removeChild(log.firstChild);
    logTotal++; $('log-count').textContent = logTotal + ' записей';
    if (atBottom) log.scrollTop = log.scrollHeight;
  }
  on('log', addLog);
  document.querySelectorAll('#log-filter .tab').forEach(function (b) {
    b.onclick = function () {
      minLevel = +b.dataset.level;
      document.querySelectorAll('#log-filter .tab').forEach(function (x) { x.classList.toggle('active', x === b); });
      Array.prototype.forEach.call($('log').children, function (d) { d.hidden = +d.dataset.level < minLevel; });
      $('log').scrollTop = $('log').scrollHeight;
    };
  });
  $('btn-copy-log').onclick = function () { AL.call('copyLog'); };
  $('btn-clear-log').onclick = function () { $('log').innerHTML = ''; logTotal = 0; $('log-count').textContent = ''; };

  var obSteps = [], obIndex = -1;
  function obStep(i) {
    obIndex = i; var s = obSteps[i]; showTab(s.tab || 0);
    $('ob-title').textContent = s.title; $('ob-text').textContent = s.text;
    $('ob-counter').textContent = (i + 1) + ' из ' + obSteps.length;
    show($('ob-back'), i > 0); $('ob-next').textContent = i + 1 === obSteps.length ? 'Готово' : 'Далее';
    function place() {
      var tip = $('ob-tip'), hole = $('ob-hole'), W = window.innerWidth, H = window.innerHeight;
      var target = s.target && $(s.target), r = target ? target.getBoundingClientRect() : null;
      if (r && (r.width < 2 || r.height < 2)) r = null;   // элемент скрыт — подсвечивать нечего
      if (r) { hole.style.cssText = 'left:' + (r.left - 6) + 'px;top:' + (r.top - 6) + 'px;width:' + (r.width + 12) + 'px;height:' + (r.height + 12) + 'px;'; hole.classList.remove('hidden'); }
      else hole.classList.add('hidden');
      var tw = 340, th = tip.offsetHeight || 150, x, y, m = 12;
      if (!r) { x = (W - tw) / 2; y = (H - th) / 2; }
      else if (r.right + 16 + tw <= W - m) { x = r.right + 16; y = r.top + r.height / 2 - th / 2; }
      else if (r.left - 16 - tw >= m) { x = r.left - 16 - tw; y = r.top + r.height / 2 - th / 2; }
      else if (r.bottom + 14 + th <= H - m) { x = r.left + r.width / 2 - tw / 2; y = r.bottom + 14; }
      else if (r.top - 14 - th >= m) { x = r.left + r.width / 2 - tw / 2; y = r.top - 14 - th; }
      else { x = r.left + r.width / 2 - tw / 2; y = r.top + r.height / 2 - th / 2; }
      // Панель обязана целиком помещаться в окно
      x = Math.max(m, Math.min(x, W - tw - m)); y = Math.max(m, Math.min(y, H - th - m));
      tip.style.left = x + 'px'; tip.style.top = y + 'px';
    }
    // Страница въезжает анимацией, поэтому первое измерение уточняем, когда
    // она встала на место: иначе рамка подсветки стоит со сдвигом.
    setTimeout(place, 60);
    setTimeout(place, 320);
  }
  function obFinish() { obIndex = -1; show($('onboard'), false); AL.call('onboardingDone'); AL.call('overlay', { open: false }); showTab(0); }
  $('ob-next').onclick = function () { if (obIndex + 1 >= obSteps.length) obFinish(); else obStep(obIndex + 1); };
  $('ob-back').onclick = function () { if (obIndex > 0) obStep(obIndex - 1); };
  $('ob-skip').onclick = obFinish;
  function startOnboarding() {
    obSteps = [
      { target: 'url', title: 'Ссылка на трансляцию', text: 'Вставьте ссылку MTS-Link. Если оставить поле пустым, ссылки придут сами: из расписания, из письма или из Telegram.' },
      { target: 'auth-block', title: 'Авторизация', text: 'Telegram — команды и уведомления. МИРЭА — вход в ЕСКО, без него не пройдёт отметка по QR-коду. Почта — разбор приглашений MTS-Link.' },
      { target: 'btn-start', title: 'Старт сессии', text: 'Одна кнопка запускает всё: связь с сервером, планировщик по расписанию, мониторинг почты и вход на трансляцию.' },
      { target: 'previewer', title: 'Live Previewer', text: 'Здесь идёт трансляция: картинка включается сразу, как клиент вошёл в лекцию. Кнопка «ЭКО» рядом с таймером выключает отрисовку кадров — звук, anti-AFK и сканер QR продолжают работать, а ноутбук не греется. Сама по себе картинка гаснет, когда окно свёрнуто или открыта другая вкладка.' },
      { tab: 1, target: 'sch-group', title: 'Группа', text: 'Впишите группу как в расписании МИРЭА, например ИВБО-21-23, и нажмите Enter.' },
      { tab: 1, target: 'sch-list', title: 'Список лекций', text: 'Здесь лекции из вашего расписания, справа у каждой — текущий статус.' },
      { tab: 1, target: 'stats', title: 'Статистика', text: 'Занятий на неделе, сколько посещено, сколько раз отметка засчитана и общее время внутри трансляций.' },
      { tab: 1, target: 'btn-refresh', title: 'Обновление расписания', text: 'Расписание перечитывается само, но кнопка перезагружает его сразу. Рядом видно, что получилось: сколько дистанционных занятий нашлось.' },
      { target: 'btn-telegram', title: 'Если ссылка не пришла', text: 'Занятие началось, а ссылки нет — клиент ждёт письмо 15 минут и потом сам пишет вам в Telegram с просьбой прислать ссылку. Ответьте боту ссылкой, и он подключится. Туда же приходят старт лекции, подтверждение присутствия и результат отметки.' },
      { target: 'btn-volume', title: 'Громкость', text: 'Регулятор громкости трансляции, как в системном микшере Windows.' },
      { target: 'btn-settings', title: 'Параметры', text: 'Имя, под которым клиент входит на трансляцию. Рядом с крестиком — кнопка «свернуть в трей»: окно исчезает, а клиент продолжает вести лекцию в фоне.' },
      { target: 'btn-account', title: 'Аккаунт', text: 'Войдите, чтобы настройки, ссылки и статусы синхронизировались между устройствами и работал Telegram, либо продолжайте как гость — тогда всё хранится только на этом ПК.' }
    ];
    show($('onboard'), true); AL.call('overlay', { open: true }); obStep(0);
  }
  window.addEventListener('resize', function () { if (obIndex >= 0) obStep(obIndex); });

  // Результат отметки по QR: подпись и цвет.
  var attendLook = {
    SUCCESS: ['✓ Отмечено', 'a-ok'],
    NEEDS_AUTH: ['✖ Не авторизован', 'a-err'],
    ERROR: ['✖ Ошибка отметки', 'a-err'],
    RETRY: ['● Осечка', 'a-warn'],
    TIMEOUT: ['● Нет ответа', 'a-warn']
  };
  var onboardingStarted = false;
  on('state', function (s) {
    S = s;
    document.body.classList.toggle('transparent', !!s.transparent);
    // соединение
    var c = $('conn');
    if (s.account.guest) { c.textContent = '● локально'; c.className = 'conn local'; c.title = 'Гостевой режим: сервер не используется, настройки и статусы хранятся на этом ПК'; }
    else if (s.connection.online) { c.textContent = '● онлайн'; c.className = 'conn online'; c.title = 'Соединение с сервером установлено'; }
    else { c.textContent = '● офлайн'; c.className = 'conn'; c.title = 'Нет соединения с сервером синхронизации'; }
    $('account-label').textContent = s.account.guest ? 'Гость' : s.account.login;
    $('account-dot').classList.toggle('on', !s.account.guest);
    $('mode-hint').textContent = s.account.guest ? 'Гостевой режим: данные хранятся только на этом ПК, Telegram недоступен' : 'Настройки и статусы синхронизируются с сервером';
    $('btn-telegram').disabled = s.account.guest;
    // После привязки на кнопке — привязанный ник, а не слово «Telegram».
    var tg = s.telegram || {};
    $('dot-telegram').classList.toggle('on', !!tg.linked);
    $('telegram-label').textContent = tg.linked ? (tg.username ? '@' + tg.username : 'Telegram привязан') : 'Telegram';
    $('btn-telegram').title = tg.linked ? 'Telegram привязан' + (tg.username ? ' (@' + tg.username + ')' : '') + ' — уведомления и команды работают'
      : 'Привязать Telegram-бота: команды и уведомления';
    var esco = s.esco || {};
    $('dot-esco').classList.toggle('on', !!esco.ok);
    // вход в ЕСКО определяется по странице
    $('esco-label').textContent = esco.loginActive ? 'Готово, вернуться' : (esco.ok && esco.name ? 'МИРЭА: ' + esco.name : 'МИРЭА');
    $('btn-esco').title = esco.loginActive ? 'Закрыть окно входа и вернуться к трансляции'
      : esco.ok ? 'Вход в ЕСКО выполнен' + (esco.name ? ' (' + esco.name + ')' : '') + ' — отметка по QR-коду работает'
                : 'Войти в единую систему МИРЭА для автоматической отметки';
    $('dot-mail').classList.toggle('on', !!s.mail.configured);
    $('mail-row').classList.toggle('collapsed', !s.mail.configured);
    $('mail-toggle').checked = !!s.mail.monitoring; $('mail-status').textContent = s.mail.status || (s.mail.monitoring ? 'Мониторинг активен' : 'Мониторинг выключен');
    if (document.activeElement !== $('url') && s.url != null && $('url').value !== s.url) $('url').value = s.url;
    // сессия
    $('start-label').textContent = s.globalSession ? 'Стоп' : 'Старт';
    $('btn-start').className = 'btn big ' + (s.globalSession ? 'danger' : 'primary');
    $('btn-start').firstElementChild.className = 'ico ' + (s.globalSession ? 'ico-stop' : 'ico-play');
    var ses = s.session;
    show($('lecture-line'), ses.active); $('lecture-line').textContent = '● В лекции: ' + ses.title;
    show($('pill'), ses.active);
    $('pill-state').textContent = ses.state;
    timerOn = !!ses.active;
    timerBase = ses.seconds || 0; timerAt = Date.now();
    $('pill-timer').textContent = fmtDur(timerBase);
    var people = ses.participants || 0;
    show($('pill-people'), ses.active && people > 0);
    $('pill-people').textContent = people + ' чел.';
    // Итог отметки по QR-коду: зелёный — засчитана, жёлтый — осечка.
    var at = s.attendance || {}, look = attendLook[at.status];
    if (look && ses.active) {
      $('pill-attend').textContent = look[0];
      $('pill-attend').className = 'attend ' + look[1];
      $('pill-attend').title = at.text || look[0];
    }
    show($('pill-attend'), !!look && ses.active);
    $('btn-eco').classList.toggle('on', !!s.eco.lowPower);
    $('btn-eco').title = s.eco.lowPower ? (s.eco.reason || 'Кадры не рендерятся') + '. Нажмите, чтобы показать трансляцию'
                                        : 'Эко-режим: не рисовать кадры, оставить звук, anti-AFK и сканер QR';
    // заглушку прячем, только когда сцена реально видна
    var ph = $('placeholder');
    if (s.stageShown) ph.classList.add('fade');
    else {
      ph.classList.remove('fade');
      if (ses.active && s.eco.lowPower) { $('ph-title').textContent = 'Эко-режим'; $('ph-text').textContent = s.eco.reason || 'Кадры не рендерятся, работают звук и anti-AFK'; show($('btn-watch'), true); }
      else if (ses.active) { $('ph-title').textContent = 'Подключение к трансляции'; $('ph-text').textContent = ses.title || 'Открываю страницу лекции'; show($('btn-watch'), false); }
      else { $('ph-title').textContent = 'Нет активной трансляции'; $('ph-text').textContent = s.idleHint || 'Вставьте ссылку слева и нажмите «Старт», либо дождитесь ссылки из почты или Telegram'; show($('btn-watch'), false); }
    }
    // громкость
    if (document.activeElement !== $('volume')) applyVolumeUI(s.volume);
    // обновление
    var u = s.update || {};
    show($('btn-update'), !!u.available);
    $('btn-update').disabled = u.progress >= 0;
    $('btn-update').textContent = u.progress >= 100 ? 'Перезапуск…' : u.progress >= 0 ? 'Загрузка ' + u.progress + '%' : 'Обновить до ' + u.version;
    // аккаунт-диалог
    $('acc-status').innerHTML = s.account.guest ? 'Гостевой режим. Войдите, чтобы синхронизировать настройки между устройствами и подключить Telegram.'
      : 'Вы вошли как <b>' + esc(s.account.login) + '</b>. Настройки, ссылки и статусы синхронизируются с сервером.';
    show($('acc-form'), s.account.guest); show($('btn-signout'), !s.account.guest);
    // Имя для входа спрашиваем один раз на попытку: если окно закрыли крестиком,
    // навязываться повторно не надо.
    if (!s.needNickname) nickAsked = false;
    else if (!nickAsked && !dialog) { nickAsked = true; askNickname(); }
    // онбординг при первом запуске
    if (!s.onboardingDone && !onboardingStarted) { onboardingStarted = true; setTimeout(startOnboarding, 600); }
    sendPreviewRect();
  });

  AL.call('ready').then(function () { sendPreviewRect(true); });
  new ResizeObserver(function () { sendPreviewRect(); }).observe($('previewer'));
})();
