package server

import "strings"

// wellKnownGroups are paths automated scanners request on every host, grouped
// by the name the probe counter logs. They are matched exactly (ignoring case
// and a trailing slash). A path here is answered with the standard 404 and
// counted. It is a plain list so a path a site really serves can be pruned
// (accepted risk: /ghost and /user/login are short and legitimate on rare
// sites). /api/v1/admin/* is a different surface and is not listed.
var wellKnownGroups = map[string][]string{
	"wp-login": {
		"/wp-login.php", "/wp-signup.php", "/wp-activate.php", "/wordpress/wp-login.php",
		"/blog/wp-login.php", "/cms/wp-login.php", "/wp/wp-login.php", "/site/wp-login.php",
		"/web/wp-login.php", "/news/wp-login.php", "/new/wp-login.php", "/old/wp-login.php",
		"/test/wp-login.php", "/wp2/wp-login.php", "/shop/wp-login.php", "/home/wp-login.php",
	},
	"wp-admin": {"/wp-admin", "/wp-admin.php", "/wordpress/wp-admin", "/blog/wp-admin", "/wp/wp-admin"},
	"wp-config": {
		"/wp-config.php", "/wp-config.php.bak", "/wp-config.php~", "/wp-config.php.old", "/wp-config.php.save",
		"/wp-config.php.swp", "/wp-config.php.orig", "/wp-config.php.txt", "/wp-config.bak", "/wp-config.old",
		"/wp-config.txt", "/wp-config-sample.php", "/wordpress/wp-config.php", "/blog/wp-config.php",
	},
	"wp-core": {
		"/wp-cron.php", "/wp-trackback.php", "/wp-comments-post.php", "/wp-links-opml.php", "/wp-mail.php",
		"/wp-load.php", "/wp-settings.php", "/wp-blog-header.php", "/wp-json",
		"/readme.html", "/license.txt", "/wp-includes", "/wp-content", "/wp-content/debug.log",
		"/wp-content/uploads", "/wp-content/plugins", "/wp-content/themes", "/wp-includes/wlwmanifest.xml",
		"/wlwmanifest.xml", "/wp-content/plugins/hello.php", "/wp-content/uploads/shell.php",
	},
	"xmlrpc": {"/xmlrpc.php", "/blog/xmlrpc.php", "/wordpress/xmlrpc.php", "/wp/xmlrpc.php"},
	"joomla": {
		"/administrator", "/administrator/index.php", "/administrator/manifests/files/joomla.xml",
		"/configuration.php", "/configuration.php-dist", "/configuration.php.bak", "/language/en-GB/en-GB.xml",
		"/joomla/administrator",
	},
	"drupal": {
		"/user/login", "/core/install.php", "/CHANGELOG.txt",
		"/sites/default/settings.php", "/sites/default/files", "/misc/drupal.js",
	},
	"ghost":   {"/ghost", "/ghost/api/admin", "/ghost/api/v3/admin", "/ghost/signin"},
	"typo3":   {"/typo3/index.php", "/typo3conf/localconf.php", "/typo3conf/LocalConfiguration.php"},
	"magento": {"/index.php/admin", "/app/etc/local.xml", "/app/etc/env.php", "/errors/local.xml"},
	"bitrix":  {"/bitrix/admin/index.php", "/bitrix/php_interface/dbconn.php"},
	"admin-php": {
		"/admin.php", "/admin.asp", "/admin.aspx", "/admin.jsp", "/admin.html", "/admin.cgi",
		"/adminer.php",
		"/administrator.php",

		"/login.php", "/login.asp", "/login.aspx", "/login.jsp", "/login.action", "/login.html", "/signin.php",
		"/index.php", "/index.asp", "/index.aspx", "/default.asp", "/default.aspx", "/api.php",
	},
	"phpmyadmin": {
		"/phpmyadmin", "/phpmyadmin/index.php", "/pma", "/myadmin", "/mysqladmin",
		"/phpmyadmin2", "/phpmyadmin3", "/phpmyadmin4",
		"/phpMyAdmin-2", "/pma/index.php", "/mysql/index.php", "/dbadmin/index.php",
	},
	"env": {
		"/.env", "/.env.local", "/.env.production", "/.env.development", "/.env.staging", "/.env.test",
		"/.env.example", "/.env.bak", "/.env.backup", "/.env.old", "/.env.save", "/.env.dist", "/.env.php",
		"/.env.sample", "/env.js", "/env.json", "/.environment", "/backend/.env",
		"/app/.env", "/laravel/.env", "/config/.env", "/admin/.env", "/public/.env", "/core/.env", "/docker/.env",
	},
	"git": {
		"/.git", "/.git/config", "/.git/HEAD", "/.git/index", "/.git/logs/HEAD", "/.git/refs/heads/master",
		"/.git/refs/heads/main", "/.gitignore", "/.gitlab-ci.yml", "/.github/workflows", "/.gitmodules",
		"/.svn", "/.svn/entries", "/.svn/wc.db", "/.hg", "/.hg/hgrc", "/.bzr", "/CVS/Root", "/CVS/Entries",
	},
	"dotfiles": {
		"/.DS_Store", "/.htaccess", "/.htpasswd", "/.npmrc", "/.dockerenv", "/.bash_history", "/.bashrc",
		"/.profile", "/.mysql_history", "/.psql_history", "/.viminfo", "/.netrc", "/.pgpass", "/.my.cnf",
		"/.aws/credentials", "/.aws/config", "/.ssh/id_rsa", "/.ssh/id_dsa", "/.ssh/authorized_keys",
		"/.ssh/known_hosts", "/.vscode/settings.json", "/.vscode/sftp.json", "/.idea/workspace.xml",
		"/.travis.yml", "/.circleci/config.yml", "/.docker/config.json", "/.kube/config", "/.s3cfg",
		"/.git-credentials", "/.gcloud", "/.config", "/.user.ini", "/.htaccess.bak",
	},
	"config-file": {
		"/config.php", "/config.json", "/config.yml", "/config.yaml", "/config.inc.php", "/config.xml",
		"/config.js", "/config.bak", "/config.old", "/config.txt", "/conf/server.xml",
		"/settings.py", "/settings.json", "/local_settings.py", "/web.config", "/app.config", "/appsettings.json",
		"/application.yml", "/application.properties", "/parameters.yml", "/secrets.json", "/credentials.json",
		"/database.yml", "/db.php", "/db.inc", "/connect.php", "/connection.php", "/constants.php",
		"/composer.json", "/composer.lock", "/package.json", "/package-lock.json", "/yarn.lock", "/Gemfile",
		"/Gemfile.lock", "/requirements.txt", "/Dockerfile", "/docker-compose.yml", "/docker-compose.yaml",
		"/Makefile", "/Vagrantfile", "/terraform.tfstate", "/serverless.yml", "/phpinfo.ini",
	},
	"backup": {
		"/backup.zip", "/backup.tar", "/backup.tar.gz", "/backup.tgz", "/backup.sql",
		"/backup.sql.gz", "/backup.rar", "/backup.7z", "/backup.bak", "/db.sql", "/db.sql.gz", "/db.zip",
		"/dump.sql", "/dump.sql.gz", "/dump.zip", "/database.sql", "/database.zip", "/database.bak",
		"/data.sql", "/data.zip", "/site.zip", "/site.tar.gz", "/www.zip", "/www.tar.gz", "/wwwroot.zip",
		"/web.zip", "/public_html.zip", "/html.zip", "/htdocs.zip", "/old.zip", "/archive.zip", "/files.zip",
		"/website.zip", "/sql.zip", "/mysql.sql", "/backup.old", "/test.zip",
	},
	"debug": {
		"/phpinfo.php", "/info.php", "/php.php", "/test.php", "/pi.php", "/i.php", "/debug.php",
		"/_debugbar", "/_profiler", "/_ignition/health-check", "/_ignition/execute-solution",
		"/elmah.axd", "/trace.axd", "/server-status", "/server-info",
		"/nginx_status", "/php-fpm-status", "/fpm-status", "/__debug__",
		"/storage/logs/laravel.log", "/error_log", "/error.log", "/access.log", "/debug.log",
	},
	"actuator": {
		"/actuator/env", "/actuator/health", "/actuator/heapdump", "/actuator/mappings",
		"/actuator/beans", "/actuator/configprops", "/actuator/loggers", "/actuator/threaddump", "/actuator/httptrace",
		"/actuator/gateway/routes", "/actuator/jolokia", "/jolokia", "/heapdump",
	},
	"webshell": {
		"/shell.php", "/cmd.php", "/c99.php", "/r57.php", "/wso.php", "/b374k.php", "/alfa.php", "/up.php",
		"/upload.php", "/uploader.php", "/file.php", "/files.php", "/filemanager.php",
		"/fm.php", "/x.php", "/1.php", "/a.php", "/eval-stdin.php", "/vendor/phpunit/phpunit/src/Util/PHP/eval-stdin.php",
		"/cgi-bin/php", "/jsp/shell.jsp", "/shell.jsp", "/cmd.jsp", "/cmd.aspx", "/shell.aspx", "/webshell.php",
	},
	"app-server": {
		"/manager/html", "/host-manager/html", "/invoker/JMXInvokerServlet",
		"/solr/admin",
		"/_cat/indices", "/_cluster/health", "/_search", "/_all/_search",
	},
	"appliance": {
		"/boaform/admin/formLogin", "/HNAP1", "/ews/exchange.asmx",
		"/autodiscover/autodiscover.xml", "/remote/fgt_lang", "/vpn/index.html",
		"/global-protect/login.esp", "/+CSCOE+/logon.html", "/dana-na/auth/url_default/welcome.cgi",
		"/cgi-bin/luci", "/cgi-bin/login.cgi", "/setup.cgi", "/goform/webLogin", "/Portal/Portal.mwsl",
		"/ISAPI/Security/users", "/onvif/device_service",
	},
	"api-docs": {
		"/swagger-ui.html", "/swagger.json", "/swagger.yaml", "/swagger/index.html",
		"/openapi.json", "/openapi.yaml", "/v2/api-docs", "/v3/api-docs",
	},
	"auth-legacy": {"/auth"},
}

// wellKnownPrefixes match every path below (or starting with) the prefix.
var wellKnownPrefixes = []struct{ Prefix, Name string }{
	{"/wp-admin/", "wp-admin"},
	{"/wp-content/", "wp-core"},
	{"/wp-includes/", "wp-core"},
	{"/wp-json/", "wp-core"},
	{"/administrator/", "joomla"},
	{"/ghost/", "ghost"},
	{"/.git/", "git"},
	{"/.svn/", "git"},
	{"/.env.", "env"},
	{"/.aws/", "dotfiles"},
	{"/.ssh/", "dotfiles"},
	{"/.vscode/", "dotfiles"},
	{"/.idea/", "dotfiles"},
	{"/phpmyadmin", "phpmyadmin"},
	{"/pma/", "phpmyadmin"},
	{"/myadmin/", "phpmyadmin"},
	{"/cgi-bin/", "cgi-bin"},
	{"/actuator/", "actuator"},
	{"/node_modules/", "vendor"},

	{"/_profiler/", "debug"},
	{"/_ignition/", "debug"},
	{"/boaform/", "appliance"},
	{"/auth/", "auth-legacy"},
}

var wellKnownExact = func() map[string]string {
	m := map[string]string{}
	for name, paths := range wellKnownGroups {
		for _, p := range paths {
			m[strings.ToLower(strings.TrimSuffix(p, "/"))] = name
		}
	}
	return m
}()

// wellKnownName reports the probe name of a well-known scanner path.
func wellKnownName(path string) (string, bool) {
	p := strings.ToLower(path)
	if name, ok := wellKnownExact[strings.TrimSuffix(p, "/")]; ok {
		return name, true
	}
	for _, w := range wellKnownPrefixes {
		if prefixMatches(p, w.Prefix) {
			return w.Name, true
		}
	}
	return "", false
}

// prefixMatches reports whether p is under prefix. A prefix ending in "/" or
// "." matches by plain prefix; any other matches only at a segment boundary,
// so /phpmyadmin-x7k2q9 (a possible admin address) is not the /phpmyadmin
// probe.
func prefixMatches(p, prefix string) bool {
	if strings.HasSuffix(prefix, "/") || strings.HasSuffix(prefix, ".") {
		return strings.HasPrefix(p, prefix)
	}
	return p == prefix || strings.HasPrefix(p, prefix+"/")
}
