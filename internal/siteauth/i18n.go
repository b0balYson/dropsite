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
	},
}

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
