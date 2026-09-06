// qrscan.js — сканер QR-кодов отметки прямо внутри страницы.
(function () {
  var AL = window.__AL;
  if (!AL) return;   // не главный фрейм
  AL.onReady(function (bridge) {
    if (typeof jsQR !== 'function') {
      AL.log('warn', 'jsQR не загрузился — сканер QR-кодов отключён');
      return;
    }

    var canvas = document.createElement('canvas');
    var ctx = canvas.getContext('2d', { willReadFrequently: true });
    var recent = {};
    var MAX_W = 960;

    function decode(el, w, h) {
      if (!w || !h) return null;
      var scale = Math.min(1, MAX_W / w);
      var cw = Math.max(1, Math.round(w * scale));
      var ch = Math.max(1, Math.round(h * scale));
      canvas.width = cw; canvas.height = ch;
      var img;
      try {
        ctx.drawImage(el, 0, 0, cw, ch);
        img = ctx.getImageData(0, 0, cw, ch);
      } catch (e) { return null; }           // tainted canvas / не готово
      var code = jsQR(img.data, cw, ch, { inversionAttempts: 'dontInvert' });
      return code && code.data ? code.data : null;
    }

    setInterval(function () {
      if (!bridge.scanEnabled) return;
      var now = Date.now();
      var sources = [];
      document.querySelectorAll('video').forEach(function (v) {
        if (v.readyState >= 2 && v.videoWidth > 0) sources.push([v, v.videoWidth, v.videoHeight]);
      });
      document.querySelectorAll('canvas').forEach(function (c) {
        if (c !== canvas && c.width > 60 && c.height > 60) sources.push([c, c.width, c.height]);
      });
      document.querySelectorAll('img').forEach(function (i) {
        if (i.complete && i.naturalWidth > 80 && i.naturalHeight > 80 && AL.isVisible(i)) sources.push([i, i.naturalWidth, i.naturalHeight]);
      });
      // Сначала крупные источники (демонстрация экрана лектора)
      sources.sort(function (a, b) { return b[1] * b[2] - a[1] * a[2]; });

      for (var k = 0; k < sources.length; k++) {
        var data = decode(sources[k][0], sources[k][1], sources[k][2]);
        if (!data || !/^https?:\/\//i.test(data)) continue;
        if (recent[data] && now - recent[data] < 5000) continue;
        recent[data] = now;
        AL.post({ type: 'qr', url: data });
        break;
      }
      for (var key in recent) if (now - recent[key] > 30000) delete recent[key];
    }, 350);
  });
})();
