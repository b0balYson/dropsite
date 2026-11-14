#!/bin/sh
# Install Dropsite on your own server with one command, from macOS or Linux:
#
#   curl -fsSL https://raw.githubusercontent.com/b0balYson/dropsite/main/scripts/install-server.sh | sh
#
# It asks a few questions (server IP, root password or key file, your domain
# and e-mail; the short name for the server comes from the domain), then:
#   - creates a key ~/.ssh/dropsite_<name> and installs it on the server (the
#     root password is typed into ssh itself once and never stored);
#   - uploads and runs scripts/server-bootstrap.sh on the server (Docker,
#     swap, firewall, Dropsite, daily backups, update command);
#   - adds "Host <name>" with a tunnel to the admin interface to ~/.ssh/config;
#   - tells you which DNS records to create and waits until the domain works.
#
# The output is short: a few steps with a check mark each. Every detail, the
# whole server output included, goes to ~/.dropsite/install-<name>.log (mode
# 600; never the root password or keys). --verbose shows it all on screen.
#
# Answers can be given as options or environment variables (for automation):
#   --host HOST         DROPSITE_HOST          server IP address or host name
#   --port PORT         DROPSITE_SSH_PORT      SSH port (default 22)
#   --key FILE          DROPSITE_SSH_KEY       private key for root instead of the password
#   --name NAME         DROPSITE_NAME          short name: "ssh NAME" reaches the server
#                                              (default: from the domain, asked only
#                                              when that name is taken or invalid)
#   --domain DOMAIN     DROPSITE_DOMAIN        base domain for sites
#   --email EMAIL       DROPSITE_EMAIL         administrator login and certificate e-mail
#   --public-admin      DROPSITE_PUBLIC_ADMIN=1  also open the admin interface from the
#                                              internet at https://admin.DOMAIN (a potential
#                                              threat: anyone can find it; asked, default No)
#   --no-public-admin   DROPSITE_PUBLIC_ADMIN=0  keep it reachable through the SSH tunnel only
#   --skip-dns          DROPSITE_SKIP_DNS=1    do not wait for DNS and HTTPS
#   --check-dns         DROPSITE_CHECK_DNS=1   only wait for DNS/HTTPS of an installed
#                                              server (needs --name)
#   --yes               DROPSITE_YES=1         do not ask for confirmation
#   --verbose, -v       DROPSITE_VERBOSE=1     show every step and the server output
#   --lang LANG         DROPSITE_LANG          en, ru or zh (default: from LC_ALL /
#                                              LC_MESSAGES / LANG)
# Pass options through the pipe with: curl ... | sh -s -- --lang ru
#
# Other environment:
#   DROPSITE_REF          branch of the repository to install (default: main)
#   DROPSITE_REPO_URL     repository the server clones (default: GitHub)
#   DROPSITE_VERSION      release the server runs (default: latest; e.g. 1.4.2)
#   DROPSITE_BUILD=1      build from source on the server instead of
#                         downloading the release images
#   DROPSITE_BOOTSTRAP    local path of server-bootstrap.sh to upload
#   DROPSITE_DNS_TIMEOUT  seconds to wait for DNS and HTTPS (default 1800)
#   DROPSITE_SKIP_SWAP, DROPSITE_SKIP_FIREWALL  passed to the server (tests)
#
# Exit codes: 0 done, 1 error, 2 bad input, 3 DNS or HTTPS not ready yet,
# 130 interrupted.
set -eu

RAW_BASE="https://raw.githubusercontent.com/b0balYson/dropsite"
INSTALL_URL="$RAW_BASE/main/scripts/install-server.sh"
ADMIN_REMOTE_PORT=8082
FIRST_LOCAL_PORT=8082
DNS_INTERVAL=10

# ---------------------------------------------------------------------------
# Messages: one table per language. printf formats, %s are filled in order.

msgs_en() {
	M_title='Dropsite: your own server for sites'
	M_intro='This installs Dropsite on a fresh rented server (VPS) with Ubuntu 22.04 or newer, or Debian 12 or newer.
You will need: the server IP address and root password (from your hosting provider), a domain and an e-mail.
It takes 10-20 minutes. Press Ctrl+C to stop at any time.'
	M_q_host='Server IP address'
	M_q_auth='How do you sign in to the server? 1 = root password, 2 = key file'
	M_q_key='Path to the private key file'
	M_q_name='A short name for this server (later you type "ssh NAME" to reach it)'
	M_q_domain='Domain for your sites'
	M_q_email='Your e-mail'
	M_q_public_admin='Open the admin panel from the internet at https://admin.%s? It is handier but less safe: anyone will be able to find the panel. [y/N]'
	M_bad_host='This does not look like an IP address or a server name: %s'
	M_bad_port='The SSH port must be a number from 1 to 65535: %s'
	M_bad_key='Key file not found or not readable: %s'
	M_bad_name='The name may contain small Latin letters, digits and "-", must start with a letter, at most 32 characters: %s'
	M_name_taken='The name "%s" is already used in %s for another server. Choose another name.'
	M_bad_domain='This does not look like a domain (like sites.example.com): %s'
	M_bad_email='This does not look like an e-mail address: %s'
	M_missing='Missing answer: %s (no terminal to ask; pass it as an option, see --help)'
	M_summary='We are going to install:
  server:  %s
  name:    %s
  domain:  %s
  e-mail:  %s'
	M_confirm='Continue? [Y/n]'
	M_confirm_short='Install Dropsite on %s for %s? [Y/n]'
	M_cancelled='Cancelled. Nothing was changed.'
	M_need_tool='"%s" is required but not found on this computer.'
	M_installing='Installing Dropsite. It takes 10-20 minutes, keep this window open.'
	M_s_connect='Connecting to the server'
	M_s_connect_done='Connected to the server'
	M_s_install='Installing Dropsite on the server'
	M_s_install_done='Installed Dropsite'
	M_s_dns='Waiting for the domain'
	M_s_dns_done='The domain works'
	M_s_https='Turning on HTTPS'
	M_s_https_done='Turned on HTTPS'
	M_step_key='Creating the key for this server: %s'
	M_step_password='Type the root password of the server when asked (it stays invisible while you type).'
	M_step_userkey='Connecting with your key file.'
	M_key_failed='Could not sign in to %s. Check the IP address and the password (or key), then run the command again.'
	M_hostkey_changed='The server identity changed since the last connection (for example, the server was reinstalled). If that is expected, remove the old entry with:
  ssh-keygen -R %s
and run the command again.'
	M_step_check='Checking the server system'
	M_os_bad='The server runs "%s". Supported: Ubuntu 22.04 or newer, Debian 12 or newer. Reinstall the server with one of them in your provider'"'"'s control panel and run the command again.'
	M_step_sshcfg='Saved the connection as "%s" in %s (admin interface on local port %s)'
	M_step_bootstrap='Installing Dropsite on the server: 10-20 minutes. Keep this window open.'
	M_bootstrap_failed='The installation on the server stopped with an error.'
	M_server_log='Server log: ssh %s tail -n 100 /var/log/dropsite-install.log'
	M_log_tail='Last lines of the log:'
	M_log_at='Full log: %s'
	M_rerun='Run the same command again: finished steps are skipped.'
	M_download_failed='Could not download %s'
	M_dns_title='Now connect your domain. Where you bought %s, open its DNS settings and create these records:'
	M_dns_short='Now connect the domain. Where you bought %s, open its DNS settings and add these records:'
	M_dns_head='  type   name   value'
	M_dns_names='  ("@" means the domain itself; full names: %s and *.%s)'
	M_dns_hint='If a record with the same name already exists, change it instead of adding a second one.
DNS changes take from a few minutes to an hour. With Cloudflare, choose "DNS only" (grey cloud).'
	M_dns_cloudflare='With Cloudflare, choose "DNS only" (grey cloud).'
	M_q_wait='Wait here until the domain works? [Y/n]'
	M_dns_waiting='Checking every 10 seconds, up to %s minutes. Ctrl+C stops waiting; nothing breaks.'
	M_dns_ok='The domain points to your server.'
	M_dns_aaaa_missing='No AAAA (IPv6) records yet: sites still work over IPv4. Add them when you can.'
	M_dns_aaaa_wrong='The AAAA (IPv6) records point elsewhere: %s. Fix them, otherwise some visitors cannot open your sites.'
	M_https_waiting='Waiting for the HTTPS certificate of %s'
	M_https_ok='HTTPS works: %s'
	M_dns_timeout='The domain does not point to the server yet. Check the records above.'
	M_https_timeout='The HTTPS certificate is not ready yet. It usually appears a few minutes after DNS works.'
	M_check_later='To check again later run:
  %s'
	M_interrupted='Stopped. The server is installed and keeps working.'
	M_skip_dns='DNS check skipped.'
	M_done='Done! Dropsite is installed.'
	M_open_admin='Open the admin interface:
  1. In a terminal run:   ssh %s
     and keep that window open.
  2. In your browser open:   http://localhost:%s'
	M_login='Login:     %s
Password:  %s
Save the password in your password manager and change it after you sign in (Account page).'
	M_login_old='Login:     %s
Password:  the one from the first installation. Forgot it? Run:  ssh %s dropsite-admin-password'
	M_connect_ai='Connect ChatGPT or Claude: in the admin interface open "Connect AI" and follow the steps there.'
	M_open_public='The admin interface is also open to the internet: %s
This is a potential threat: use a strong, unique password and turn it off in Settings when you do not need it.'
	M_update='Update Dropsite later:   ssh %s dropsite-update'
	M_sites_at='Your sites will be at https://<name>.%s'
	M_no_install='No Dropsite server named "%s" in %s. Run the installer without --check-dns first.'
	M_done_short='Done!'
	M_final_ssh='1. Run: ssh %s   (keep that window open)'
	M_final_open='2. Open: http://localhost:%s'
	M_final_public='   Or from anywhere: %s   (open to the internet: use a strong password; turn it off in Settings when not needed)'
	M_final_login='   Login: %s   Password: %s   (save the password)'
	M_final_login_old='   Login: %s   Password: the one from the first installation (forgot it? ssh %s dropsite-admin-password)'
	M_final_next='Next, open "Getting started": it walks you through the rest.'
	M_final_connect='   Connect a chat app: paste %s as the MCP server address (works once, within an hour)'
	M_connect_url='Connect ChatGPT or Claude right away: paste this address into the chat app as the MCP server
and press Allow; no code needed. It works once, within an hour; later issue a new one with
ssh %s dropsite-connect.
  %s'
}

msgs_ru() {
	M_title='Dropsite — свой сервер для сайтов'
	M_intro='Скрипт установит Dropsite на новый арендованный сервер (VPS) с Ubuntu 22.04 или новее либо Debian 12 или новее.
Понадобятся: IP-адрес сервера и пароль root (из письма хостинга), домен и e-mail.
Займёт 10-20 минут. Остановить можно в любой момент клавишами Ctrl+C.'
	M_q_host='IP-адрес сервера'
	M_q_auth='Как входить на сервер? 1 — пароль root, 2 — файл ключа'
	M_q_key='Путь к файлу приватного ключа'
	M_q_name='Короткое имя сервера (потом вы будете набирать "ssh ИМЯ")'
	M_q_domain='Домен для сайтов'
	M_q_email='Ваша почта'
	M_q_public_admin='Открыть панель управления из интернета по адресу https://admin.%s? Это удобнее, но менее безопасно: панель сможет найти кто угодно. [y/N]'
	M_bad_host='Это не похоже на IP-адрес или имя сервера: %s'
	M_bad_port='Порт SSH должен быть числом от 1 до 65535: %s'
	M_bad_key='Файл ключа не найден или недоступен: %s'
	M_bad_name='Имя может содержать маленькие латинские буквы, цифры и "-", начинаться с буквы, не длиннее 32 символов: %s'
	M_name_taken='Имя "%s" уже занято в %s другим сервером. Выберите другое.'
	M_bad_domain='Это не похоже на домен (вида sites.example.com): %s'
	M_bad_email='Это не похоже на адрес e-mail: %s'
	M_missing='Нет ответа: %s (спросить негде, нет терминала; передайте параметром, см. --help)'
	M_summary='Будет установлено:
  сервер:  %s
  имя:     %s
  домен:   %s
  e-mail:  %s'
	M_confirm='Продолжить? [Y/n]'
	M_confirm_short='Ставлю Dropsite на %s для %s? [Y/n]'
	M_cancelled='Отменено. Ничего не изменилось.'
	M_need_tool='На этом компьютере не найдена программа "%s".'
	M_installing='Устанавливаю Dropsite. Это 10–20 минут, не закрывайте окно.'
	M_s_connect='Подключаюсь к серверу'
	M_s_connect_done='Подключился к серверу'
	M_s_install='Ставлю Dropsite на сервер'
	M_s_install_done='Установил Dropsite'
	M_s_dns='Жду домен'
	M_s_dns_done='Домен работает'
	M_s_https='Включаю HTTPS'
	M_s_https_done='Включил HTTPS'
	M_step_key='Создаю ключ для этого сервера: %s'
	M_step_password='Когда спросят, введите пароль root от сервера (при вводе он не виден).'
	M_step_userkey='Подключаюсь с вашим файлом ключа.'
	M_key_failed='Не удалось войти на %s. Проверьте IP-адрес и пароль (или ключ) и запустите команду ещё раз.'
	M_hostkey_changed='Сервер выглядит иначе, чем при прошлом подключении (например, его переустановили). Если так и есть, удалите старую запись:
  ssh-keygen -R %s
и запустите команду ещё раз.'
	M_step_check='Проверяю систему сервера'
	M_os_bad='На сервере "%s". Поддерживаются: Ubuntu 22.04 и новее, Debian 12 и новее. Переустановите сервер с одной из этих систем в панели хостинга и запустите команду ещё раз.'
	M_step_sshcfg='Подключение сохранено как "%s" в %s (веб-интерфейс на локальном порту %s)'
	M_step_bootstrap='Устанавливаю Dropsite на сервер: 10-20 минут. Не закрывайте это окно.'
	M_bootstrap_failed='Установка на сервере остановилась с ошибкой.'
	M_server_log='Журнал на сервере: ssh %s tail -n 100 /var/log/dropsite-install.log'
	M_log_tail='Последние строки журнала:'
	M_log_at='Весь журнал: %s'
	M_rerun='Запустите ту же команду ещё раз: готовые шаги пропускаются.'
	M_download_failed='Не удалось скачать %s'
	M_dns_title='Теперь подключите домен. Там, где вы купили %s, откройте настройки DNS и создайте записи:'
	M_dns_short='Теперь подключите домен. Там, где вы купили %s, откройте настройки DNS и добавьте записи:'
	M_dns_head='  тип    имя    значение'
	M_dns_names='  ("@" означает сам домен; полные имена: %s и *.%s)'
	M_dns_hint='Если запись с таким именем уже есть, измените её, а не добавляйте вторую.
Изменения DNS вступают в силу от нескольких минут до часа. В Cloudflare выберите "DNS only" (серое облако).'
	M_dns_cloudflare='В Cloudflare выберите «DNS only» (серое облако).'
	M_q_wait='Подождать здесь, пока домен заработает? [Y/n]'
	M_dns_waiting='Проверяю каждые 10 секунд, не дольше %s минут. Ctrl+C прекращает ожидание, ничего не сломается.'
	M_dns_ok='Домен указывает на ваш сервер.'
	M_dns_aaaa_missing='Записей AAAA (IPv6) пока нет: сайты работают по IPv4. Добавьте их, когда сможете.'
	M_dns_aaaa_wrong='Записи AAAA (IPv6) указывают не туда: %s. Исправьте их, иначе часть посетителей не откроет сайты.'
	M_https_waiting='Жду HTTPS-сертификат для %s'
	M_https_ok='HTTPS работает: %s'
	M_dns_timeout='Домен пока не указывает на сервер. Проверьте записи выше.'
	M_https_timeout='HTTPS-сертификат пока не готов. Обычно он появляется через несколько минут после того, как заработал DNS.'
	M_check_later='Чтобы проверить позже, выполните:
  %s'
	M_interrupted='Остановлено. Сервер установлен и продолжает работать.'
	M_skip_dns='Проверка DNS пропущена.'
	M_done='Готово! Dropsite установлен.'
	M_open_admin='Как открыть веб-интерфейс:
  1. В терминале выполните:   ssh %s
     и не закрывайте это окно.
  2. В браузере откройте:   http://localhost:%s'
	M_login='Логин:   %s
Пароль:  %s
Сохраните пароль в менеджере паролей и смените его после входа (страница аккаунта).'
	M_login_old='Логин:   %s
Пароль:  тот, что был при первой установке. Забыли? Выполните:  ssh %s dropsite-admin-password'
	M_connect_ai='Подключить ChatGPT или Claude: в веб-интерфейсе откройте "Подключить ИИ" и следуйте шагам.'
	M_open_public='Веб-интерфейс также открыт из интернета: %s
Это потенциальная угроза: используйте надёжный уникальный пароль и выключите доступ в настройках, когда он не нужен.'
	M_update='Обновить Dropsite потом:   ssh %s dropsite-update'
	M_sites_at='Ваши сайты будут по адресам https://<имя>.%s'
	M_no_install='Сервер Dropsite с именем "%s" не найден в %s. Сначала запустите установку без --check-dns.'
	M_done_short='Готово!'
	M_final_ssh='1. Выполните: ssh %s   (окно не закрывайте)'
	M_final_open='2. Откройте: http://localhost:%s'
	M_final_public='   Или откуда угодно: %s   (открыта из интернета: нужен надёжный пароль; выключите в настройках, когда не нужно)'
	M_final_login='   Логин: %s   Пароль: %s   (сохраните пароль)'
	M_final_login_old='   Логин: %s   Пароль: прежний (забыли? ssh %s dropsite-admin-password)'
	M_final_next='Дальше откройте «Начало работы» — там всё по шагам.'
	M_final_connect='   Подключить чат: вставьте %s как адрес сервера MCP (сработает один раз, в течение часа)'
	M_connect_url='Подключить ChatGPT или Claude сразу: вставьте этот адрес в чат как адрес сервера MCP
и нажмите «Разрешить», код не нужен. Сработает один раз, в течение часа; новый выпустит
ssh %s dropsite-connect.
  %s'
}

msgs_zh() {
	M_title='Dropsite：您自己的网站服务器'
	M_intro='此脚本会在一台全新租用的服务器（VPS，Ubuntu 22.04 及以上或 Debian 12 及以上）上安装 Dropsite。
需要准备：服务器 IP 地址和 root 密码（来自主机商的邮件）、一个域名和一个电子邮箱。
大约需要 10-20 分钟。随时可以按 Ctrl+C 停止。'
	M_q_host='服务器 IP 地址'
	M_q_auth='如何登录服务器？1 = root 密码，2 = 密钥文件'
	M_q_key='私钥文件路径'
	M_q_name='服务器的简短名称（之后输入 "ssh 名称" 即可连接）'
	M_q_domain='网站使用的域名'
	M_q_email='您的电子邮箱'
	M_q_public_admin='是否通过 https://admin.%s 从互联网打开管理面板？这样更方便，但安全性更低：任何人都能找到该面板。[y/N]'
	M_bad_host='这看起来不像 IP 地址或服务器名称：%s'
	M_bad_port='SSH 端口必须是 1 到 65535 之间的数字：%s'
	M_bad_key='找不到私钥文件或无法读取：%s'
	M_bad_name='名称只能包含小写拉丁字母、数字和 "-"，必须以字母开头，最多 32 个字符：%s'
	M_name_taken='名称 "%s" 已在 %s 中被另一台服务器使用，请换一个名称。'
	M_bad_domain='这看起来不像域名（例如 sites.example.com）：%s'
	M_bad_email='这看起来不像电子邮箱地址：%s'
	M_missing='缺少答案：%s（没有终端可以提问；请用参数传入，见 --help）'
	M_summary='即将安装：
  服务器：%s
  名称：  %s
  域名：  %s
  邮箱：  %s'
	M_confirm='继续吗？[Y/n]'
	M_confirm_short='在 %s 上为 %s 安装 Dropsite 吗？[Y/n]'
	M_cancelled='已取消，没有做任何更改。'
	M_need_tool='此电脑上找不到所需的程序 "%s"。'
	M_installing='正在安装 Dropsite，大约需要 10–20 分钟，请不要关闭此窗口。'
	M_s_connect='正在连接服务器'
	M_s_connect_done='已连接服务器'
	M_s_install='正在服务器上安装 Dropsite'
	M_s_install_done='Dropsite 已安装'
	M_s_dns='正在等待域名生效'
	M_s_dns_done='域名已生效'
	M_s_https='正在启用 HTTPS'
	M_s_https_done='HTTPS 已启用'
	M_step_key='正在为此服务器创建密钥：%s'
	M_step_password='出现提示时请输入服务器的 root 密码（输入时不会显示）。'
	M_step_userkey='正在使用您的私钥文件连接。'
	M_key_failed='无法登录 %s。请检查 IP 地址和密码（或私钥），然后重新运行命令。'
	M_hostkey_changed='服务器的身份与上次连接时不同（例如服务器被重装过）。如果确实如此，请先删除旧记录：
  ssh-keygen -R %s
然后重新运行命令。'
	M_step_check='正在检查服务器系统'
	M_os_bad='服务器运行的是 "%s"。支持的系统：Ubuntu 22.04 及以上、Debian 12 及以上。请在主机商控制面板中重装为其中之一，然后重新运行命令。'
	M_step_sshcfg='连接已保存为 "%s"，位于 %s（管理界面使用本地端口 %s）'
	M_step_bootstrap='正在服务器上安装 Dropsite：需要 10-20 分钟，请不要关闭此窗口。'
	M_bootstrap_failed='服务器上的安装因错误而停止。'
	M_server_log='服务器日志：ssh %s tail -n 100 /var/log/dropsite-install.log'
	M_log_tail='日志的最后几行：'
	M_log_at='完整日志：%s'
	M_rerun='请重新运行同一条命令：已完成的步骤会被跳过。'
	M_download_failed='无法下载 %s'
	M_dns_title='现在连接您的域名。在购买 %s 的地方打开 DNS 设置，创建以下记录：'
	M_dns_short='现在连接域名。在购买 %s 的地方打开 DNS 设置，添加以下记录：'
	M_dns_head='  类型   名称   值'
	M_dns_names='  （"@" 表示域名本身；完整名称：%s 和 *.%s）'
	M_dns_hint='如果已存在同名记录，请修改它，而不是再添加一条。
DNS 更改需要几分钟到一小时生效。使用 Cloudflare 时请选择 "DNS only"（灰色云朵）。'
	M_dns_cloudflare='使用 Cloudflare 时请选择 "DNS only"（灰色云朵）。'
	M_q_wait='在这里等待域名生效吗？[Y/n]'
	M_dns_waiting='每 10 秒检查一次，最多 %s 分钟。按 Ctrl+C 停止等待，不会造成任何损坏。'
	M_dns_ok='域名已指向您的服务器。'
	M_dns_aaaa_missing='还没有 AAAA（IPv6）记录：网站仍可通过 IPv4 访问。方便时请添加。'
	M_dns_aaaa_wrong='AAAA（IPv6）记录指向了别处：%s。请修正，否则部分访问者无法打开您的网站。'
	M_https_waiting='正在等待 %s 的 HTTPS 证书'
	M_https_ok='HTTPS 已可用：%s'
	M_dns_timeout='域名还没有指向服务器。请检查上面的记录。'
	M_https_timeout='HTTPS 证书还没有准备好。通常在 DNS 生效后几分钟内出现。'
	M_check_later='稍后再次检查，请运行：
  %s'
	M_interrupted='已停止。服务器已安装并继续运行。'
	M_skip_dns='已跳过 DNS 检查。'
	M_done='完成！Dropsite 已安装。'
	M_open_admin='打开管理界面：
  1. 在终端中运行：   ssh %s
     并保持该窗口打开。
  2. 在浏览器中打开：   http://localhost:%s'
	M_login='登录名：%s
密码：  %s
请把密码保存到密码管理器中，并在登录后修改（账户页面）。'
	M_login_old='登录名：%s
密码：  首次安装时的密码。忘记了？运行：  ssh %s dropsite-admin-password'
	M_connect_ai='连接 ChatGPT 或 Claude：在管理界面中打开 "Connect AI" 页面并按步骤操作。'
	M_open_public='管理界面也已向互联网开放：%s
这是一个潜在的威胁：请使用强且唯一的密码，不需要时在设置中关闭。'
	M_update='以后更新 Dropsite：   ssh %s dropsite-update'
	M_sites_at='您的网站地址将是 https://<名称>.%s'
	M_no_install='找不到名为 "%s" 的 Dropsite 服务器（%s）。请先在不带 --check-dns 的情况下运行安装程序。'
	M_done_short='完成！'
	M_final_ssh='1. 运行：ssh %s   （保持该窗口打开）'
	M_final_open='2. 打开：http://localhost:%s'
	M_final_public='   或在任何地方打开：%s   （已向互联网开放：请使用强密码；不需要时在设置中关闭）'
	M_final_login='   登录名：%s   密码：%s   （请保存密码）'
	M_final_login_old='   登录名：%s   密码：首次安装时的密码（忘记了？ssh %s dropsite-admin-password）'
	M_final_next='接下来打开“入门指南”页面，按步骤完成剩余设置。'
	M_final_connect='   连接聊天应用：把 %s 作为 MCP 服务器地址粘贴（一小时内可用一次）'
	M_connect_url='立即连接 ChatGPT 或 Claude：把这个地址作为 MCP 服务器地址粘贴到聊天应用中，
然后点击“允许”，无需连接码。一小时内可用一次；之后可用 ssh %s dropsite-connect 获取新的地址。
  %s'
}

detect_lang() {
	l=${DROPSITE_LANG:-${LC_ALL:-${LC_MESSAGES:-${LANG:-}}}}
	case "$l" in
	ru* | RU*) echo ru ;;
	zh* | ZH*) echo zh ;;
	*) echo en ;;
	esac
}

# ---------------------------------------------------------------------------
# Output and input.

# Three kinds of output:
#   say/sayf    on the screen only (questions, the final block with the password);
#   tell        on the screen and in the log;
#   vsayf       in the log, and on the screen with --verbose only.
# The log never gets the root password (ssh asks for it on the terminal
# itself) nor the private keys; the generated admin password stays out too.

VERBOSE=0
LOG_FILE=""
STEP_MSG=""     # the step in progress
STEP_INLINE=0   # 1: a step is one terminal line, "…" turned into "✓" in place
STEP_OPEN=0     # 1: that line has no newline yet

# break_line ends a step line left open, before anything else is printed.
break_line() {
	if [ "$STEP_OPEN" = 1 ]; then
		printf '\n' >&2
		STEP_OPEN=0
	fi
}

say() {
	break_line
	printf '%s\n' "$*" >&2
}

# msgf FORMAT ARGS...: a message table entry with its %s filled in.
msgf() {
	f=$1
	shift
	# shellcheck disable=SC2059 # formats come from the message tables
	printf "$f" "$@"
}

# sayf FORMAT ARGS...: print a message table entry.
sayf() { say "$(msgf "$@")"; }

log_line() {
	[ -z "$LOG_FILE" ] || printf '%s\n' "$*" >>"$LOG_FILE"
}

tell() {
	m=$(msgf "$@")
	say "$m"
	log_line "$m"
}

vsayf() {
	m=$(msgf "$@")
	log_line "$m"
	[ "$VERBOSE" != 1 ] || say "$m"
}

# mark SYMBOL TEXT: the finished line of a step.
mark() {
	log_line "$1 $2"
	if [ "$STEP_OPEN" = 1 ]; then
		printf '\r  %s %s\033[K\n' "$1" "$2" >&2
		STEP_OPEN=0
	else
		printf '  %s %s\n' "$1" "$2" >&2
	fi
	STEP_MSG=""
}

# step_begin TEXT: "… TEXT" on a terminal (replaced by step_done), only
# in the log otherwise; with --verbose a line of its own.
step_begin() {
	STEP_MSG=$1
	log_line "... $1"
	break_line
	if [ "$STEP_INLINE" = 1 ]; then
		printf '  … %s' "$1" >&2
		STEP_OPEN=1
	elif [ "$VERBOSE" = 1 ]; then
		printf '  … %s\n' "$1" >&2
	fi
}

step_done() { mark ✓ "$1"; }

# step_fail marks the step in progress as failed.
step_fail() {
	[ -z "$STEP_MSG" ] || mark ✗ "$STEP_MSG"
}

# step_tick: one more dot after the step in progress, to show it is alive.
step_tick() {
	[ "$STEP_OPEN" != 1 ] || printf '.' >&2
}

die() {
	step_fail
	tell "$@"
	[ -z "$LOG_FILE" ] || sayf "$M_log_at" "$LOG_FILE"
	exit 1
}

# fail_with_log FORMAT ARGS...: a failure explained by the log: one plain
# sentence, the last lines of the log, where it is and how to go on.
fail_with_log() {
	last=""
	[ -z "$LOG_FILE" ] || [ ! -s "$LOG_FILE" ] || last=$(tail -n 15 "$LOG_FILE")
	step_fail
	say ""
	sayf "$@"
	if [ -n "$last" ]; then
		say ""
		sayf "$M_log_tail"
		printf '%s\n' "$last" | sed 's/^/  /' >&2
		say ""
		sayf "$M_log_at" "$LOG_FILE"
	fi
	sayf "$M_rerun"
	log_line "$(msgf "$@")"
	exit 1
}

# log_open: ~/.dropsite/install-<name>.log, mode 600, one section per run.
log_open() {
	d="$HOME/.dropsite"
	if mkdir -p "$d" 2>/dev/null && chmod 700 "$d" &&
		(umask 077 && : >>"$d/install-$NAME.log") 2>/dev/null; then
		LOG_FILE="$d/install-$NAME.log"
		chmod 600 "$LOG_FILE"
	else
		LOG_FILE=""
		return 0
	fi
	log_line ""
	log_line "===== $(date -u +%FT%TZ) Dropsite install-server.sh ====="
}

# scrub_log SECRET: replace SECRET in the log (defence in depth: the server
# never prints the generated password to the log stream).
scrub_log() {
	[ -n "$1" ] && [ -n "$LOG_FILE" ] && [ -f "$LOG_FILE" ] || return 0
	grep -qF -- "$1" "$LOG_FILE" || return 0
	t="$LOG_FILE.tmp"
	awk -v s="$1" '{
		out = ""
		while ((i = index($0, s)) > 0) { out = out substr($0, 1, i - 1) "********"; $0 = substr($0, i + length(s)) }
		print out $0
	}' "$LOG_FILE" >"$t"
	cat "$t" >"$LOG_FILE"
	rm -f "$t"
}

bad_input() {
	sayf "$@"
	exit 2
}

HAVE_TTY=0
check_tty() {
	if [ "${DROPSITE_NO_TTY:-}" != 1 ] && (: </dev/tty) 2>/dev/null; then
		HAVE_TTY=1
	fi
}

# ask VAR PROMPT [DEFAULT]: read an answer from the terminal (stdin may be
# the script itself when it runs as "curl ... | sh").
ask() {
	if [ "$HAVE_TTY" != 1 ]; then
		bad_input "$M_missing" "$1"
	fi
	break_line
	if [ -n "${3:-}" ]; then
		printf '%s [%s]: ' "$2" "$3" >&2
	else
		printf '%s: ' "$2" >&2
	fi
	ans=""
	IFS= read -r ans </dev/tty || ans=""
	[ -n "$ans" ] || ans=${3:-}
	eval "$1=\$ans"
}

# yes_no PROMPT: true for yes (the default).
yes_no() {
	if [ "${ASSUME_YES:-0}" = 1 ] || [ "$HAVE_TTY" != 1 ]; then
		return 0
	fi
	break_line
	printf '%s ' "$1" >&2
	ans=""
	IFS= read -r ans </dev/tty || ans=""
	case "$ans" in
	"" | y | Y | yes | YES | Yes | д | Д | да | Да | 是 | 好) return 0 ;;
	*) return 1 ;;
	esac
}

# no_yes PROMPT: true for yes; the default (Enter, no terminal, --yes) is no.
no_yes() {
	if [ "${ASSUME_YES:-0}" = 1 ] || [ "$HAVE_TTY" != 1 ]; then
		return 1
	fi
	break_line
	printf '%s ' "$1" >&2
	ans=""
	IFS= read -r ans </dev/tty || ans=""
	case "$ans" in
	y | Y | yes | YES | Yes | д | Д | да | Да | 是 | 好) return 0 ;;
	*) return 1 ;;
	esac
}

# ---------------------------------------------------------------------------
# Validation.

DOMAIN_RE='^([a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\.)+([a-z]{2,63}|xn--[a-z0-9-]{1,59})$'

lower() { printf '%s' "$1" | tr '[:upper:]' '[:lower:]'; }

is_ipv4() {
	printf '%s' "$1" | grep -Eq '^([0-9]{1,3}\.){3}[0-9]{1,3}$' || return 1
	printf '%s' "$1" | awk -F. '{ for (i = 1; i <= 4; i++) if ($i > 255) exit 1 }'
}

is_ipv6() { printf '%s' "$1" | grep -Eq '^[0-9a-fA-F]{0,4}(:[0-9a-fA-F]{0,4}){2,7}$'; }

valid_domain() {
	[ "${#1}" -le 253 ] && printf '%s' "$1" | grep -Eq "$DOMAIN_RE"
}

valid_host() { is_ipv4 "$1" || is_ipv6 "$1" || valid_domain "$1"; }

valid_email() {
	case "$1" in
	*@*@* | *@) return 1 ;;
	esac
	local_part=${1%@*}
	domain_part=${1##*@}
	[ "$local_part" != "$1" ] &&
		printf '%s' "$local_part" | grep -Eq '^[A-Za-z0-9._%+-]{1,64}$' &&
		valid_domain "$domain_part"
}

valid_name() { printf '%s' "$1" | grep -Eq '^[a-z][a-z0-9-]{0,31}$'; }

valid_port() {
	printf '%s' "$1" | grep -Eq '^[0-9]{1,5}$' && [ "$1" -ge 1 ] && [ "$1" -le 65535 ]
}

# normalize_domain strips a scheme, a path and a trailing dot, lowercases.
normalize_domain() {
	d=$(lower "$1")
	d=${d#http://}
	d=${d#https://}
	d=${d%%/*}
	d=${d%.}
	printf '%s' "$d"
}

expand_path() {
	# shellcheck disable=SC2088 # a literal tilde typed by the user
	case "$1" in
	"~") printf '%s' "$HOME" ;;
	"~/"*) printf '%s/%s' "$HOME" "${1#"~/"}" ;;
	*) printf '%s' "$1" ;;
	esac
}

# ---------------------------------------------------------------------------
# ~/.ssh/config: one block per server between BEGIN/END markers.

ssh_dir() { printf '%s/.ssh' "$HOME"; }
ssh_config() { printf '%s/config' "$(ssh_dir)"; }

ensure_ssh_dir() {
	d=$(ssh_dir)
	if [ ! -d "$d" ]; then
		mkdir -p "$d"
		chmod 700 "$d"
	fi
	f=$(ssh_config)
	if [ ! -e "$f" ]; then
		(umask 077 && : >"$f")
	fi
}

block_begin() { printf '# BEGIN dropsite %s' "$1"; }
block_end() { printf '# END dropsite %s' "$1"; }

# config_without_block NAME: the config with this server's block removed.
config_without_block() {
	f=$(ssh_config)
	[ -f "$f" ] || return 0
	awk -v b="$(block_begin "$1")" -v e="$(block_end "$1")" '
		$0 == b { skip = 1; next }
		skip && $0 == e { skip = 0; next }
		!skip' "$f"
}

# block_value NAME KEY: a value from this server's block, e.g. HostName.
block_value() {
	f=$(ssh_config)
	[ -f "$f" ] || return 0
	awk -v b="$(block_begin "$1")" -v e="$(block_end "$1")" -v k="$(lower "$2")" '
		$0 == b { inside = 1; next }
		$0 == e { inside = 0 }
		inside && tolower($1) == k { print $2; exit }' "$f"
}

has_block() {
	f=$(ssh_config)
	[ -f "$f" ] && grep -qxF "$(block_begin "$1")" "$f"
}

# host_taken NAME: another Host line (outside our block) already uses NAME.
host_taken() {
	config_without_block "$1" | awk -v n="$1" '
		tolower($1) == "host" { for (i = 2; i <= NF; i++) if ($i == n) found = 1 }
		END { exit !found }'
}

# used_forward_ports NAME: local ports of LocalForward lines of other hosts.
used_forward_ports() {
	config_without_block "$1" | awk 'tolower($1) == "localforward" { print $2 }' | sed 's/.*://'
}

# port_listening PORT: something accepts connections on 127.0.0.1:PORT.
# curl exits with 7 exactly when the connection is refused.
port_listening() {
	rc=0
	curl -s -o /dev/null --max-time 2 "http://127.0.0.1:$1/" 2>/dev/null || rc=$?
	[ "$rc" != 7 ]
}

# choose_local_port NAME: keep the port of an existing block, otherwise the
# first port from 8082 that no other LocalForward uses and nothing listens on.
choose_local_port() {
	existing=$(block_value "$1" LocalForward)
	if [ -n "$existing" ]; then
		printf '%s' "$existing"
		return
	fi
	used=" $(used_forward_ports "$1" | tr '\n' ' ') "
	p=$FIRST_LOCAL_PORT
	while [ "$p" -lt $((FIRST_LOCAL_PORT + 1000)) ]; do
		case "$used" in
		*" $p "*) ;;
		*) port_listening "$p" || {
			printf '%s' "$p"
			return
		} ;;
		esac
		p=$((p + 1))
	done
	die "no free local port from %s" "$FIRST_LOCAL_PORT"
}

# quote_cfg VALUE: quote an ssh_config value that contains spaces.
quote_cfg() {
	case "$1" in
	*" "*) printf '"%s"' "$1" ;;
	*) printf '%s' "$1" ;;
	esac
}

# write_ssh_config NAME HOST PORT KEY LOCAL_PORT: insert or replace the block.
# A new block goes to the top: the first matching value wins in ssh_config,
# so a later "Host *" cannot override User or IdentityFile.
write_ssh_config() {
	ensure_ssh_dir
	f=$(ssh_config)
	work=$(mktemp "$(ssh_dir)/config.dropsite.XXXXXX")
	blk="$work.block"
	{
		block_begin "$1"
		printf '\nHost %s\n' "$1"
		printf '    HostName %s\n' "$2"
		[ "$3" = 22 ] || printf '    Port %s\n' "$3"
		printf '    User root\n'
		printf '    IdentityFile %s\n' "$(quote_cfg "$4")"
		printf '    IdentitiesOnly yes\n'
		printf '    LocalForward %s 127.0.0.1:%s\n' "$5" "$ADMIN_REMOTE_PORT"
		printf '    ServerAliveInterval 30\n'
		printf '    ServerAliveCountMax 6\n'
		block_end "$1"
		printf '\n'
	} >"$blk"
	if has_block "$1"; then
		awk -v b="$(block_begin "$1")" -v e="$(block_end "$1")" -v blk="$blk" '
			$0 == b { while ((getline line < blk) > 0) print line; skip = 1; next }
			skip && $0 == e { skip = 0; next }
			!skip' "$f" >"$work"
	else
		{
			cat "$blk"
			if [ -s "$f" ]; then
				printf '\n'
				cat "$f"
			fi
		} >"$work"
	fi
	# Write through (not mv): keeps a symlinked config and its permissions.
	cat "$work" >"$f"
	rm -f "$work" "$blk"
	chmod 600 "$f" 2>/dev/null || true
}

# ---------------------------------------------------------------------------
# SSH to the server. -F /dev/null: the user's own ssh_config never changes
# how the installer connects; known_hosts is the user's.

HOST="" PORT=22 KEY="" NAME=""

base_ssh() {
	ssh -F /dev/null -p "$PORT" \
		-o "UserKnownHostsFile=\"$(ssh_dir)/known_hosts\"" \
		-o StrictHostKeyChecking=accept-new \
		-o ConnectTimeout=20 -o ServerAliveInterval=30 -o ServerAliveCountMax=20 \
		-o LogLevel=ERROR "$@"
}

# kssh ARGS...: ssh as root with the dedicated key only.
kssh() {
	base_ssh -i "$KEY" -o IdentitiesOnly=yes -o BatchMode=yes \
		-o PasswordAuthentication=no -o KbdInteractiveAuthentication=no "root@$HOST" "$@"
}

key_works() { kssh true </dev/null >/dev/null 2>&1; }

# install_key: append the dedicated public key to root's authorized_keys
# in one ssh call, signing in with the password (typed into ssh) or the
# user's key file.
install_key() {
	pub=$(cat "$KEY.pub")
	cmd="umask 077 && mkdir -p ~/.ssh && touch ~/.ssh/authorized_keys && { grep -qxF '$pub' ~/.ssh/authorized_keys || printf '%s\n' '$pub' >>~/.ssh/authorized_keys; }"
	err=$(mktemp)
	rc=0
	if [ -n "$USER_KEY" ]; then
		vsayf "$M_step_userkey"
		base_ssh -i "$USER_KEY" -o IdentitiesOnly=yes -o PasswordAuthentication=no \
			"root@$HOST" "$cmd" </dev/null 2>"$err" || rc=$?
	else
		# The person has to act here, so this line is always shown.
		tell "$M_step_password"
		base_ssh -o PubkeyAuthentication=no -o PreferredAuthentications=keyboard-interactive,password \
			-o NumberOfPasswordPrompts=3 "root@$HOST" "$cmd" </dev/null 2>"$err" || rc=$?
	fi
	if [ "$rc" != 0 ]; then
		# ssh's own messages: into the log, on the screen with --verbose.
		log_line "$(cat "$err")"
		[ "$VERBOSE" != 1 ] || cat "$err" >&2
		if grep -q 'REMOTE HOST IDENTIFICATION HAS CHANGED\|Host key verification failed' "$err"; then
			rm -f "$err"
			die "$M_hostkey_changed" "$([ "$PORT" = 22 ] && printf '%s' "$HOST" || printf '[%s]:%s' "$HOST" "$PORT")"
		fi
		rm -f "$err"
		die "$M_key_failed" "$HOST"
	fi
	rm -f "$err"
}

# q VALUE: single-quote for the remote shell.
q() { printf "'%s'" "$(printf '%s' "$1" | sed "s/'/'\\\\''/g")"; }

# ---------------------------------------------------------------------------
# Steps.

TMP=""
cleanup() {
	break_line
	[ -z "$TMP" ] || rm -rf "$TMP"
}

need_tools() {
	for t in ssh ssh-keygen curl awk sed grep; do
		command -v "$t" >/dev/null 2>&1 || die "$M_need_tool" "$t"
	done
}

usage() {
	if [ -f "$0" ]; then
		sed -n '2,/^set -eu$/p' "$0" | sed '$d' | sed 's/^# \{0,1\}//' >&2
	else
		say "Options: see the header of $INSTALL_URL"
	fi
}

parse_args() {
	HOST=${DROPSITE_HOST:-}
	PORT=${DROPSITE_SSH_PORT:-22}
	USER_KEY=${DROPSITE_SSH_KEY:-}
	NAME=${DROPSITE_NAME:-}
	DOMAIN=${DROPSITE_DOMAIN:-}
	EMAIL=${DROPSITE_EMAIL:-}
	SKIP_DNS=${DROPSITE_SKIP_DNS:-0}
	CHECK_DNS=${DROPSITE_CHECK_DNS:-0}
	ASSUME_YES=${DROPSITE_YES:-0}
	VERBOSE=${DROPSITE_VERBOSE:-0}
	PUBLIC_ADMIN=${DROPSITE_PUBLIC_ADMIN:-}
	LANG_OPT=""
	WANT_HELP=0
	while [ $# -gt 0 ]; do
		case "$1" in
		--host | --port | --key | --name | --domain | --email | --lang)
			[ $# -ge 2 ] || bad_input 'option %s needs a value' "$1"
			opt=$1 val=$2
			shift 2
			;;
		--host=* | --port=* | --key=* | --name=* | --domain=* | --email=* | --lang=*)
			opt=${1%%=*} val=${1#*=}
			shift
			;;
		--skip-dns) SKIP_DNS=1 && shift && continue ;;
		--public-admin) PUBLIC_ADMIN=1 && shift && continue ;;
		--no-public-admin) PUBLIC_ADMIN=0 && shift && continue ;;
		--check-dns) CHECK_DNS=1 && shift && continue ;;
		--yes | -y) ASSUME_YES=1 && shift && continue ;;
		--verbose | -v) VERBOSE=1 && shift && continue ;;
		--help | -h) WANT_HELP=1 && shift && continue ;;
		*) bad_input 'unknown option: %s (see --help)' "$1" ;;
		esac
		case "$opt" in
		--host) HOST=$val ;;
		--port) PORT=$val ;;
		--key) USER_KEY=$val ;;
		--name) NAME=$val ;;
		--domain) DOMAIN=$val ;;
		--email) EMAIL=$val ;;
		--lang)
			case "$val" in
			en | ru | zh) LANG_OPT=$val ;;
			*) bad_input 'unknown language: %s (use en, ru or zh)' "$val" ;;
			esac
			;;
		esac
	done
	[ "$VERBOSE" = 1 ] || VERBOSE=0
	case "$PUBLIC_ADMIN" in
	"" | 0 | 1) ;;
	*) bad_input 'DROPSITE_PUBLIC_ADMIN must be 1 or 0: %s' "$PUBLIC_ADMIN" ;;
	esac
}

# pick_lang: --lang, else DROPSITE_LANG, else the locale.
pick_lang() {
	if [ -n "${LANG_OPT:-}" ]; then
		printf '%s\n' "$LANG_OPT"
	else
		detect_lang
	fi
}

# collect_inputs asks for whatever is missing and validates everything.
collect_inputs() {
	while [ -z "$HOST" ] || ! valid_host "$HOST"; do
		[ -z "$HOST" ] || { sayf "$M_bad_host" "$HOST"; [ "$HAVE_TTY" = 1 ] || exit 2; }
		ask HOST "$M_q_host"
		HOST=$(printf '%s' "$HOST" | tr -d ' []')
	done
	valid_port "$PORT" || bad_input "$M_bad_port" "$PORT"
	if [ -z "$USER_KEY" ] && [ "$HAVE_TTY" = 1 ] && [ "$ASSUME_YES" != 1 ]; then
		auth=""
		ask auth "$M_q_auth" 1
		if [ "$auth" = 2 ]; then
			while [ -z "$USER_KEY" ] || [ ! -r "$USER_KEY" ]; do
				[ -z "$USER_KEY" ] || sayf "$M_bad_key" "$USER_KEY"
				ask USER_KEY "$M_q_key" "$(ssh_dir)/id_ed25519"
				USER_KEY=$(expand_path "$USER_KEY")
			done
		fi
	fi
	if [ -n "$USER_KEY" ]; then
		USER_KEY=$(expand_path "$USER_KEY")
		[ -r "$USER_KEY" ] || bad_input "$M_bad_key" "$USER_KEY"
	fi
	while [ -z "$DOMAIN" ] || ! valid_domain "$DOMAIN"; do
		[ -z "$DOMAIN" ] || { sayf "$M_bad_domain" "$DOMAIN"; [ "$HAVE_TTY" = 1 ] || exit 2; }
		ask DOMAIN "$M_q_domain"
		DOMAIN=$(normalize_domain "$DOMAIN")
	done
	DOMAIN=$(normalize_domain "$DOMAIN")
	valid_domain "$DOMAIN" || bad_input "$M_bad_domain" "$DOMAIN"
	choose_name
	while [ -z "$EMAIL" ] || ! valid_email "$EMAIL"; do
		[ -z "$EMAIL" ] || { sayf "$M_bad_email" "$EMAIL"; [ "$HAVE_TTY" = 1 ] || exit 2; }
		ask EMAIL "$M_q_email"
		EMAIL=$(printf '%s' "$EMAIL" | tr -d ' ')
	done
	EMAIL="${EMAIL%@*}@$(lower "${EMAIL##*@}")"
	ask_public_admin
}

# ask_public_admin: open the admin interface to the internet? One short
# question, default No. --public-admin / --no-public-admin (or
# DROPSITE_PUBLIC_ADMIN) answer it; without a terminal or with --yes it is
# not asked and the server keeps its current choice (off on a new server).
ask_public_admin() {
	[ -z "$PUBLIC_ADMIN" ] || return 0
	[ "$HAVE_TTY" = 1 ] && [ "$ASSUME_YES" != 1 ] || return 0
	if no_yes "$(msgf "$M_q_public_admin" "$DOMAIN")"; then
		PUBLIC_ADMIN=1
	else
		PUBLIC_ADMIN=0
	fi
}

# name_from_domain: the first label of the domain (may be an invalid name).
name_from_domain() { printf '%s' "${DOMAIN%%.*}" | tr -cd 'a-z0-9-' | cut -c1-32; }

# choose_name sets NAME. Without --name it comes from the domain silently;
# the question is asked only when that name is taken by another Host in
# ~/.ssh/config or is not a valid name, or with --verbose. Without a
# terminal a free alternative (dropsite-<name>) is used instead.
choose_name() {
	if [ -z "$NAME" ]; then
		n=$(name_from_domain)
		if [ "$VERBOSE" != 1 ] && valid_name "$n" && ! host_taken "$n"; then
			NAME=$n
			return 0
		fi
		if [ "$HAVE_TTY" != 1 ]; then
			NAME=$(default_name)
			valid_name "$NAME" && ! host_taken "$NAME" && return 0
			bad_input "$M_name_taken" "$NAME" "$(ssh_config)"
		fi
		! valid_name "$n" || ! host_taken "$n" || sayf "$M_name_taken" "$n" "$(ssh_config)"
	fi
	NAME=$(lower "$NAME")
	while [ -z "$NAME" ] || ! valid_name "$NAME" || host_taken "$NAME"; do
		if [ -n "$NAME" ]; then
			if ! valid_name "$NAME"; then sayf "$M_bad_name" "$NAME"; else sayf "$M_name_taken" "$NAME" "$(ssh_config)"; fi
			[ "$HAVE_TTY" = 1 ] || exit 2
		fi
		ask NAME "$M_q_name" "$(default_name)"
		NAME=$(lower "$NAME")
	done
}

# default_name: the first label of the domain, made a valid name.
default_name() {
	n=$(name_from_domain)
	case "$n" in
	[a-z]*) ;;
	*) n="dropsite" ;;
	esac
	host_taken "$n" && n="dropsite-$n"
	printf '%s' "$n"
}

ensure_key() {
	ensure_ssh_dir
	KEY="$(ssh_dir)/dropsite_$NAME"
	if [ ! -f "$KEY" ]; then
		vsayf "$M_step_key" "$KEY"
		ssh-keygen -q -t ed25519 -N '' -C "dropsite-$NAME" -f "$KEY" </dev/null >/dev/null
	fi
	chmod 600 "$KEY"
	[ -f "$KEY.pub" ] || ssh-keygen -y -f "$KEY" >"$KEY.pub"
}

# os_supported ID VERSION_ID: Ubuntu 22.04 or newer, Debian 12 or newer.
# Newer releases are accepted as long as Docker publishes packages for them
# (checked on the server before Docker is installed).
os_supported() {
	v=${2:-}
	case "${1:-}" in
	ubuntu)
		major=${v%%.*}
		minor=${v#*.}
		[ "$minor" != "$v" ] || minor=0
		case "$major" in '' | *[!0-9]*) return 1 ;; esac
		case "$minor" in '' | *[!0-9]*) return 1 ;; esac
		[ $((major * 100 + ${minor#0})) -ge 2204 ]
		;;
	debian)
		case "$v" in '' | *[!0-9]*) return 1 ;; esac
		[ "$v" -ge 12 ]
		;;
	*) return 1 ;;
	esac
}

check_remote_os() {
	vsayf "$M_step_check"
	# shellcheck disable=SC2016 # expanded by the remote shell
	os=$(kssh '. /etc/os-release && printf "%s %s" "$ID" "$VERSION_ID"' </dev/null 2>/dev/null) || os="unknown"
	log_line "server system: $os"
	# shellcheck disable=SC2086 # split "ID VERSION" into two arguments
	os_supported ${os} || die "$M_os_bad" "$os"
}

# fetch_bootstrap prints the path of server-bootstrap.sh to upload.
fetch_bootstrap() {
	if [ -n "${DROPSITE_BOOTSTRAP:-}" ]; then
		printf '%s' "$DROPSITE_BOOTSTRAP"
		return
	fi
	case "$0" in
	*/install-server.sh)
		local_copy="$(dirname "$0")/server-bootstrap.sh"
		if [ -f "$local_copy" ]; then
			printf '%s' "$local_copy"
			return
		fi
		;;
	esac
	url="$RAW_BASE/${DROPSITE_REF:-main}/scripts/server-bootstrap.sh"
	curl -fsSL --proto '=https' -o "$TMP/server-bootstrap.sh" "$url" || die "$M_download_failed" "$url"
	printf '%s' "$TMP/server-bootstrap.sh"
}

run_bootstrap() {
	script=$(fetch_bootstrap)
	kssh 'umask 077 && cat >/root/dropsite-bootstrap.sh' <"$script" 2>>"${LOG_FILE:-/dev/null}" ||
		fail_with_log "$M_download_failed" "server-bootstrap.sh"
	envs="DROPSITE_DOMAIN=$(q "$DOMAIN") DROPSITE_EMAIL=$(q "$EMAIL") DROPSITE_LOCAL_PORT=$(q "$LOCAL_PORT")"
	[ -z "${PUBLIC_ADMIN:-}" ] || envs="$envs DROPSITE_PUBLIC_ADMIN=$(q "$PUBLIC_ADMIN")"
	for v in DROPSITE_REPO_URL DROPSITE_REF DROPSITE_VERSION DROPSITE_BUILD DROPSITE_SKIP_SWAP DROPSITE_SKIP_FIREWALL; do
		eval "val=\${$v:-}"
		# shellcheck disable=SC2154 # set by eval
		[ -z "$val" ] || envs="$envs $v=$(q "$val")"
	done
	vsayf "$M_step_bootstrap"
	# stdout carries the DROPSITE_RESULT lines (the new admin password among
	# them) into a private temporary file only; the progress on stderr goes
	# through show_progress into the log.
	{
		rc=0
		kssh "$envs bash /root/dropsite-bootstrap.sh" </dev/null 2>&1 >"$TMP/result" || rc=$?
		printf '%s' "$rc" >"$TMP/rc"
	} | show_progress
	if [ "$(cat "$TMP/rc" 2>/dev/null)" != 0 ]; then
		[ "$VERBOSE" != 1 ] || sayf "$M_server_log" "$NAME"
		log_line "$(msgf "$M_server_log" "$NAME")"
		fail_with_log "$M_bootstrap_failed"
	fi
}

# show_progress copies the server output into the log; on the screen it is
# shown in full with --verbose, else as one dot per server step.
show_progress() {
	while IFS= read -r line || [ -n "$line" ]; do
		log_line "$line"
		if [ "$VERBOSE" = 1 ]; then
			printf '%s\n' "$line" >&2
		else
			case "$line" in "==> "*) step_tick ;; esac
		fi
	done
}

# result KEY: a DROPSITE_RESULT value from the server output.
result() { sed -n "s/^DROPSITE_RESULT $1=//p" "$TMP/result" | tail -n 1; }

# ---------------------------------------------------------------------------
# DNS and HTTPS.

# resolve TYPE NAME: addresses from a public resolver (dig, else DNS over
# HTTPS through curl), one per line.
resolve() {
	if command -v dig >/dev/null 2>&1; then
		out=$(dig +short +time=3 +tries=1 "$1" "$2" @1.1.1.1 2>/dev/null) && {
			printf '%s\n' "$out" | grep -E '^[0-9a-fA-F.:]+$' || true
			return
		}
	fi
	curl -fsS --max-time 8 -H 'accept: application/dns-json' \
		"https://1.1.1.1/dns-query?name=$2&type=$1" 2>/dev/null |
		tr ',{}' '[\n*]' | sed -n 's/^"data":"\([0-9a-fA-F.:]*\)"$/\1/p' || true
}

# points_to TYPE NAME ADDR: NAME has exactly ADDR among its TYPE records.
points_to() { resolve "$1" "$2" | grep -qxF "$3"; }

random_label() { printf 'dns-check-%s' "$(od -An -N4 -tx1 /dev/urandom | tr -d ' \n')"; }

# dns_row TYPE NAME VALUE: one line of the DNS table, on the screen and in the log.
dns_row() { tell '  %-6s %-6s %s' "$1" "$2" "$3"; }

# print_dns_records: by default the two A records and one Cloudflare hint;
# --verbose adds AAAA, the full names and the advice on existing records.
print_dns_records() {
	say ""
	if [ "$VERBOSE" = 1 ]; then
		tell "$M_dns_title" "$DOMAIN"
	else
		tell "$M_dns_short" "$DOMAIN"
	fi
	say ""
	tell "$M_dns_head"
	dns_row A "@" "$IPV4"
	dns_row A "*" "$IPV4"
	if [ "$VERBOSE" = 1 ]; then
		if [ -n "$IPV6" ]; then
			dns_row AAAA "@" "$IPV6"
			dns_row AAAA "*" "$IPV6"
		fi
		sayf "$M_dns_names" "$DOMAIN" "$DOMAIN"
		say ""
		sayf "$M_dns_hint"
	else
		say ""
		sayf "$M_dns_cloudflare"
	fi
	say ""
}

check_later_cmd() { printf 'curl -fsSL %s | sh -s -- --check-dns --name %s' "$INSTALL_URL" "$NAME"; }

on_interrupt() {
	say ""
	sayf "$M_interrupted"
	sayf "$M_check_later" "$(check_later_cmd)"
	final_screen
	exit 130
}

# wait_dns: 0 when the domain and a random subdomain point to the server
# and https://api.<domain>/ answers with a valid certificate.
wait_dns() {
	timeout=${DROPSITE_DNS_TIMEOUT:-1800}
	deadline=$(($(date +%s) + timeout))
	tell "$M_dns_waiting" "$(((timeout + 59) / 60))"
	trap on_interrupt INT
	sub="$(random_label).$DOMAIN"
	step_begin "$M_s_dns"
	while :; do
		if points_to A "$DOMAIN" "$IPV4" && points_to A "$sub" "$IPV4"; then
			break
		fi
		if [ "$(date +%s)" -ge "$deadline" ]; then
			trap - INT
			step_fail
			tell "$M_dns_timeout"
			return 3
		fi
		step_tick
		sleep "$DNS_INTERVAL"
	done
	step_done "$M_s_dns_done"
	vsayf "$M_dns_ok"
	if [ -n "$IPV6" ]; then
		v6=$(resolve AAAA "$DOMAIN")
		if [ -z "$v6" ]; then
			vsayf "$M_dns_aaaa_missing"
		elif ! printf '%s\n' "$v6" | grep -qxF "$IPV6"; then
			tell "$M_dns_aaaa_wrong" "$(printf '%s' "$v6" | tr '\n' ' ')"
		fi
	fi
	url="https://$API_HOST/"
	step_begin "$M_s_https"
	vsayf "$M_https_waiting" "$url"
	restarted=0
	started=$(date +%s)
	while ! https_works "$url"; do
		now=$(date +%s)
		if [ "$now" -ge "$deadline" ]; then
			trap - INT
			step_fail
			tell "$M_https_timeout"
			return 3
		fi
		# Caddy retries certificates with a growing pause; once DNS works,
		# a restart makes it try again at once.
		if [ "$restarted" = 0 ] && [ $((now - started)) -ge 180 ]; then
			log_line "restarting caddy on the server"
			kssh 'cd /opt/dropsite/deploy && docker compose restart caddy' </dev/null >/dev/null 2>&1 || true
			restarted=1
		fi
		step_tick
		sleep "$DNS_INTERVAL"
	done
	trap - INT
	step_done "$M_s_https_done"
	vsayf "$M_https_ok" "$url"
	return 0
}

# https_works URL: the URL answers with a valid certificate.
https_works() { curl -sS -o /dev/null --max-time 15 "$1" 2>/dev/null; }

dns_step() {
	print_dns_records
	if [ "$SKIP_DNS" = 1 ]; then
		tell "$M_skip_dns"
		sayf "$M_check_later" "$(check_later_cmd)"
		return 0
	fi
	# The question only with --verbose: by default it simply waits, and
	# Ctrl+C stops the waiting.
	if [ "$CHECK_DNS" != 1 ] && [ "$VERBOSE" = 1 ] && ! yes_no "$M_q_wait"; then
		sayf "$M_check_later" "$(check_later_cmd)"
		return 0
	fi
	rc=0
	wait_dns || rc=$?
	if [ "$rc" != 0 ]; then
		sayf "$M_check_later" "$(check_later_cmd)"
	fi
	return "$rc"
}

# final_screen: what to do next. It shows the generated password, so it is
# never written to the log.
final_screen() {
	if [ "$VERBOSE" = 1 ]; then
		final_screen_full
		return
	fi
	say ""
	sayf "$M_done_short"
	say ""
	sayf "$M_final_ssh" "$NAME"
	sayf "$M_final_open" "$LOCAL_PORT"
	[ -z "${PUBLIC_ADMIN_URL:-}" ] || sayf "$M_final_public" "$PUBLIC_ADMIN_URL"
	if [ "${ADMIN_PASSWORD_NEW:-0}" = 1 ] && [ -n "${ADMIN_PASSWORD:-}" ]; then
		sayf "$M_final_login" "$ADMIN_EMAIL" "$ADMIN_PASSWORD"
	else
		sayf "$M_final_login_old" "$ADMIN_EMAIL" "$NAME"
	fi
	[ -z "${CONNECT_URL:-}" ] || sayf "$M_final_connect" "$CONNECT_URL"
	say ""
	sayf "$M_final_next"
}

final_screen_full() {
	say ""
	say "=================================================================="
	sayf "$M_done"
	say ""
	sayf "$M_open_admin" "$NAME" "$LOCAL_PORT"
	if [ -n "${PUBLIC_ADMIN_URL:-}" ]; then
		say ""
		sayf "$M_open_public" "$PUBLIC_ADMIN_URL"
	fi
	say ""
	if [ "${ADMIN_PASSWORD_NEW:-0}" = 1 ] && [ -n "${ADMIN_PASSWORD:-}" ]; then
		sayf "$M_login" "$ADMIN_EMAIL" "$ADMIN_PASSWORD"
	else
		sayf "$M_login_old" "$ADMIN_EMAIL" "$NAME"
	fi
	say ""
	sayf "$M_connect_ai"
	if [ -n "${CONNECT_URL:-}" ]; then
		sayf "$M_connect_url" "$NAME" "$CONNECT_URL"
	fi
	sayf "$M_sites_at" "$DOMAIN"
	sayf "$M_update" "$NAME"
	[ -z "$LOG_FILE" ] || sayf "$M_log_at" "$LOG_FILE"
	say "=================================================================="
}

# load_facts reads the server facts from the bootstrap result file.
load_facts() {
	DOMAIN=$(result domain)
	API_HOST=$(result api_host)
	ADMIN_EMAIL=$(result admin_email)
	ADMIN_PASSWORD=$(result admin_password)
	ADMIN_PASSWORD_NEW=$(result admin_password_new)
	IPV6=$(result ipv6)
	PUBLIC_ADMIN_URL=$(result public_admin_url)
	# A one-time connect URL (fresh installations only): shown on the screen,
	# never written to the log, like the password.
	CONNECT_URL=$(result connect_url)
	# The address the user typed is the public one; the server may only see
	# a private address behind NAT.
	if is_ipv4 "$HOST"; then IPV4=$HOST; else IPV4=$(result ipv4); fi
	[ -n "$API_HOST" ] || API_HOST="api.$DOMAIN"
}

check_dns_mode() {
	[ -n "$NAME" ] || bad_input "$M_missing" "--name"
	has_block "$NAME" || die "$M_no_install" "$NAME" "$(ssh_config)"
	log_open
	log_line "mode: --check-dns, name: $NAME"
	HOST=$(block_value "$NAME" HostName)
	PORT=$(block_value "$NAME" Port)
	[ -n "$PORT" ] || PORT=22
	KEY="$(ssh_dir)/dropsite_$NAME"
	LOCAL_PORT=$(block_value "$NAME" LocalForward)
	kssh dropsite-info </dev/null >"$TMP/result" 2>>"${LOG_FILE:-/dev/null}" || die "$M_key_failed" "$HOST"
	load_facts
	rc=0
	dns_step || rc=$?
	final_screen
	exit "$rc"
}

# confirm: one short question by default, the full summary with --verbose.
confirm() {
	where="$HOST$([ "$PORT" = 22 ] || printf ':%s' "$PORT")"
	if [ "$VERBOSE" = 1 ]; then
		say ""
		sayf "$M_summary" "$where" "$NAME" "$DOMAIN" "$EMAIL"
		q_text=$M_confirm
	else
		q_text=$(msgf "$M_confirm_short" "$where" "$DOMAIN")
	fi
	yes_no "$q_text" || {
		sayf "$M_cancelled"
		exit 1
	}
}

# connect_step: the dedicated key, sign-in, system check and ~/.ssh/config.
connect_step() {
	step_begin "$M_s_connect"
	ensure_key
	key_works || install_key
	key_works || die "$M_key_failed" "$HOST"
	check_remote_os
	LOCAL_PORT=$(choose_local_port "$NAME")
	write_ssh_config "$NAME" "$HOST" "$PORT" "$KEY" "$LOCAL_PORT"
	vsayf "$M_step_sshcfg" "$NAME" "$(ssh_config)" "$LOCAL_PORT"
	step_done "$M_s_connect_done"
}

main() {
	parse_args "$@"
	lang=$(pick_lang)
	msgs_en
	[ "$lang" = en ] || "msgs_$lang"
	if [ "$WANT_HELP" = 1 ]; then
		usage
		exit 0
	fi
	need_tools
	check_tty
	# On a terminal a step is one line whose "…" turns into "✓".
	if [ "$VERBOSE" != 1 ] && [ -t 2 ] && [ "${TERM:-dumb}" != dumb ]; then
		STEP_INLINE=1
	fi
	TMP=$(mktemp -d)
	trap cleanup EXIT
	trap 'exit 130' INT TERM
	if [ "$CHECK_DNS" = 1 ]; then
		NAME=$(lower "$NAME")
		check_dns_mode
	fi

	say ""
	sayf "$M_title"
	say ""
	if [ "$VERBOSE" = 1 ]; then
		sayf "$M_intro"
		say ""
	fi
	collect_inputs
	confirm
	log_open
	log_line "server: $HOST port $PORT, name: $NAME, domain: $DOMAIN, e-mail: $EMAIL, key file: ${USER_KEY:-none (root password)}"

	say ""
	tell "$M_installing"
	connect_step
	step_begin "$M_s_install"
	run_bootstrap
	step_done "$M_s_install_done"
	load_facts
	scrub_log "$ADMIN_PASSWORD"
	scrub_log "$CONNECT_URL"
	rc=0
	dns_step || rc=$?
	final_screen
	exit "$rc"
}

# DROPSITE_INSTALLER_LIB=1 loads the functions only (tests).
if [ "${DROPSITE_INSTALLER_LIB:-}" != 1 ]; then
	main "$@"
fi
