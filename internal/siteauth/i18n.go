package siteauth

import "strings"

// catalog holds the visitor login page strings. The page is neutral: it
// names the site, never the platform.
var catalog = map[string]map[string]string{
	"ru": { //nolint:gosec // UI strings, not credentials
		"page.title":   "Вход на сайт",
		"page.eyebrow": "Сайт закрыт паролем",
		"page.lead":    "Введите пароль, который вам передал владелец сайта.",
		"field.pass":   "Пароль",
		"submit":       "Открыть сайт",
		"note":         "На этом устройстве пароль запомнится на 7 дней.",
		"err.password": "Неверный пароль. Проверьте раскладку клавиатуры и попробуйте ещё раз.",
		"err.rate":     "Слишком много попыток. Попробуйте снова через {minutes} мин.",
		"err.csrf":     "Страница устарела. Введите пароль ещё раз.",
		"err.altcha":   "Не удалось завершить проверку браузера. Дождитесь отметки «Проверено» и попробуйте ещё раз.",
		"noscript":     "Для входа на сайт включите JavaScript: он нужен для автоматической проверки браузера.",
	},
	"en": { //nolint:gosec // UI strings, not credentials
		"page.title":   "Sign in to the site",
		"page.eyebrow": "This site is password protected",
		"page.lead":    "Enter the password the site owner gave you.",
		"field.pass":   "Password",
		"submit":       "Open the site",
		"note":         "This device will remember the password for 7 days.",
		"err.password": "Wrong password. Check the keyboard layout and try again.",
		"err.rate":     "Too many attempts. Try again in {minutes} min.",
		"err.csrf":     "The page has expired. Enter the password again.",
		"err.altcha":   "The browser check did not complete. Wait until it shows \"Verified\" and try again.",
		"noscript":     "Turn on JavaScript to sign in: it runs an automatic browser check.",
	},
	"zh": { //nolint:gosec // UI strings, not credentials
		"page.title":   "访问网站",
		"page.eyebrow": "此网站受密码保护",
		"page.lead":    "请输入网站所有者提供给您的密码。",
		"field.pass":   "密码",
		"submit":       "打开网站",
		"note":         "此设备将记住密码 7 天。",
		"err.password": "密码错误。请检查输入法和大小写后重试。",
		"err.rate":     "尝试次数过多。请在 {minutes} 分钟后重试。",
		"err.csrf":     "页面已过期，请重新输入密码。",
		"err.altcha":   "浏览器验证未完成。请等到显示“已验证”后再试一次。",
		"noscript":     "请启用 JavaScript 以登录：登录时需要自动进行浏览器验证。",
	},
}

// langTags maps a page language to its html lang attribute.
var langTags = map[string]string{"ru": "ru", "en": "en", "zh": "zh-CN"}

// pickLang returns the first supported language in Accept-Language order
// (quality values are not weighed: browsers list preferences in order).
func pickLang(header string) string {
	for _, part := range strings.Split(header, ",") {
		tag, _, _ := strings.Cut(strings.TrimSpace(part), ";")
		primary, _, _ := strings.Cut(strings.ToLower(tag), "-")
		if _, ok := catalog[primary]; ok {
			return primary
		}
	}
	return "en"
}
