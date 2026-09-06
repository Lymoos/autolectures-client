(function () {
  var AL = window.__AL;
  if (!AL) return;   
  var BUTTONS = ['button, [role="button"], input[type="submit"], input[type="button"]', 'a, span'];
  var RE_CONFIRM = /ПОДТВЕРЖДАЮ|ПОДТВЕРДИТЬ|ПРИСУТСТВ|Я\s+ЗДЕСЬ|Я\s+НА\s+МЕСТЕ|ОСТАТЬСЯ|KEEP[\s-]?ALIVE|STAY\s+(SIGNED|CONNECTED|HERE)|I'?M\s+HERE|CONFIRM/i;
  var RE_CLOSE = /^(ЗАКРЫТЬ|CLOSE|ОК|OK|ПОНЯТНО|GOT\s+IT)$/i;
  var RE_DIALOG = /ВЫ\s+(ЕЩЁ|ЕЩЕ)\s+ЗДЕСЬ|ПОДТВЕРДИТЕ|ПОДТВЕРЖДЕНИЕ\s+ПРИСУТСТВ|ПРОВЕРКА\s+АКТИВНОСТИ|ВЫ\s+НА\s+МЕСТЕ|ARE\s+YOU\s+STILL/i;
  AL.onReady(function (bridge) {
    var pending = false;
    var reported = false;
    var ticks = 0;
    AL.log('info', 'Anti-AFK активен: слежу за окном подтверждения присутствия');
    setInterval(function () {
      if (pending || bridge.antiAfkEnabled === false) return;
      var btn = AL.findButton(BUTTONS, RE_CONFIRM, null, 80);
      if (!btn) {
        if (!reported && ++ticks % 5 === 0 && RE_DIALOG.test(AL.text(document.body))) {
          reported = true;
          var labels = [];
          AL.deepQuery('button, [role="button"], a').forEach(function (el) {
            var t = AL.label(el);
            if (t && t.length <= 60 && AL.isVisible(el) && labels.length < 8) labels.push(t);
          });
          AL.log('warn', 'Похоже, показано окно проверки активности, но кнопка не распознана. '
                       + 'Кнопки на экране: ' + (labels.join(' | ') || 'не найдены'));
        }
        return;
      }
      pending = true;
      var delay = 5000 + Math.random() * 15000;
      AL.log('info', 'Найдена кнопка присутствия «' + AL.label(btn) + '», жду '
                     + (delay / 1000).toFixed(1) + ' с перед нажатием');
      setTimeout(function () {
        var target = AL.findButton(BUTTONS, RE_CONFIRM, null, 80) || btn;
        AL.realClick(target);
        AL.log('info', 'Присутствие подтверждено');
        try { AL.post({ type: 'presence', text: AL.label(target) }); } catch (e) {  }
        setTimeout(function () {
          var close = AL.findButton(BUTTONS, RE_CLOSE, null, 20);
          if (close) {
            AL.realClick(close);
            AL.log('info', 'Окно «Отлично. Рады, что вы с нами!» закрыто');
          }
          pending = false;
        }, 2000);
      }, delay);
    }, 2000);
  });
})();
