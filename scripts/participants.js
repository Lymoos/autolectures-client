// participants.js — число участников трансляции.
//
// В MTS-Link счётчик виден только в панели участников, а открывает её кнопка без
// подписи — одна иконка, положение которой меняется. Поэтому порядок такой:
// сначала ищем готовый счётчик на видимой части, потом пробуем открыть панель
// подходящими кнопками и смотрим, появился ли заголовок «Участники N».
// Сработавшую кнопку запоминаем и дальше жмём только её.
(function () {
  var AL = window.__AL;
  if (!AL) return;

  var RE_PEOPLE = /участник|участвуют|participant|attendee|people|member|зрител|viewer/i;
  var RE_SKIP = /сообщени|message|чат|chat|вопрос|question|опрос|poll|минут|min|сек|sec|%/i;
  // Кнопки, которые трогать нельзя: выход, микрофон, камера, запись, реакции.
  var RE_DANGER = /выйти|выход|уйти|покинут|отключ|leave|hangup|hang-up|endcall|end-call|заверш|звонок|трубк|phone|call|микрофон|micro|mic-|камер|camera|запис|record|подн|hand|реакц|reaction|emoji|настрой|settings|громк|volume|полноэкран|fullscreen|демонстрац|share|презентац|чат|chat|вопрос|question|опрос|poll/i;
  // Заголовок панели: «Участники 2», «Participants 12».
  var RE_HEADER = /^(участник[а-яё]*|participants?|people)\D{0,6}(\d{1,5})$/i;
  var RE_BADGE = [
    /участник(?:и|ов)\D{0,12}(\d{1,5})/i,
    /(\d{1,5})\D{0,12}участник/i,
    /participants?\D{0,12}(\d{1,5})/i,
    /(\d{1,5})\D{0,12}(?:participants|attendees|viewers|online)/i
  ];
  var PEEK_EVERY = 180000;   // как часто заглядываем в список: раз в три минуты
  var ROW = '[class*="participant" i], [class*="attendee" i], [class*="member" i], [role="listitem"], li';

  function attr(el, name) { return (el && el.getAttribute && el.getAttribute(name)) || ''; }

  function hintOf(el) {
    var cls = (el && typeof el.className === 'string') ? el.className : '';
    return attr(el, 'aria-label') + ' ' + attr(el, 'title') + ' ' + attr(el, 'data-testid') + ' ' + cls;
  }

  // Прямой путь: у кнопки участников есть подпись для читалок экрана
  // (aria-label="Участники") и служебная метка data-testid. Если они на месте,
  // ничего перебирать не нужно.
  var DIRECT = '[aria-label*="частник" i], [aria-label*="articipant" i], [aria-label*="eople" i],'
             + '[title*="частник" i], [title*="articipant" i],'
             + '[data-testid*="eoples" i], [data-testid*="articipant" i], [data-testid*="eople" i]';

  function directButton() {
    var nodes = AL.deepQuery(DIRECT);
    for (var i = 0; i < nodes.length; i++) {
      var el = nodes[i];
      var btn = (el.closest && el.closest('button, [role="button"], a')) || el;
      if (!AL.isClickable(btn)) continue;
      var hint = hintOf(btn) + ' ' + (btn.outerHTML || '').slice(0, 400);
      if (RE_DANGER.test(hint)) continue;
      return btn;
    }
    return null;
  }

  function num(text) {
    var m = String(text).match(/(\d{1,5})/);
    if (!m) return -1;
    var n = parseInt(m[1], 10);
    return (isFinite(n) && n >= 0 && n < 100000) ? n : -1;
  }

  // Заголовок открытой панели: «Участники 2», «Участники (2)», а также случай,
  // когда число лежит в соседнем бейдже — тогда смотрим текст родителя.
  function panelCountLoose() {
    var nodes = AL.deepQuery('h1, h2, h3, h4, header, [role="heading"], div, span');
    for (var i = 0; i < nodes.length; i++) {
      var el = nodes[i];
      if (!AL.isVisible(el)) continue;
      var t = AL.text(el).replace(/\s+/g, ' ').trim();
      if (!t || t.length > 24) continue;
      if (!/^(участник[а-яё]*|participants?|people)$/i.test(t)) continue;
      var box = el.parentElement || el;
      var whole = AL.text(box).replace(/\s+/g, ' ').trim();
      if (whole.length > 40) continue;
      var m = whole.match(/(\d{1,5})/);
      if (m) return parseInt(m[1], 10);
    }
    return -1;
  }

  // Заголовок открытой панели участников.
  function panelCount() {
    var nodes = AL.deepQuery('h1, h2, h3, h4, header, [role="heading"], div, span');
    for (var i = 0; i < nodes.length; i++) {
      var el = nodes[i];
      if (el.children && el.children.length > 3) continue;
      if (!AL.isVisible(el)) continue;
      var t = AL.text(el);
      if (!t || t.length > 40) continue;
      var m = t.replace(/\s+/g, ' ').trim().replace(/[()]/g, ' ').replace(/\s+/g, ' ').trim().match(RE_HEADER);
      if (m) return parseInt(m[2], 10);
    }
    return panelCountLoose();
  }

  // Счётчик, видимый без открытия панели. Имена классов сюда не берём: строка
  // вроде «participants-list__item-8» превращалась в «8 участников».
  function badgeCount() {
    var nodes = AL.deepQuery('[aria-label], [title], button, [role="button"], [role="tab"], [class*="counter" i], [class*="badge" i], [class*="count" i], span, div');
    for (var i = 0; i < nodes.length; i++) {
      var el = nodes[i];
      if (el.children && el.children.length > 3) continue;
      if (!AL.isVisible(el)) continue;
      var aria = (el.getAttribute && (el.getAttribute('aria-label') || el.getAttribute('title'))) || '';
      var hint = (aria + ' ' + AL.text(el)).replace(/\s+/g, ' ').trim();
      if (!hint || hint.length > 40 || RE_SKIP.test(hint)) continue;
      // «participants-list__item-8» — это идентификатор в разметке, а не счётчик
      if (/[-_\/]\s*\d/.test(hint) || hint.indexOf('__') >= 0) continue;
      for (var r = 0; r < RE_BADGE.length; r++) {
        var m = hint.match(RE_BADGE[r]);
        if (m) {
          var n = parseInt(m[1], 10);
          if (isFinite(n) && n >= 0 && n < 100000) return n;
        }
      }
    }
    return -1;
  }

  // Строки открытого списка — запасной способ, если в заголовке числа нет.
  function listCount() {
    var lists = AL.deepQuery('[class*="participant" i], [class*="attendee" i], [class*="member" i], [role="list"], ul');
    var best = -1;
    for (var i = 0; i < lists.length; i++) {
      var box = lists[i];
      if (!AL.isVisible(box)) continue;
      var rows = 0;
      try {
        var items = box.querySelectorAll(ROW);
        for (var j = 0; j < items.length; j++) {
          var it = items[j];
          if (it.querySelector && it.querySelector(ROW)) continue;
          if (AL.isVisible(it)) rows++;
        }
      } catch (e) { continue; }
      if (rows >= 2 && rows > best) best = rows;
    }
    return best;
  }

  // Кнопки-кандидаты: сначала те, где в подписи или классе есть «участники»,
  // затем просто маленькие иконки панели инструментов.
  function candidates() {
    var nodes = AL.deepQuery('button, [role="button"], [class*="icon" i], [class*="btn" i], [class*="tab" i]');
    var out = [];
    for (var i = 0; i < nodes.length; i++) {
      var el = nodes[i];
      if (!AL.isClickable(el)) continue;
      var hint = hintOf(el) + ' ' + AL.text(el);
      // Разметку иконки тоже смотрим: у кнопки завершения подпись бывает только
      // в классе вложенного svg.
      var html = (el.outerHTML || '').slice(0, 600);
      if (RE_DANGER.test(hint) || RE_DANGER.test(html)) continue;
      // Красная кнопка в панели — это «завершить». Не трогаем её никогда.
      var bg = '';
      try { bg = window.getComputedStyle(el).backgroundColor || ''; } catch (e) { /* игнорируем */ }
      var rgb = bg.match(/rgba?\((\d+),\s*(\d+),\s*(\d+)/);
      if (rgb && +rgb[1] > 140 && +rgb[2] < 95 && +rgb[3] < 95) continue;
      var box = el.getBoundingClientRect();
      var icon = box.width > 16 && box.width < 90 && box.height > 16 && box.height < 90;
      var named = RE_PEOPLE.test(hint);
      if (!named && !icon) continue;
      if (!named && AL.text(el).length > 20) continue;   // не иконка, а обычная кнопка
      out.push({ el: el, score: named ? 2 : (el.querySelector('svg, img, i') ? 1 : 0) });
    }
    out.sort(function (a, b) { return b.score - a.score; });
    return out.slice(0, 4);
  }

  // Явное «мероприятие завершено» надёжнее любой статистики, но текст из чата
  // за объявление принимать нельзя — смотрим только заголовки и заглушки.
  // \w в JavaScript — только латиница, поэтому окончания слов задаём явно.
  var RE_ENDED = /(мероприяти[ея]|трансляци[яи]|вебинар|эфир|встреча|конференци[яи])\s+(заверш|оконч|закончил)[а-яё]*|(event|webinar|broadcast|meeting)\s+(has\s+)?(ended|finished)|спасибо\s+за\s+участие/i;
  var ENDED_HOSTS = 'h1, h2, h3, h4, [role="dialog"], [class*="title" i], [class*="finish" i], [class*="end" i], [class*="over" i], [class*="stub" i]';
  var RE_CHATLIKE = /chat|message|comment|коммент/i;

  function endedText() {
    var nodes = AL.deepQuery(ENDED_HOSTS);
    for (var i = 0; i < nodes.length; i++) {
      var el = nodes[i];
      if (!AL.isVisible(el)) continue;
      var t = AL.text(el);
      if (!t || t.length > 120 || !RE_ENDED.test(t)) continue;
      var box = el.closest ? el.closest('[class*="chat" i], [class*="message" i], [class*="comment" i]') : null;
      if (box || RE_CHATLIKE.test((typeof el.className === 'string' ? el.className : ''))) continue;
      return t;
    }
    return '';
  }

  AL.onReady(function () {
    var last = -1, sentAt = 0, lastPeek = 0, peeking = false, endedSeen = 0, endedSent = false;
    var known = null;      // кнопка, которая открывает панель участников
    var confirmed = false; // панель хоть раз открывалась — бейджам больше не верим

    function publish(n) {
      if (n < 0) return;
      var now = Date.now();
      if (n === last && now - sentAt < 60000) return;
      last = n; sentAt = now;
      AL.post({ type: 'participants', count: n });
    }

    // Открыть панель, посчитать, закрыть обратно.
    function readCount() {
      var n = panelCount();
      if (n < 0) n = listCount();
      return n;
    }

    function peekWith(list, index, done) {
      if (index >= list.length) { done(false); return; }
      var el = list[index].el;
      AL.realClick(el);
      setTimeout(function () {
        var n = readCount();
        if (n >= 0) { success(el, n, done); return; }
        // Мышь не сработала — часть кнопок отзывается только на клавиатуру.
        AL.keyClick(el);
        setTimeout(function () {
          var n2 = readCount();
          if (n2 >= 0) { success(el, n2, done); return; }
          AL.realClick(el);   // не та кнопка — возвращаем как было
          setTimeout(function () { peekWith(list, index + 1, done); }, 400);
        }, 800);
      }, 900);
    }

    function success(el, n, done) {
      known = el;
      confirmed = true;
      publish(n);
      AL.log('debug', 'Панель участников открывается кнопкой «' + (hintOf(el) || 'без подписи').trim() + '», участников: ' + n);
      AL.realClick(el);   // закрываем обратно
      done(true);
    }

    function peek() {
      if (peeking) return;
      peeking = true;
      lastPeek = Date.now();
      var list;
      if (known) {
        list = [{ el: known, score: 3 }];
      } else {
        var direct = directButton();
        // Нашли по подписи — перебирать иконки вслепую не нужно.
        list = direct ? [{ el: direct, score: 9 }] : candidates();
      }
      if (!list.length) { peeking = false; return; }
      peekWith(list, 0, function (ok) {
        peeking = false;
        if (!ok) {
          known = null;
          AL.log('debug', 'Панель участников открыть не удалось: перебрано кнопок ' + list.length);
        }
      });
    }

    // Первый раз заглядываем через полминуты после входа: комната ещё грузится.
    lastPeek = Date.now() - PEEK_EVERY + 30000;

    setInterval(function () {
      // сообщение о завершении подтверждаем двумя замерами подряд
      var over = endedText();
      if (over && !endedSent) {
        if (++endedSeen >= 2) { endedSent = true; AL.post({ type: 'ended', text: over }); }
      } else if (!over) {
        endedSeen = 0;
      }
      if (peeking) return;

      // 1. Панель открыта — заголовок «Участники N» самый надёжный источник.
      var n = panelCount();
      if (n >= 0) { confirmed = true; publish(n); return; }

      // 2. Иначе сами открываем панель и закрываем обратно.
      if (Date.now() - lastPeek > PEEK_EVERY) { peek(); return; }

      // 3. Бейдж рядом с кнопкой — только пока панель ни разу не открылась.
      if (!confirmed) {
        var b = badgeCount();
        if (b >= 0) publish(b);
      }
    }, 10000);
  });
})();
