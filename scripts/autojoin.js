// autojoin.js — автоматический гостевой вход на трансляцию MTS-Link.
(function () {
  var AL = window.__AL;
  if (!AL) return;   // не главный фрейм

  var BUTTONS = ['button, [role="button"], input[type="submit"]', 'a'];
  var NAME_SELECTOR = 'input[placeholder*="имя" i], input[placeholder*="name" i], ' +
                      'input[name*="name" i], input[aria-label*="имя" i], input[type="text"]';
  var RE_NO_DEVICES = /БЕЗ\s+(МИКРОФОНА|УСТРОЙСТВ|КАМЕРЫ)|WITHOUT\s+(CAMERA|DEVICE|MIC)/i;
  // JOIN — англоязычный виджет входа, часто в iframe
  var RE_JOIN = /ПРИСОЕДИН|ПОДКЛЮЧИТЬСЯ|ВОЙТИ|JOIN|ENTER/i;
  // «Присоединиться в приложении МТС Линк» открывает внешнее приложение.
  var RE_SKIP_JOIN = /БЕЗ\s+(МИКРОФОНА|УСТРОЙСТВ|КАМЕРЫ)|WITHOUT\s+(CAMERA|DEVICE|MIC)|В\s+ПРИЛОЖЕНИИ|IN\s+THE\s+APP|СКАЧА|DOWNLOAD|УСТАНОВ/i;
  var RE_IN_ROOM = /ВЫЙТИ\s+В\s+ЭФИР|ПОКИНУТЬ|LEAVE\s+(ROOM|EVENT|MEETING)/i;
  var RE_NAME_OK = /имя|name|как\s+вас\s+зовут/i;
  // Поле чата, поиска и прочие текстовые поля комнаты именем заполнять нельзя.
  var RE_NAME_SKIP = /сообщени|message|чат|chat|поиск|search|почт|e-?mail|пароль|password|код|code|вопрос|question/i;

  // Безымянное текстовое поле считается полем имени только рядом с кнопкой.
  function findNameInput(loginFormPresent) {
    var nodes = AL.deepQuery(NAME_SELECTOR);
    var fallback = null;
    for (var i = 0; i < nodes.length; i++) {
      var el = nodes[i];
      if (!AL.isVisible(el)) continue;
      var hint = (el.placeholder || '') + ' ' + (el.getAttribute('aria-label') || '') + ' ' + (el.name || '');
      if (RE_NAME_SKIP.test(hint)) continue;
      if (RE_NAME_OK.test(hint)) return el;
      if (!fallback) fallback = el;
    }
    return loginFormPresent ? fallback : null;
  }

  // Признаки того, что страница уже показывает комнату, а не форму входа.
  function inRoom() {
    if (AL.findButton(BUTTONS, RE_IN_ROOM, null, 60)) return true;
    if (AL.first(AL.deepQuery('input[placeholder*="сообщени" i], textarea[placeholder*="сообщени" i], '
                              + '[contenteditable][data-placeholder*="сообщени" i]'))) return true;
    var videos = AL.deepQuery('video');
    for (var i = 0; i < videos.length; i++)
      if (videos[i].readyState >= 2 || AL.isVisible(videos[i])) return true;
    return false;
  }

  AL.onReady(function (bridge) {
    var joined = false;
    var nameFilled = false;
    var askedName = false;
    var filledAt = 0;
    var enterTried = false;
    var clicks = 0;
    var settled = 0;
    var startedAt = Date.now();
    var lastRun = 0;
    var timer = null;
    var observer = null;

    function stop() {
      if (timer) { clearInterval(timer); timer = null; }
      if (observer) { observer.disconnect(); observer = null; }
    }

    function finish(reason) {
      joined = true;
      stop();
      AL.log('info', reason);
      setTimeout(function () { AL.post({ type: 'joined', title: document.title || 'Трансляция' }); }, 2000);
    }

    function tick() {
      if (joined) { stop(); return; }
      var now = Date.now();
      if (now - lastRun < 150) return;   // защита от шквала мутаций DOM
      lastRun = now;
      if (now - startedAt > 180000) {
        stop();
        AL.log('warn', 'Автовход: форма входа не пройдена за 3 минуты');
        return;
      }

      // 1. Кнопка входа без устройств — последний шаг сценария.
      var noDevices = AL.findButton(BUTTONS, RE_NO_DEVICES, null, 80);
      if (noDevices) {
        AL.realClick(noDevices);
        finish('Нажата кнопка «' + AL.label(noDevices) + '» — вход на трансляцию выполнен');
        return;
      }

      // по кнопке входа отличаем форму от комнаты
      var join = AL.findButton(BUTTONS, RE_JOIN, RE_SKIP_JOIN, 80);

      // 2. имя участника
      var input = findNameInput(join !== null);
      var typed = input ? (input.value || '').trim() : '';
      if (typed.length > 1) {
        // имя уже стоит — виджет подставил сам или это наш ввод
        if (!nameFilled) { nameFilled = true; filledAt = now; }
      } else if (input) {
        var nick = (bridge.nickname || '').trim();
        if (!nick) {
          // Имени нет ни на странице, ни в настройках: просим его у пользователя
          // в окне приложения и ждём — форму без имени отправлять бесполезно.
          if (!askedName) {
            askedName = true;
            AL.log('warn', 'Нужно имя участника: форма входа просит его, а в параметрах пусто');
            AL.post({ type: 'nicknameRequired' });
          }
          return;
        }
        AL.setInputValue(input, nick);
        nameFilled = true;
        filledAt = now;
        enterTried = false;
        AL.log('info', 'Имя участника введено: ' + nick);
        return;
      }

      // 3. «Присоединиться» — жмём, пока форма не исчезнет
      if (join && clicks < 40) {
        clicks++;
        AL.realClick(join);
        if (clicks === 1 || clicks % 10 === 0)
          AL.log('info', 'Нажимаю «' + AL.label(join) + '» (попытка ' + clicks + ')');
        return;
      }

      // Имя вписано, а кнопка так и не включилась — пробуем Enter.
      if (!join && input && typed.length > 1 && filledAt && now - filledAt > 2500 && !enterTried) {
        enterTried = true;
        AL.log('info', 'Кнопка входа осталась неактивной — отправляю форму по Enter');
        AL.pressEnter(input);
        return;
      }

      // 4. Формы входа нет. Если видны элементы трансляции, мы уже внутри.
      if (!input && !join) {
        settled++;
        if (settled >= 3 && inRoom())
          finish('Форма входа не потребовалась — уже в комнате');
      } else {
        settled = 0;
      }
    }

    // опрос + реакция на перерисовку
    timer = setInterval(tick, 500);
    try {
      observer = new MutationObserver(tick);
      observer.observe(document.documentElement, {
        childList: true,
        subtree: true,
        attributes: true,
        attributeFilter: ['disabled', 'aria-disabled', 'class', 'style']
      });
    } catch (e) {
      AL.log('debug', 'MutationObserver недоступен, работает только опрос');
    }
    tick();
  });
})();
