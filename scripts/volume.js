// volume.js — управление громкостью медиа-элементов страницы.
(function () {
  var AL = window.__AL;
  if (!AL) return;
  AL.onReady(function (bridge) {
    var known = (typeof WeakSet === 'function') ? new WeakSet() : null;
    function apply() {
      var v = Math.max(0, Math.min(100, Number(bridge.volume))) / 100;
      // Плеер живёт в веб-компоненте, поэтому обычного querySelectorAll мало.
      AL.deepQuery('audio, video').forEach(function (m) {
        try {
          if (Math.abs(m.volume - v) > 0.001) m.volume = v;
          if (m.muted && v > 0) m.muted = false;
          // Плеер иногда сбрасывает громкость на свою — возвращаем свою обратно.
          if (known && !known.has(m)) {
            known.add(m);
            m.addEventListener('volumechange', function () {
              var want = Math.max(0, Math.min(100, Number(bridge.volume))) / 100;
              if (Math.abs(m.volume - want) > 0.02) {
                try { m.volume = want; } catch (e) { /* игнорируем */ }
              }
            });
          }
        } catch (e) { /* игнорируем */ }
      });
    }
    AL.onState(apply);
    try {
      new MutationObserver(function () { apply(); })
        .observe(document.documentElement, { childList: true, subtree: true });
    } catch (e) { /* игнорируем */ }
    setInterval(apply, 2000);
    apply();
  });
})();
