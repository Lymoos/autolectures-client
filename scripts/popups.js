// popups.js — закрывает подсказки и опросы MTS-Link, которые лезут поверх трансляции.
(function () {
  var AL = window.__AL;
  if (!AL) return;

  var BUTTONS = ['button, [role="button"], a, [class*="button" i]'];
  // Только явные «закрыть подсказку» и «не сейчас». Крестики и «Закрыть» не
  // трогаем: тем же крестиком закрывается форма входа на трансляцию.
  var RE_DISMISS = new RegExp('^(ПОНЯТНО|ПОНЯЛ|ПОНЯЛА|ВСЁ ПОНЯТНО|ЯСНО|ХОРОШО|СПАСИБО|ОК|OK|OKAY'
                            + '|GOT IT|I GOT IT|GOT THIS|ПОЗЖЕ|НЕ СЕЙЧАС|В ДРУГОЙ РАЗ'
                            + '|LATER|MAYBE LATER|NOT NOW|REMIND ME LATER|DISMISS)$', 'i');
  // Всё, что уводит со страницы, ставит приложение или отправляет оценку.
  var RE_KEEP = /ОЦЕНИТЬ|RATE|ВЫЙТИ|LEAVE|ПОКИНУТЬ|СКАЧА|DOWNLOAD|УСТАНОВ|INSTALL|ПРИСОЕДИН|JOIN|ОТПРАВИТЬ|SUBMIT/i;

  AL.onReady(function () {
    var clicked = (typeof WeakSet === 'function') ? new WeakSet() : null;
    var last = 0;

    function sweep() {
      var now = Date.now();
      if (now - last < 700) return;   // не долбим интерфейс подряд
      var nodes = AL.deepQuery(BUTTONS[0]);
      for (var i = 0; i < nodes.length; i++) {
        var el = nodes[i];
        if (clicked && clicked.has(el)) continue;
        var text = AL.label(el);
        if (!text || text.length > 24) continue;
        if (RE_KEEP.test(text) || !RE_DISMISS.test(text)) continue;
        if (!AL.isClickable(el)) continue;
        if (clicked) clicked.add(el);
        last = now;
        AL.realClick(el);
        AL.log('debug', 'Закрыто всплывающее окно кнопкой «' + text + '»');
        return;
      }
    }

    setInterval(sweep, 1500);
    try {
      new MutationObserver(sweep).observe(document.documentElement, { childList: true, subtree: true });
    } catch (e) { /* хватит опроса */ }
    sweep();
  });
})();
