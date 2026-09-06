// participants.js — снимает число участников трансляции. Пока это только
// показатель в интерфейсе и журнале, но на нём будет строиться статистика
// посещаемости, поэтому счётчик считается всегда, когда сессия активна.
(function () {
  var AL = window.__AL;
  if (!AL) return;

  var SELECTOR = '[aria-label], [title], button, [role="button"], [role="tab"], '
               + '[class*="participant" i], [class*="member" i], [class*="attendee" i], [class*="counter" i]';
  var RE_PEOPLE = /участник|участвуют|participant|attendee|в\s*эфире|зрител|viewer|online/i;
  var RE_SKIP = /сообщени|message|чат|chat|вопрос|question|минут|min|сек|sec|%/i;

  function countFrom(text) {
    var m = String(text).match(/(\d[\d\s ]{0,6})/);
    if (!m) return -1;
    var n = parseInt(m[1].replace(/[\s ]/g, ''), 10);
    return (isFinite(n) && n >= 0 && n < 100000) ? n : -1;
  }

  function find() {
    var nodes = AL.deepQuery(SELECTOR);
    for (var i = 0; i < nodes.length; i++) {
      var el = nodes[i];
      if (!AL.isVisible(el)) continue;
      var hint = (el.getAttribute('aria-label') || '') + ' ' + (el.getAttribute('title') || '') + ' '
               + (el.className && el.className.baseVal !== undefined ? '' : (el.className || '')) + ' ' + AL.text(el);
      if (hint.length > 120 || !RE_PEOPLE.test(hint) || RE_SKIP.test(hint)) continue;
      var n = countFrom(hint);
      if (n >= 0) return n;
    }
    return -1;
  }

  AL.onReady(function () {
    var last = -1;
    setInterval(function () {
      var n = find();
      if (n < 0 || n === last) return;
      last = n;
      AL.post({ type: 'participants', count: n });
    }, 5000);
  });
})();
