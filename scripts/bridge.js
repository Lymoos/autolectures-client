// bridge.js — мост между страницей трансляции и приложением (WebView2).
(function () {
  // мост в каждом фрейме: виджет входа бывает в iframe
  if (window.__AL) return;
  var isTop = window.top === window;

  var AL = window.__AL = {
    state: { nickname: '', scanEnabled: false, antiAfkEnabled: true, volume: 100 },
    _ready: [],
    _listeners: [],

    onReady: function (cb) { AL._ready.push(cb); },
    onState: function (cb) { AL._listeners.push(cb); },

    setState: function (s) {
      for (var k in s) if (Object.prototype.hasOwnProperty.call(s, k)) AL.state[k] = s[k];
      AL._listeners.forEach(function (cb) { try { cb(AL.state); } catch (e) { /* игнорируем */ } });
    },

    post: function (msg) {
      try { window.chrome.webview.postMessage(JSON.stringify(msg)); } catch (e) { /* нет моста */ }
    },

    log: function (level, msg) { AL.post({ type: 'log', level: level, message: String(msg) }); },

    text: function (el) {
      return ((el && (el.innerText || el.textContent)) || '').replace(/\s+/g, ' ').trim();
    },

    isVisible: function (el) {
      if (!el || !el.getBoundingClientRect) return false;
      var r = el.getBoundingClientRect();
      if (r.width <= 0 || r.height <= 0) return false;
      var st = window.getComputedStyle(el);
      return st.visibility !== 'hidden' && st.display !== 'none' && st.opacity !== '0';
    },

    first: function (list) {
      for (var i = 0; i < list.length; i++) if (AL.isVisible(list[i])) return list[i];
      return null;
    },

    // Установка значения в контролируемый (React/Vue/Angular) input.
    // Одного события input мало: форма входа MTS-Link включает кнопку только
    // после «человеческого» набора, поэтому шлём весь набор событий и снимаем
    // фокус — иначе имя стоит, а «Присоединиться» остаётся серой.
    setInputValue: function (input, value) {
      try { input.focus(); } catch (e) { /* игнорируем */ }
      AL.key(input, 'keydown');
      try {
        var proto = Object.getPrototypeOf(input);
        var desc = Object.getOwnPropertyDescriptor(proto, 'value')
                || Object.getOwnPropertyDescriptor(HTMLInputElement.prototype, 'value');
        if (desc && desc.set) desc.set.call(input, value); else input.value = value;
      } catch (e) { input.value = value; }
      try {
        input.dispatchEvent(new InputEvent('input', { bubbles: true, data: value, inputType: 'insertText' }));
      } catch (e) {
        input.dispatchEvent(new Event('input', { bubbles: true }));
      }
      AL.key(input, 'keyup');
      input.dispatchEvent(new Event('change', { bubbles: true }));
      try { input.blur(); } catch (e) { /* игнорируем */ }
      input.dispatchEvent(new Event('blur', { bubbles: true }));
      input.dispatchEvent(new FocusEvent('focusout', { bubbles: true }));
    },

    key: function (el, type, name) {
      var init = { bubbles: true, cancelable: true, key: name || 'a', code: name === 'Enter' ? 'Enter' : 'KeyA',
                   keyCode: name === 'Enter' ? 13 : 65, which: name === 'Enter' ? 13 : 65 };
      try { el.dispatchEvent(new KeyboardEvent(type, init)); } catch (e) { /* игнорируем */ }
    },

    // Формы, где кнопка так и не включилась, обычно отправляются по Enter.
    pressEnter: function (el) {
      try { el.focus(); } catch (e) { /* игнорируем */ }
      AL.key(el, 'keydown', 'Enter');
      AL.key(el, 'keypress', 'Enter');
      AL.key(el, 'keyup', 'Enter');
      var form = el.form || (el.closest && el.closest('form'));
      if (form && typeof form.requestSubmit === 'function') {
        try { form.requestSubmit(); } catch (e) { /* игнорируем */ }
      }
    },

    // Обход открытых shadow-root: части интерфейса живут в веб-компонентах.
    deepQuery: function (selector) {
      var out = [];
      (function walk(root) {
        try { out.push.apply(out, root.querySelectorAll(selector)); } catch (e) { return; }
        var all = root.querySelectorAll('*');
        for (var i = 0; i < all.length; i++) if (all[i].shadowRoot) walk(all[i].shadowRoot);
      })(document);
      return out;
    },

    label: function (el) {
      return AL.text(el) || el.value || el.getAttribute('aria-label') || '';
    },

    // Видимости недостаточно: кнопка может оставаться заблокированной.
    isClickable: function (el) {
      if (!AL.isVisible(el)) return false;
      if (el.disabled || el.getAttribute('aria-disabled') === 'true') return false;
      if (window.getComputedStyle(el).pointerEvents === 'none') return false;
      return true;
    },

    // Часть обработчиков слушает указательные события, а не click().
    realClick: function (el) {
      var opts = { bubbles: true, cancelable: true, view: window };
      ['pointerdown', 'mousedown', 'pointerup', 'mouseup', 'click'].forEach(function (type) {
        var Ctor = (type.indexOf('pointer') === 0 && window.PointerEvent) ? PointerEvent : MouseEvent;
        try { el.dispatchEvent(new Ctor(type, opts)); } catch (e) { /* игнорируем */ }
      });
      try { el.click(); } catch (e) { /* игнорируем */ }
    },

    // Первый кликабельный элемент с подходящим текстом.
    findButton: function (selectors, re, excludeRe, maxLen) {
      for (var s = 0; s < selectors.length; s++) {
        var nodes = AL.deepQuery(selectors[s]);
        for (var i = 0; i < nodes.length; i++) {
          var text = AL.label(nodes[i]);
          if (!text || (maxLen && text.length > maxLen)) continue;
          if (!re.test(text)) continue;
          if (excludeRe && excludeRe.test(text)) continue;
          if (AL.isClickable(nodes[i])) return nodes[i];
        }
      }
      return null;
    }
  };

  function start() {
    var cbs = AL._ready; AL._ready = [];
    cbs.forEach(function (cb) { try { cb(AL.state); } catch (e) { AL.log('error', 'Скрипт: ' + e); } });
    AL.onReady = function (cb) { try { cb(AL.state); } catch (e) { AL.log('error', 'Скрипт: ' + e); } };
    AL.post({ type: 'ready' });
    AL.log('debug', 'Мост установлен: ' + location.host + (isTop ? '' : ' (iframe)'));
    if (!isTop) return;   // заголовок вкладки берём только у главного документа
    // Заголовок страницы — название лекции
    var lastTitle = '';
    setInterval(function () {
      if (document.title && document.title !== lastTitle) {
        lastTitle = document.title;
        AL.post({ type: 'title', title: lastTitle });
      }
    }, 2000);
  }
  if (document.readyState === 'loading') document.addEventListener('DOMContentLoaded', start);
  else start();
})();
