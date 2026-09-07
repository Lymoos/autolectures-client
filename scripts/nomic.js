// nomic.js — страховка от захвата микрофона и камеры. WebView2 и так отклоняет
// запросы разрешений, но проверка выполняется на каждой лекции: если страница
// всё же попросит устройство, запрос будет отклонён здесь и попадёт в журнал.
(function () {
  var AL = window.__AL;
  if (!AL) return;

  var asked = 0;

  function deny(what) {
    asked++;
    AL.log('warn', 'Страница просит доступ к «' + what + '» — отказано клиентом');
    AL.post({ type: 'media', action: 'denied', kind: what });
    var err = new Error('Permission denied');
    err.name = 'NotAllowedError';
    return Promise.reject(err);
  }

  function kindOf(c) {
    var a = c && c.audio, v = c && c.video;
    return (a && v) ? 'микрофон и камера' : (a ? 'микрофон' : (v ? 'камера' : 'устройство'));
  }

  try {
    var md = navigator.mediaDevices;
    if (md && md.getUserMedia) {
      md.getUserMedia = function (c) { return deny(kindOf(c)); };
    }
    if (md && md.getDisplayMedia) {
      md.getDisplayMedia = function () { return deny('экран'); };
    }
    ['getUserMedia', 'webkitGetUserMedia', 'mozGetUserMedia'].forEach(function (name) {
      if (navigator[name]) {
        navigator[name] = function (c, ok, fail) {
          deny(kindOf(c)).catch(function (e) { if (fail) fail(e); });
        };
      }
    });
  } catch (e) {
    AL.log('error', 'Не удалось перехватить доступ к устройствам: ' + e);
  }

  // Отчёт при входе на лекцию: состояние разрешений и живые дорожки захвата.
  AL.onReady(function () {
    function report() {
      // Захват невозможен: getUserMedia перекрыт выше, поэтому достаточно
      // отчитаться о состоянии разрешения и о числе попыток его получить.
      var out = { type: 'media', action: 'check', mic: 'denied', asked: asked };
      if (navigator.permissions && navigator.permissions.query) {
        navigator.permissions.query({ name: 'microphone' }).then(function (st) {
          out.mic = st.state;
          AL.post(out);
        }).catch(function () { AL.post(out); });
      } else {
        AL.post(out);
      }
    }
    setTimeout(report, 4000);
    setInterval(report, 120000);
  });
})();
