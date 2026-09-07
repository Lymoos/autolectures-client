//go:build windows

package session

import (
	"encoding/json"
	"strconv"
	"strings"
	"time"

	"github.com/Lymoos/autolectures/client/internal/logger"
	"github.com/Lymoos/autolectures/client/internal/sdo"
)

// Обход СДО идёт в той же скрытой вкладке, что и проверка ЕСКО: сессия общая,
// отдельный вход не нужен. Скрипты только собирают данные со страницы — что с
// ними делать, решает приложение.

const sdoCoursesScript = `
(function () {
  function post(o) { try { chrome.webview.postMessage(JSON.stringify(o)); } catch (e) {} }
  try {
    var seen = {}, out = [];
    var links = document.querySelectorAll('a[href*="/course/view.php"]');
    for (var i = 0; i < links.length; i++) {
      var a = links[i];
      var m = /[?&]id=(\d+)/.exec(a.getAttribute('href') || '');
      if (!m) continue;
      var title = (a.innerText || a.textContent || '').replace(/\s+/g, ' ').trim();
      if (!title || title.length > 160) continue;
      if (seen[m[1]]) continue;
      seen[m[1]] = 1;
      out.push({ id: m[1], title: title, url: a.href });
    }
    post({ type: 'sdoCourses', courses: out, host: location.hostname });
  } catch (e) { post({ type: 'sdoCourses', courses: [], host: location.hostname }); }
})();`

// На странице курса вебинары лежат в разделе «Вебинары»: это элементы с
// календарным значком, внутри которых уже таблица занятий. Берём и сам раздел,
// и любые элементы, похожие на вебинар по названию.
const sdoActivitiesScript = `
(function () {
  function post(o) { try { chrome.webview.postMessage(JSON.stringify(o)); } catch (e) {} }
  function text(el) { return ((el && (el.innerText || el.textContent)) || '').replace(/\s+/g, ' ').trim(); }

  // Раздел ищем не по классам разметки — они у Moodle меняются от версии к
  // версии, — а по ближайшему заголовку выше по документу.
  function headingFor(el) {
    var box = el.closest && el.closest('li.section, .section, [data-for="section"], .course-section, section, .card, .box');
    if (box) {
      var h = box.querySelector('.sectionname, [data-for="section_title"], h2, h3, h4');
      var t = text(h);
      if (t) return t;
    }
    var node = el, guard = 0;
    while (node && guard++ < 300) {
      var prev = node.previousElementSibling;
      while (prev) {
        if (/^H[1-4]$/.test(prev.tagName)) { var d = text(prev); if (d) return d; }
        var inner = prev.querySelector && prev.querySelector('h2, h3, h4, .sectionname');
        if (inner) { var t2 = text(inner); if (t2) return t2; }
        prev = prev.previousElementSibling;
      }
      node = node.parentElement;
    }
    return '';
  }

  var SKIP = /форум|объявлен|задани|тест|материал|литератур|курсов|глоссар|опрос|анкет|файл|папк|страниц|ведомост|оценк|участник/i;
  var WEB = /вебинар|webinar|трансляц|лекци|занят|поток|mts|линк|meeting|конференц/i;
  var WEBHREF = /\/mod\/(mtslink|webinar|bigbluebuttonbn|lti|zoom|jitsi|collaborate|meeting)/i;

  try {
    var out = [], seen = {}, mods = 0, sample = [];
    var links = document.querySelectorAll('a[href]');
    for (var i = 0; i < links.length; i++) {
      var a = links[i], href = a.href || '';
      if (href.indexOf('/mod/') < 0) continue;
      mods++;
      var t = text(a.querySelector('.instancename')) || text(a);
      if (!t || t.length > 250) continue;
      if (sample.length < 12) sample.push(t);
      if (seen[href]) continue;
      var section = headingFor(a);
      var web = WEBHREF.test(href) || WEB.test(section) || WEB.test(t);
      if (!web) continue;
      // «Материалы по дисциплине» с лекциями в названии не считаем вебинаром,
      // если сам раздел про материалы.
      if (SKIP.test(t) && !WEB.test(section)) continue;
      seen[href] = 1;
      out.push({ title: t, url: href, section: section });
    }
    // Признак входа собираем из нескольких: класс темы бывает свой, поэтому
    // смотрим ещё меню пользователя и текст страницы. Жёстко останавливаемся
    // только если перед нами форма входа.
    var cls = ' ' + (document.body.className || '') + ' ' + (document.documentElement.className || '') + ' ';
    var loginForm = !!document.querySelector('form#login, form.login, input[name="password"]');
    var userMenu = !!document.querySelector('[data-region="user-menu"], .usermenu, #user-menu-toggle, .userinitials, .avatar, .userpicture');
    var hints = /мои курсы|my courses|личный кабинет|dashboard|выход|log out|режим редактирования/i.test(document.body.innerText || '');
    var logged = / userloggedin /.test(cls) || (!loginForm && (userMenu || hints));
    post({ type: 'sdoActivities', items: out, mods: mods, sample: sample, host: location.hostname,
           page: (document.title || '').slice(0, 120),
           logged: logged, loginForm: loginForm });
  } catch (e) {
    post({ type: 'sdoActivities', items: [], mods: 0, sample: [], host: location.hostname, page: String(e), logged: false });
  }
})();`

// Таблица вебинаров внутри элемента: название, начало, окончание, группы и
// кнопка действия. «Подключиться» бывает и ссылкой, и кнопкой со скриптом —
// поэтому запоминаем номер строки, чтобы потом нажать её.
const sdoWebinarsScript = `
(function () {
  function post(o) { try { chrome.webview.postMessage(JSON.stringify(o)); } catch (e) {} }
  function text(el) { return ((el && (el.innerText || el.textContent)) || '').replace(/\s+/g, ' ').trim(); }
  try {
    var rows = document.querySelectorAll('table tr, [role="row"]');
    var out = [], n = 0;
    for (var i = 0; i < rows.length; i++) {
      var cells = rows[i].querySelectorAll('td, [role="cell"]');
      if (cells.length < 3) continue;
      var act = rows[i].querySelector('a[href], button, [role="button"]');
      var url = '';
      if (act && act.tagName === 'A') url = act.href || '';
      var row = {
        index: n++,
        title: text(cells[0]),
        start: text(cells[1]),
        end: cells.length > 2 ? text(cells[2]) : '',
        groups: cells.length > 3 ? text(cells[3]) : '',
        action: text(act),
        url: url
      };
      rows[i].setAttribute('data-al-row', String(row.index));
      out.push(row);
    }
    post({ type: 'sdoWebinars', rows: out, href: location.href, host: location.hostname });
  } catch (e) { post({ type: 'sdoWebinars', rows: [], href: location.href, host: location.hostname }); }
})();`

// Нажимаем кнопку входа в нужной строке и ждём, куда уведёт страница: адрес
// встречи может открыться и переходом, и новой вкладкой, и просто появиться
// ссылкой в разметке.
const sdoJoinScriptTemplate = `
(function () {
  function post(o) { try { chrome.webview.postMessage(JSON.stringify(o)); } catch (e) {} }
  function meeting(u) { return /(mts-link|webinar\.ru|telemost|zoom\.us|teams\.microsoft|meet\.google|jazz)/i.test(u || ''); }
  try {
    var row = document.querySelector('[data-al-row="__ROW__"]');
    if (!row) { post({ type: 'sdoJoin', url: '', reason: 'строка исчезла' }); return; }
    var act = row.querySelector('a[href], button, [role="button"]');
    if (!act) { post({ type: 'sdoJoin', url: '', reason: 'кнопка не найдена' }); return; }
    if (act.tagName === 'A' && meeting(act.href)) { post({ type: 'sdoJoin', url: act.href }); return; }
    var before = location.href;
    // окно-«вкладка» перехватываем: MTS-Link часто открывается через window.open
    var opened = '';
    var realOpen = window.open;
    window.open = function (u) { opened = u || ''; return null; };
    try { act.click(); } catch (e) {}
    setTimeout(function () {
      window.open = realOpen;
      var found = meeting(opened) ? opened : '';
      if (!found && meeting(location.href)) found = location.href;
      if (!found) {
        var links = document.querySelectorAll('a[href]');
        for (var i = 0; i < links.length; i++) {
          if (meeting(links[i].href)) { found = links[i].href; break; }
        }
      }
      post({ type: 'sdoJoin', url: found, href: location.href, was: before });
    }, 3000);
  } catch (e) { post({ type: 'sdoJoin', url: '', reason: String(e) }); }
})();`

const sdoLinksScript = `
(function () {
  function post(o) { try { chrome.webview.postMessage(JSON.stringify(o)); } catch (e) {} }
  function text(el) { return ((el && (el.innerText || el.textContent)) || '').replace(/\s+/g, ' ').trim(); }
  function sectionOf(el) {
    var box = el.closest && el.closest('li.section, .section, [data-for="section"], .course-section');
    if (!box) return '';
    var head = box.querySelector('.sectionname, h3, h4, [data-for="section_title"]');
    return text(head).slice(0, 120);
  }
  try {
    var out = [];
    var seen = {};
    var nodes = document.querySelectorAll('a[href]');
    for (var i = 0; i < nodes.length; i++) {
      var a = nodes[i], href = a.href || '';
      if (!href || seen[href]) continue;
      // ссылки-встречи и элементы курса «Гиперссылка», за которыми они прячутся
      var isMod = /\/mod\/(url|lti|bigbluebuttonbn)\/view\.php/i.test(href);
      var isMeet = /(mts-link|webinar\.ru|telemost|zoom\.us|teams\.microsoft|meet\.google|jazz)/i.test(href);
      if (!isMod && !isMeet) continue;
      seen[href] = 1;
      out.push({ url: href, title: text(a).slice(0, 160), section: sectionOf(a), kind: isMeet ? 'meeting' : 'activity' });
      if (out.length >= 200) break;
    }
    post({ type: 'sdoLinks', links: out });
  } catch (e) { post({ type: 'sdoLinks', links: [] }); }
})();`

// Три шага поиска ссылки: страница курса → элемент с таблицей вебинаров →
// нажатие «Подключиться» в нужной строке.
func (e *Engine) ScanSdoActivities(courseURL string) bool {
	return e.startSdoScan("sdo-activities", courseURL, sdoActivitiesScript)
}

func (e *Engine) ScanSdoWebinars(activityURL string) bool {
	return e.startSdoScan("sdo-webinars", activityURL, sdoWebinarsScript)
}

// JoinSdoWebinar нажимает кнопку входа в строке таблицы, открытой предыдущим
// шагом: страница уже загружена, поэтому навигация не нужна.
func (e *Engine) JoinSdoWebinar(index int) bool {
	e.mu.Lock()
	if e.authMode != "" || e.validating {
		e.mu.Unlock()
		return false
	}
	e.authMode = "sdo-join"
	if e.sdoTimer != nil {
		e.sdoTimer.Stop()
	}
	e.sdoTimer = time.AfterFunc(20*time.Second, func() {
		e.dispatch(func() { e.finishSdoScan("sdo-join", nil) })
	})
	e.mu.Unlock()
	e.auth.Eval(strings.ReplaceAll(sdoJoinScriptTemplate, "__ROW__", strconv.Itoa(index)))
	return true
}

// ScanSdoCourses собирает список курсов студента.
func (e *Engine) ScanSdoCourses(dashboardURL string) bool {
	return e.startSdoScan("sdo-courses", dashboardURL, sdoCoursesScript)
}

// ScanSdoCourse собирает со страницы курса ссылки на встречи и элементы,
// за которыми они могут скрываться.
func (e *Engine) ScanSdoCourse(course, courseURL string) bool {
	e.mu.Lock()
	e.sdoCourse = course
	e.mu.Unlock()
	return e.startSdoScan("sdo-links", courseURL, sdoLinksScript)
}

func (e *Engine) startSdoScan(mode, target, script string) bool {
	e.mu.Lock()
	// Фоновая проверка входа уступает место поиску ссылки.
	if e.authMode == "sdo" {
		e.authMode = ""
		if e.sdoTimer != nil {
			e.sdoTimer.Stop()
			e.sdoTimer = nil
		}
	}
	if e.authMode != "" || e.validating {
		busy := e.authMode
		e.mu.Unlock()
		logger.Debugf("СДО", "Вкладка занята (%s) — обход отложен", busy)
		return false
	}
	e.authMode = mode
	e.expectHost = hostOf(target)
	e.sdoScript = script
	if e.sdoTimer != nil {
		e.sdoTimer.Stop()
	}
	e.sdoTimer = time.AfterFunc(25*time.Second, func() {
		e.dispatch(func() { e.finishSdoScan(mode, nil) })
	})
	e.mu.Unlock()

	e.auth.SetVisible(false)
	e.auth.Navigate(target)
	return true
}

// finishSdoScan закрывает обход независимо от того, ответила страница или нет.
func (e *Engine) finishSdoScan(mode string, payload map[string]any) {
	if payload != nil && mode != "sdo-join" {
		if host, _ := payload["host"].(string); !e.fromExpectedPage(host) {
			logger.Debugf("СДО", "Разбор пришёл со страницы %q — жду нужную", host)
			return
		}
	}
	e.mu.Lock()
	if e.authMode != mode {
		e.mu.Unlock()
		return
	}
	e.authMode = ""
	e.sdoScript = ""
	e.expectHost = ""
	course := e.sdoCourse
	if e.sdoTimer != nil {
		e.sdoTimer.Stop()
		e.sdoTimer = nil
	}
	e.mu.Unlock()
	if mode != "sdo-webinars" {
		// таблицу оставляем открытой: следом по ней жмём «Подключиться»
		e.auth.Navigate("about:blank")
	}

	if payload == nil {
		logger.Debugf("СДО", "Обход %s не завершился вовремя", mode)
		return
	}
	switch mode {
	case "sdo-activities":
		var items []sdo.WebinarActivity
		decode(payload["items"], &items)
		var info sdo.PageInfo
		decode(payload, &info)
		logger.Infof("СДО", "Страница «%s»: элементов курса %d, похожих на вебинар %d%s",
			info.Page, info.Mods, len(items), map[bool]string{false: ", вход не выполнен", true: ""}[info.Logged])
		if e.OnSdoActivities != nil {
			e.OnSdoActivities(items, info)
		}
	case "sdo-webinars":
		var rows []sdo.Webinar
		decode(payload["rows"], &rows)
		logger.Infof("СДО", "Строк в таблице вебинаров: %d", len(rows))
		if e.OnSdoWebinars != nil {
			e.OnSdoWebinars(rows)
		}
	case "sdo-join":
		u, _ := payload["url"].(string)
		reason, _ := payload["reason"].(string)
		if u == "" && reason != "" {
			logger.Debugf("СДО", "Ссылка на вход не получена: %s", reason)
		}
		if e.OnSdoJoin != nil {
			e.OnSdoJoin(u)
		}
	case "sdo-courses":
		var courses []sdo.Course
		decode(payload["courses"], &courses)
		logger.Infof("СДО", "Найдено курсов: %d", len(courses))
		if e.OnSdoCourses != nil {
			e.OnSdoCourses(courses)
		}
	case "sdo-links":
		var links []sdo.Link
		decode(payload["links"], &links)
		meetings := 0
		for _, l := range links {
			if sdo.IsMeetingLink(l.URL) {
				meetings++
			}
		}
		logger.Infof("СДО", "Курс «%s»: ссылок на встречи %d, элементов курса %d", course, meetings, len(links)-meetings)
		if e.OnSdoLinks != nil {
			e.OnSdoLinks(course, links)
		}
	}
}

func decode(v any, out any) {
	raw, err := json.Marshal(v)
	if err != nil {
		return
	}
	_ = json.Unmarshal(raw, out)
}
