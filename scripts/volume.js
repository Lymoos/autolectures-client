(function () {
  var AL = window.__AL;
  if (!AL) return;   
  AL.onReady(function (bridge) {
    function apply() {
      var v = Math.max(0, Math.min(100, Number(bridge.volume))) / 100;
      document.querySelectorAll('audio, video').forEach(function (m) {
        try { if (Math.abs(m.volume - v) > 0.001) m.volume = v; } catch (e) {  }
      });
    }
    AL.onState(apply);
    try {
      new MutationObserver(function () { apply(); })
        .observe(document.documentElement, { childList: true, subtree: true });
    } catch (e) {  }
    setInterval(apply, 2000);
    apply();
  });
})();
