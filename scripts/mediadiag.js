// mediadiag.js — диагностика медиа: поддержка кодеков и состояние.
(function () {
  var AL = window.__AL;
  if (!AL) return;   // не главный фрейм
  if (!/^https?:$/.test(location.protocol)) return;

  AL.onReady(function () {
    var probe = document.createElement('video');
    function mse(type) {
      try { return !!(window.MediaSource && MediaSource.isTypeSupported(type)); } catch (e) { return false; }
    }
    var h264 = mse('video/mp4; codecs="avc1.42E01E"');
    var aac = mse('audio/mp4; codecs="mp4a.40.2"');
    var rtcH264 = false;
    try {
      var caps = RTCRtpSender.getCapabilities('video');
      rtcH264 = !!(caps && caps.codecs.some(function (c) { return /h26\d/i.test(c.mimeType || ''); }));
    } catch (e) { /* нет WebRTC */ }
    AL.log(h264 && aac && rtcH264 ? 'info' : 'error',
      'Кодеки: MSE H.264 ' + (h264 ? 'да' : 'НЕТ') + ', AAC ' + (aac ? 'да' : 'НЕТ')
      + ', WebRTC H.264 ' + (rtcH264 ? 'да' : 'НЕТ')
      + ', HLS «' + (probe.canPlayType('application/vnd.apple.mpegurl') || 'нет') + '»');

    var count = 0, last = '';
    function report() {
      var vids = document.querySelectorAll('video');
      var parts = [];
      for (var i = 0; i < vids.length && i < 4; i++) {
        var v = vids[i];
        parts.push(v.videoWidth + 'x' + v.videoHeight + ' готовность ' + v.readyState
          + (v.paused ? ' пауза' : '') + (v.srcObject ? ' поток' : (v.currentSrc ? ' ' + v.currentSrc.split(':')[0] : ' без источника')));
      }
      var line = 'Медиа: video ' + vids.length + (parts.length ? ' (' + parts.join('; ') + ')' : '')
        + ', iframe ' + document.querySelectorAll('iframe').length;
      count++;
      if (count > 4 && line === last) return;   // молчим, пока состояние не меняется
      last = line;
      AL.log('debug', line);
    }
    [10000, 30000, 60000, 120000].forEach(function (t) { setTimeout(report, t); });
    setInterval(report, 120000);
  });
})();
