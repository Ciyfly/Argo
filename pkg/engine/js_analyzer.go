package engine

import (
	"net/url"
	"regexp"
	"strings"
	"sync"

	"argo/pkg/log"
	"argo/pkg/static"

	"github.com/go-rod/rod"
)

// JSAnalyzer JavaScript静态分析器
type JSAnalyzer struct {
	mu              sync.RWMutex
	patterns        []*URLPattern
	extractedURLs   map[string]*ExtractedURL
	apiEndpoints    map[string]*APIEndpoint
	sensitiveData   map[string][]string
	config          *JSAnalyzerConfig
	// P0优化: JSluice AST 分析器
	jsluiceAnalyzer *static.JSluiceAnalyzer
}

// JSAnalyzerConfig 配置
type JSAnalyzerConfig struct {
	EnableAPIExtraction    bool     // 启用API提取
	EnableSecretDetection  bool     // 启用敏感信息检测
	EnableSourceMapParsing bool     // 启用SourceMap解析
	EnableJSluice          bool     // 启用JSluice AST分析 (P0优化)
	MaxScriptSize          int      // 最大脚本大小(字节)
	CustomPatterns         []string // 自定义正则模式
}

// URLPattern URL匹配模式
type URLPattern struct {
	Name     string
	Pattern  *regexp.Regexp
	Type     string // api, static, page, websocket
	Priority int
}

// ExtractedURL 提取的URL
type ExtractedURL struct {
	URL       string
	Source    string   // script_inline, script_external, json_config
	Type      string   // api, static, page
	Method    string   // GET, POST, etc.
	Params    []string // 发现的参数
	Context   string   // 上下文信息
	Frequency int      // 出现频率
}

// APIEndpoint API端点
type APIEndpoint struct {
	Path       string
	Method     string
	Params     []APIParam
	Headers    map[string]string
	AuthType   string // none, bearer, basic, cookie
	Source     string
	Confidence float64 // 置信度 0-1
}

// APIParam API参数
type APIParam struct {
	Name     string
	Type     string // query, body, path
	Required bool
	Example  string
}

// 预定义的URL提取模式
var defaultURLPatterns = []*URLPattern{
	// API路径模式
	{Name: "api_path", Pattern: regexp.MustCompile(`['"](/api/[^'"?#\s]{1,200})['"?]`), Type: "api", Priority: 100},
	{Name: "api_v_path", Pattern: regexp.MustCompile(`['"](/v[0-9]+/[^'"?#\s]{1,200})['"?]`), Type: "api", Priority: 95},
	{Name: "rest_path", Pattern: regexp.MustCompile(`['"](/rest/[^'"?#\s]{1,200})['"?]`), Type: "api", Priority: 90},
	{Name: "graphql", Pattern: regexp.MustCompile(`['"](/graphql[^'"?#\s]{0,50})['"?]`), Type: "api", Priority: 100},

	// 完整URL模式
	{Name: "full_url", Pattern: regexp.MustCompile(`['"]((https?:)?//[^'"?#\s]{5,500})['"?]`), Type: "page", Priority: 50},
	{Name: "protocol_relative", Pattern: regexp.MustCompile(`['"](//[^'"?#\s]{5,300})['"?]`), Type: "page", Priority: 45},

	// 相对路径模式
	{Name: "relative_path", Pattern: regexp.MustCompile(`['"](/[a-zA-Z0-9_-]+/[^'"?#\s]{1,200})['"?]`), Type: "page", Priority: 40},
	{Name: "action_path", Pattern: regexp.MustCompile(`['"]([^'"]*[?&]action=[^'"&\s]+)['"?]`), Type: "api", Priority: 80},

	// 特殊端点
	{Name: "json_endpoint", Pattern: regexp.MustCompile(`['"]([^'"]+\.json)['"?]`), Type: "api", Priority: 85},
	{Name: "ajax_endpoint", Pattern: regexp.MustCompile(`['"]([^'"]+/ajax/[^'"?#\s]+)['"?]`), Type: "api", Priority: 85},
	{Name: "rpc_endpoint", Pattern: regexp.MustCompile(`['"]([^'"]+/rpc/[^'"?#\s]+)['"?]`), Type: "api", Priority: 85},

	// WebSocket
	{Name: "websocket", Pattern: regexp.MustCompile(`['"](wss?://[^'"?#\s]+)['"?]`), Type: "websocket", Priority: 70},

	// 配置中的URL
	{Name: "config_url", Pattern: regexp.MustCompile(`(?:url|endpoint|api|host|server|base)['":\s]+['"]([^'"]+)['"?]`), Type: "api", Priority: 75},
	{Name: "fetch_url", Pattern: regexp.MustCompile(`fetch\s*\(\s*['"]([^'"]+)['"?]`), Type: "api", Priority: 90},
	{Name: "axios_url", Pattern: regexp.MustCompile(`axios\.[a-z]+\s*\(\s*['"]([^'"]+)['"?]`), Type: "api", Priority: 90},
	{Name: "xhr_url", Pattern: regexp.MustCompile(`\.open\s*\(\s*['"][A-Z]+['"]\s*,\s*['"]([^'"]+)['"?]`), Type: "api", Priority: 90},
}

// 敏感信息检测模式 - 超全正则匹配
var sensitivePatterns = map[string]*regexp.Regexp{
	// ==================== AWS ====================
	"aws_access_key":     regexp.MustCompile(`AKIA[0-9A-Z]{16}`),
	"aws_secret_key":     regexp.MustCompile(`(?i)aws[_\-\.]?secret[_\-\.]?(?:access[_\-\.]?)?key['":\s]*[=:]["']?([A-Za-z0-9/+=]{40})["']?`),
	"aws_account_id":     regexp.MustCompile(`(?i)aws[_\-\.]?account[_\-\.]?id['":\s]*[=:]["']?([0-9]{12})["']?`),
	"aws_session_token":  regexp.MustCompile(`(?i)aws[_\-\.]?session[_\-\.]?token['":\s]*[=:]["']?([A-Za-z0-9/+=]{100,})["']?`),
	"aws_mws_auth_token": regexp.MustCompile(`amzn\.mws\.[0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12}`),

	// ==================== Google/GCP ====================
	"google_api_key":        regexp.MustCompile(`AIza[0-9A-Za-z\-_]{32,35}`),
	"google_oauth_id":       regexp.MustCompile(`[0-9]+-[0-9A-Za-z_]{32}\.apps\.googleusercontent\.com`),
	"google_oauth_secret":   regexp.MustCompile(`(?i)client[_\-]?secret['":\s]*[=:]["']?([A-Za-z0-9_\-]{24})["']?`),
	"gcp_service_account":   regexp.MustCompile(`"type"\s*:\s*"service_account"`),
	"firebase_api_key":      regexp.MustCompile(`(?i)firebase[_\-]?api[_\-]?key['":\s]*[=:]["']?([A-Za-z0-9_\-]{39})["']?`),
	"firebase_database_url": regexp.MustCompile(`https://[a-z0-9-]+\.firebaseio\.com`),

	// ==================== GitHub ====================
	"github_token":         regexp.MustCompile(`ghp_[0-9a-zA-Z]{36}`),
	"github_oauth":         regexp.MustCompile(`gho_[0-9a-zA-Z]{36}`),
	"github_app_token":     regexp.MustCompile(`(?:ghu|ghs)_[0-9a-zA-Z]{36}`),
	"github_refresh_token": regexp.MustCompile(`ghr_[0-9a-zA-Z]{36}`),
	"github_fine_grained":  regexp.MustCompile(`github_pat_[0-9a-zA-Z]{22}_[0-9a-zA-Z]{59}`),
	"github_legacy":        regexp.MustCompile(`(?i)github[_\-\.]?(?:access[_\-\.]?)?token['":\s]*[=:]["']?([0-9a-f]{40})["']?`),

	// ==================== GitLab ====================
	"gitlab_token":         regexp.MustCompile(`glpat-[0-9a-zA-Z\-_]{20}`),
	"gitlab_pipeline":      regexp.MustCompile(`glptt-[0-9a-f]{40}`),
	"gitlab_runner":        regexp.MustCompile(`GR1348941[0-9a-zA-Z\-_]{20}`),
	"gitlab_feed_token":    regexp.MustCompile(`glft-[0-9a-zA-Z\-_]{20}`),

	// ==================== Slack ====================
	"slack_token":       regexp.MustCompile(`xox[baprs]-[0-9]{10,13}-[0-9]{10,13}[a-zA-Z0-9-]*`),
	"slack_webhook":     regexp.MustCompile(`https://hooks\.slack\.com/services/T[a-zA-Z0-9_]+/B[a-zA-Z0-9_]+/[a-zA-Z0-9_]+`),
	"slack_bot_token":   regexp.MustCompile(`xoxb-[0-9]{11}-[0-9]{11}-[0-9a-zA-Z]{24}`),
	"slack_user_token":  regexp.MustCompile(`xoxp-[0-9]{11}-[0-9]{11}-[0-9a-zA-Z]{24}`),
	"slack_config_token": regexp.MustCompile(`xoxe\.xox[bp]-[0-9]-[0-9a-zA-Z]{163}`),

	// ==================== Stripe ====================
	"stripe_secret_key":       regexp.MustCompile(`sk_live_[0-9a-zA-Z]{24,99}`),
	"stripe_publishable_key":  regexp.MustCompile(`pk_live_[0-9a-zA-Z]{24,99}`),
	"stripe_test_secret":      regexp.MustCompile(`sk_test_[0-9a-zA-Z]{24,99}`),
	"stripe_test_publishable": regexp.MustCompile(`pk_test_[0-9a-zA-Z]{24,99}`),
	"stripe_restricted_key":   regexp.MustCompile(`rk_live_[0-9a-zA-Z]{24,99}`),
	"stripe_webhook_secret":   regexp.MustCompile(`whsec_[0-9a-zA-Z]{32,}`),

	// ==================== Twilio ====================
	"twilio_account_sid": regexp.MustCompile(`AC[0-9a-f]{32}`),
	"twilio_auth_token":  regexp.MustCompile(`(?i)twilio[_\-\.]?auth[_\-\.]?token['":\s]*[=:]["']?([0-9a-f]{32})["']?`),
	"twilio_api_key":     regexp.MustCompile(`SK[0-9a-f]{32}`),

	// ==================== SendGrid ====================
	"sendgrid_api_key": regexp.MustCompile(`SG\.[0-9A-Za-z\-_]{20,25}\.[0-9A-Za-z\-_]{40,50}`),

	// ==================== Mailchimp ====================
	"mailchimp_api_key": regexp.MustCompile(`[0-9a-f]{32}-us[0-9]{1,2}`),

	// ==================== Mailgun ====================
	"mailgun_api_key": regexp.MustCompile(`key-[0-9a-zA-Z]{32}`),

	// ==================== Square ====================
	"square_access_token": regexp.MustCompile(`sq0atp-[0-9A-Za-z\-_]{22}`),
	"square_oauth_secret": regexp.MustCompile(`sq0csp-[0-9A-Za-z\-_]{43}`),

	// ==================== PayPal ====================
	"paypal_braintree_token": regexp.MustCompile(`access_token\$production\$[0-9a-z]{16}\$[0-9a-f]{32}`),

	// ==================== Heroku ====================
	"heroku_api_key": regexp.MustCompile(`(?i)heroku[_\-\.]?api[_\-\.]?key['":\s]*[=:]["']?([0-9a-f]{8}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{4}-[0-9a-f]{12})["']?`),

	// ==================== Azure ====================
	"azure_subscription_key": regexp.MustCompile(`(?i)azure[_\-\.]?(?:subscription|api)[_\-\.]?key['":\s]*[=:]["']?([0-9a-f]{32})["']?`),
	"azure_storage_key":      regexp.MustCompile(`(?i)(?:account[_\-\.]?key|storage[_\-\.]?key)['":\s]*[=:]["']?([A-Za-z0-9+/=]{80,92})["']?`),
	"azure_connection_string": regexp.MustCompile(`DefaultEndpointsProtocol=https;AccountName=[^;]+;AccountKey=[A-Za-z0-9+/=]{80,92}`),
	"azure_sas_token":        regexp.MustCompile(`(?:sv|sig)=[0-9a-zA-Z%]+`),

	// ==================== Alibaba Cloud ====================
	"aliyun_access_key": regexp.MustCompile(`LTAI[0-9a-zA-Z]{12,20}`),
	"aliyun_secret_key": regexp.MustCompile(`(?i)(?:aliyun|alicloud)[_\-\.]?(?:access[_\-\.]?)?secret['":\s]*[=:]["']?([a-zA-Z0-9]{30})["']?`),

	// ==================== Tencent Cloud ====================
	"tencent_secret_id":  regexp.MustCompile(`AKID[0-9a-zA-Z]{13,20}`),
	"tencent_secret_key": regexp.MustCompile(`(?i)(?:tencent|qcloud)[_\-\.]?secret[_\-\.]?key['":\s]*[=:]["']?([a-zA-Z0-9]{32})["']?`),

	// ==================== NPM ====================
	"npm_token": regexp.MustCompile(`npm_[0-9a-zA-Z]{36}`),

	// ==================== PyPI ====================
	"pypi_token": regexp.MustCompile(`pypi-AgEIcHlwaS5vcmc[0-9A-Za-z\-_]{50,}`),

	// ==================== Docker Hub ====================
	"docker_hub_token": regexp.MustCompile(`dckr_pat_[0-9A-Za-z\-_]{27}`),

	// ==================== Shopify ====================
	"shopify_shared_secret": regexp.MustCompile(`shpss_[0-9a-f]{32}`),
	"shopify_access_token":  regexp.MustCompile(`shpat_[0-9a-f]{32}`),
	"shopify_custom_token":  regexp.MustCompile(`shpca_[0-9a-f]{32}`),
	"shopify_private_token": regexp.MustCompile(`shppa_[0-9a-f]{32}`),

	// ==================== Dropbox ====================
	"dropbox_access_token":   regexp.MustCompile(`sl\.[0-9A-Za-z\-_]{130,}`),
	"dropbox_short_token":    regexp.MustCompile(`(?i)dropbox[_\-\.]?(?:access[_\-\.]?)?token['":\s]*[=:]["']?([a-zA-Z0-9_-]{64})["']?`),

	// ==================== Discord ====================
	"discord_token":         regexp.MustCompile(`[MN][A-Za-z\d]{23,}\.[\w-]{6}\.[\w-]{27}`),
	"discord_webhook":       regexp.MustCompile(`https://discord(?:app)?\.com/api/webhooks/[0-9]+/[A-Za-z0-9_\-]+`),
	"discord_bot_token":     regexp.MustCompile(`(?i)discord[_\-\.]?(?:bot[_\-\.]?)?token['":\s]*[=:]["']?([A-Za-z0-9_\-.]{59,68})["']?`),

	// ==================== Telegram ====================
	"telegram_bot_token": regexp.MustCompile(`[0-9]{9,10}:[0-9A-Za-z_-]{35}`),

	// ==================== Facebook/Meta ====================
	"facebook_access_token": regexp.MustCompile(`EAACEdEose0cBA[0-9A-Za-z]+`),
	"facebook_secret":       regexp.MustCompile(`(?i)facebook[_\-\.]?(?:app[_\-\.]?)?secret['":\s]*[=:]["']?([0-9a-f]{32})["']?`),

	// ==================== Twitter/X ====================
	"twitter_bearer_token": regexp.MustCompile(`AAAAAAAAAAAAAAAAAAA[0-9A-Za-z%]+`),
	"twitter_api_key":      regexp.MustCompile(`(?i)twitter[_\-\.]?api[_\-\.]?(?:key|secret)['":\s]*[=:]["']?([0-9a-zA-Z]{25,50})["']?`),

	// ==================== LinkedIn ====================
	"linkedin_client_id":     regexp.MustCompile(`(?i)linkedin[_\-\.]?client[_\-\.]?id['":\s]*[=:]["']?([0-9a-z]{14})["']?`),
	"linkedin_client_secret": regexp.MustCompile(`(?i)linkedin[_\-\.]?client[_\-\.]?secret['":\s]*[=:]["']?([0-9a-zA-Z]{16})["']?`),

	// ==================== 通用认证 ====================
	"jwt_token":           regexp.MustCompile(`eyJ[A-Za-z0-9_-]*\.eyJ[A-Za-z0-9_-]*\.[A-Za-z0-9_-]*`),
	"bearer_token":        regexp.MustCompile(`(?i)bearer\s+[a-zA-Z0-9_\-\.=]{20,500}`),
	"basic_auth":          regexp.MustCompile(`(?i)basic\s+[a-zA-Z0-9+/=]{20,100}`),
	"authorization_header": regexp.MustCompile(`(?i)authorization['":\s]*[=:]["']?(?:bearer|basic|token)\s+([a-zA-Z0-9_\-\.=+/]{20,})["']?`),

	// ==================== 私钥/证书 ====================
	"rsa_private_key":     regexp.MustCompile(`-----BEGIN RSA PRIVATE KEY-----`),
	"dsa_private_key":     regexp.MustCompile(`-----BEGIN DSA PRIVATE KEY-----`),
	"ec_private_key":      regexp.MustCompile(`-----BEGIN EC PRIVATE KEY-----`),
	"openssh_private_key": regexp.MustCompile(`-----BEGIN OPENSSH PRIVATE KEY-----`),
	"pgp_private_key":     regexp.MustCompile(`-----BEGIN PGP PRIVATE KEY BLOCK-----`),
	"private_key_generic": regexp.MustCompile(`-----BEGIN\s+(?:RSA\s+|DSA\s+|EC\s+|OPENSSH\s+)?PRIVATE\s+KEY-----`),
	"ssh_key":             regexp.MustCompile(`ssh-(?:rsa|dss|ed25519|ecdsa)\s+[A-Za-z0-9+/=]{100,}`),

	// ==================== 数据库 ====================
	"mysql_connection":      regexp.MustCompile(`mysql://[^\s'"<>]+:[^\s'"<>]+@[^\s'"<>]+`),
	"postgres_connection":   regexp.MustCompile(`postgres(?:ql)?://[^\s'"<>]+:[^\s'"<>]+@[^\s'"<>]+`),
	"mongodb_connection":    regexp.MustCompile(`mongodb(?:\+srv)?://[^\s'"<>]+:[^\s'"<>]+@[^\s'"<>]+`),
	"redis_connection":      regexp.MustCompile(`redis://[^\s'"<>]+:[^\s'"<>]+@[^\s'"<>]+`),
	"elasticsearch_url":     regexp.MustCompile(`(?i)elasticsearch[_\-\.]?(?:url|uri)['":\s]*[=:]["']?(https?://[^\s'"]+)["']?`),
	"jdbc_connection":       regexp.MustCompile(`jdbc:[a-z]+://[^\s'"]+`),
	"db_password":           regexp.MustCompile(`(?i)(?:db|database)[_\-\.]?pass(?:word)?['":\s]*[=:]["']?([^\s'"]{6,})["']?`),

	// ==================== 通用密码/密钥 ====================
	"password_field":       regexp.MustCompile(`(?i)(?:password|passwd|pwd|pass)['":\s]*[=:]["']?([^\s'"]{6,50})["']?`),
	"secret_key":           regexp.MustCompile(`(?i)(?:secret[_\-\.]?key|app[_\-\.]?secret|client[_\-\.]?secret)['":\s]*[=:]["']?([^\s'"]{8,100})["']?`),
	"api_key_generic":      regexp.MustCompile(`(?i)(?:api[_\-\.]?key|apikey|access[_\-\.]?key)['":\s]*[=:]["']?([^\s'"]{16,100})["']?`),
	"encryption_key":       regexp.MustCompile(`(?i)(?:encryption|encrypt)[_\-\.]?key['":\s]*[=:]["']?([^\s'"]{16,100})["']?`),
	"signing_key":          regexp.MustCompile(`(?i)(?:signing|sign)[_\-\.]?(?:key|secret)['":\s]*[=:]["']?([^\s'"]{16,100})["']?`),
	"private_token":        regexp.MustCompile(`(?i)private[_\-\.]?token['":\s]*[=:]["']?([^\s'"]{20,100})["']?`),
	"access_token_generic": regexp.MustCompile(`(?i)access[_\-\.]?token['":\s]*[=:]["']?([^\s'"]{20,500})["']?`),
	"refresh_token":        regexp.MustCompile(`(?i)refresh[_\-\.]?token['":\s]*[=:]["']?([^\s'"]{20,500})["']?`),
	"auth_token":           regexp.MustCompile(`(?i)auth[_\-\.]?token['":\s]*[=:]["']?([^\s'"]{20,500})["']?`),

	// ==================== 高熵值字符串（可能是密钥）====================
	"hex_secret_32":        regexp.MustCompile(`(?i)(?:key|secret|token|password|credential)['":\s]*[=:]["']?([0-9a-f]{32})["']?`),
	"hex_secret_64":        regexp.MustCompile(`(?i)(?:key|secret|token|password|credential)['":\s]*[=:]["']?([0-9a-f]{64})["']?`),
	"base64_secret":        regexp.MustCompile(`(?i)(?:key|secret|token|credential)['":\s]*[=:]["']?([A-Za-z0-9+/]{40,}={0,2})["']?`),

	// ==================== OAuth ====================
	"oauth_client_id":     regexp.MustCompile(`(?i)(?:oauth[_\-\.]?)?client[_\-\.]?id['":\s]*[=:]["']?([0-9a-zA-Z\-_]{20,100})["']?`),
	"oauth_client_secret": regexp.MustCompile(`(?i)(?:oauth[_\-\.]?)?client[_\-\.]?secret['":\s]*[=:]["']?([0-9a-zA-Z\-_]{20,100})["']?`),

	// ==================== 云服务通用 ====================
	"s3_bucket_url":      regexp.MustCompile(`https?://[a-z0-9.-]+\.s3[.-][a-z0-9-]+\.amazonaws\.com`),
	"s3_url_path":        regexp.MustCompile(`https?://s3[.-][a-z0-9-]+\.amazonaws\.com/[a-z0-9.-]+`),
	"cloudflare_api_key": regexp.MustCompile(`(?i)cloudflare[_\-\.]?api[_\-\.]?key['":\s]*[=:]["']?([0-9a-f]{37})["']?`),

	// ==================== 加密货币 ====================
	"bitcoin_private_key": regexp.MustCompile(`[5KL][1-9A-HJ-NP-Za-km-z]{50,51}`),
	"ethereum_private_key": regexp.MustCompile(`(?i)(?:eth|ethereum)[_\-\.]?(?:private[_\-\.]?)?key['":\s]*[=:]["']?(0x[0-9a-fA-F]{64})["']?`),

	// ==================== Sentry ====================
	"sentry_dsn": regexp.MustCompile(`https://[a-f0-9]{32}@(?:o[0-9]+\.)?(?:ingest\.)?sentry\.io/[0-9]+`),

	// ==================== Datadog ====================
	"datadog_api_key": regexp.MustCompile(`(?i)datadog[_\-\.]?api[_\-\.]?key['":\s]*[=:]["']?([0-9a-f]{32})["']?`),
	"datadog_app_key": regexp.MustCompile(`(?i)datadog[_\-\.]?app(?:lication)?[_\-\.]?key['":\s]*[=:]["']?([0-9a-f]{40})["']?`),

	// ==================== New Relic ====================
	"newrelic_license_key": regexp.MustCompile(`(?i)new[_\-\.]?relic[_\-\.]?license[_\-\.]?key['":\s]*[=:]["']?([0-9a-zA-Z]{40})["']?`),

	// ==================== PagerDuty ====================
	"pagerduty_api_key": regexp.MustCompile(`(?i)pagerduty[_\-\.]?api[_\-\.]?key['":\s]*[=:]["']?([a-zA-Z0-9+/]{20})["']?`),

	// ==================== Okta ====================
	"okta_api_token": regexp.MustCompile(`00[a-zA-Z0-9_-]{40}`),

	// ==================== Auth0 ====================
	"auth0_client_secret": regexp.MustCompile(`(?i)auth0[_\-\.]?(?:client[_\-\.]?)?secret['":\s]*[=:]["']?([A-Za-z0-9_\-]{32,})["']?`),

	// ==================== Intercom ====================
	"intercom_access_token": regexp.MustCompile(`(?i)intercom[_\-\.]?access[_\-\.]?token['":\s]*[=:]["']?([a-zA-Z0-9=_\-]{60})["']?`),

	// ==================== Asana ====================
	"asana_token": regexp.MustCompile(`(?i)asana[_\-\.]?token['":\s]*[=:]["']?([0-9]/[0-9]{13,}/[0-9]:[a-f0-9]{32})["']?`),

	// ==================== Zendesk ====================
	"zendesk_api_token": regexp.MustCompile(`(?i)zendesk[_\-\.]?api[_\-\.]?token['":\s]*[=:]["']?([a-zA-Z0-9]{40})["']?`),

	// ==================== 敏感URL ====================
	"internal_ip":      regexp.MustCompile(`(?:https?://)?(?:10\.\d{1,3}\.\d{1,3}\.\d{1,3}|172\.(?:1[6-9]|2\d|3[01])\.\d{1,3}\.\d{1,3}|192\.168\.\d{1,3}\.\d{1,3})(?::\d+)?`),
	"localhost_url":    regexp.MustCompile(`(?:https?://)?(?:localhost|127\.0\.0\.1)(?::\d+)?(?:/[^\s'"]*)?`),
	"admin_panel_url":  regexp.MustCompile(`(?i)(?:https?://)?[^\s'"]+/(?:admin|administrator|manager|backend|console|dashboard)(?:/[^\s'"]*)?`),
	"debug_endpoint":   regexp.MustCompile(`(?i)(?:https?://)?[^\s'"]+/(?:debug|phpinfo|server-status|\.env|\.git|\.svn|\.htaccess)(?:/[^\s'"]*)?`),
}

// DefaultJSAnalyzerConfig 默认配置
func DefaultJSAnalyzerConfig() *JSAnalyzerConfig {
	return &JSAnalyzerConfig{
		EnableAPIExtraction:    true,
		EnableSecretDetection:  true,
		EnableSourceMapParsing: false, // 默认关闭，可能增加请求
		EnableJSluice:          true,  // P0优化: 默认启用JSluice
		MaxScriptSize:          5 * 1024 * 1024, // 5MB
	}
}

// NewJSAnalyzer 创建JS分析器
func NewJSAnalyzer(config *JSAnalyzerConfig) *JSAnalyzer {
	if config == nil {
		config = DefaultJSAnalyzerConfig()
	}

	ja := &JSAnalyzer{
		patterns:      defaultURLPatterns,
		extractedURLs: make(map[string]*ExtractedURL),
		apiEndpoints:  make(map[string]*APIEndpoint),
		sensitiveData: make(map[string][]string),
		config:        config,
	}

	// 添加自定义模式
	for _, p := range config.CustomPatterns {
		if re, err := regexp.Compile(p); err == nil {
			ja.patterns = append(ja.patterns, &URLPattern{
				Name:     "custom",
				Pattern:  re,
				Type:     "api",
				Priority: 60,
			})
		}
	}

	// P0优化: 初始化 JSluice 分析器
	if config.EnableJSluice {
		jsluiceCfg := static.DefaultJSluiceConfig()
		jsluiceCfg.EnableSecretDetection = config.EnableSecretDetection
		jsluiceCfg.MaxScriptSize = config.MaxScriptSize
		ja.jsluiceAnalyzer = static.NewJSluiceAnalyzer(jsluiceCfg)
		log.Logger.Debug("JSAnalyzer: JSluice AST analyzer enabled")
	}

	return ja
}

// AnalyzePage 分析页面中的所有JavaScript
func (ja *JSAnalyzer) AnalyzePage(page *rod.Page) (*JSAnalysisResult, error) {
	if page == nil {
		return nil, nil
	}

	result := &JSAnalysisResult{
		URLs:         make([]*ExtractedURL, 0),
		APIEndpoints: make([]*APIEndpoint, 0),
		Secrets:      make(map[string][]string),
	}

	// 1. 分析内联脚本
	inlineScripts, err := ja.extractInlineScripts(page)
	if err != nil {
		log.Logger.Debugf("JSAnalyzer: extract inline scripts error: %v", err)
	}
	for _, script := range inlineScripts {
		ja.analyzeScript(script, "inline", result)
	}

	// 2. 分析外部脚本
	externalScripts, err := ja.extractExternalScriptURLs(page)
	if err != nil {
		log.Logger.Debugf("JSAnalyzer: extract external scripts error: %v", err)
	}
	for _, scriptURL := range externalScripts {
		result.ExternalScripts = append(result.ExternalScripts, scriptURL)
	}

	// 3. 分析页面中的JSON配置
	jsonConfigs, err := ja.extractJSONConfigs(page)
	if err != nil {
		log.Logger.Debugf("JSAnalyzer: extract JSON configs error: %v", err)
	}
	for _, config := range jsonConfigs {
		ja.analyzeScript(config, "json_config", result)
	}

	// 4. 分析全局变量中的配置
	globalConfigs, err := ja.extractGlobalConfigs(page)
	if err != nil {
		log.Logger.Debugf("JSAnalyzer: extract global configs error: %v", err)
	}
	for _, config := range globalConfigs {
		ja.analyzeScript(config, "global_config", result)
	}

	// 5. 去重和排序
	result.URLs = ja.deduplicateURLs(result.URLs)
	result.APIEndpoints = ja.deduplicateAPIs(result.APIEndpoints)

	return result, nil
}

// analyzeScript 分析单个脚本
func (ja *JSAnalyzer) analyzeScript(script, source string, result *JSAnalysisResult) {
	if len(script) == 0 || len(script) > ja.config.MaxScriptSize {
		return
	}

	// P0优化: 优先使用 JSluice AST 分析
	if ja.jsluiceAnalyzer != nil && ja.config.EnableJSluice {
		ja.analyzeWithJSluice(script, source, result)
	}

	// 使用正则作为补充 (JSluice 可能遗漏某些模式)
	if ja.config.EnableAPIExtraction {
		ja.analyzeWithRegex(script, source, result)
	}

	// 检测敏感信息 (正则补充，JSluice 已有内置检测)
	if ja.config.EnableSecretDetection && ja.jsluiceAnalyzer == nil {
		ja.detectSecretsWithRegex(script, result)
	}
}

// analyzeWithJSluice 使用 JSluice AST 分析
func (ja *JSAnalyzer) analyzeWithJSluice(script, source string, result *JSAnalysisResult) {
	jsluiceResult := ja.jsluiceAnalyzer.AnalyzeJS(script)
	if jsluiceResult == nil {
		return
	}

	// 转换 JSluice URLs 到内部格式
	for _, u := range jsluiceResult.URLs {
		if !ja.isValidURL(u.URL) {
			continue
		}

		urlType := "page"
		if u.Type == "fetch" || u.Type == "$.ajax" || u.Type == "$.get" || u.Type == "$.post" ||
			u.Type == "axios" || u.Type == "xhr" || u.Type == "XMLHttpRequest" {
			urlType = "api"
		} else if u.Type == "websocket" {
			urlType = "websocket"
		}

		extracted := &ExtractedURL{
			URL:       u.URL,
			Source:    source + "_jsluice",
			Type:      urlType,
			Method:    u.Method,
			Params:    u.QueryParams,
			Context:   u.Source,
			Frequency: 1,
		}
		result.URLs = append(result.URLs, extracted)

		// 创建 API 端点
		if urlType == "api" {
			endpoint := &APIEndpoint{
				Path:       u.URL,
				Method:     u.Method,
				Headers:    u.Headers,
				Source:     source + "_jsluice",
				Confidence: 0.9, // JSluice AST 分析置信度高
				Params:     make([]APIParam, 0),
			}
			// 添加查询参数
			for _, p := range u.QueryParams {
				endpoint.Params = append(endpoint.Params, APIParam{
					Name: p,
					Type: "query",
				})
			}
			// 添加 Body 参数
			for _, p := range u.BodyParams {
				endpoint.Params = append(endpoint.Params, APIParam{
					Name: p,
					Type: "body",
				})
			}
			result.APIEndpoints = append(result.APIEndpoints, endpoint)
		}
	}

	// 转换 JSluice Secrets
	for _, s := range jsluiceResult.Secrets {
		if result.Secrets[s.Kind] == nil {
			result.Secrets[s.Kind] = make([]string, 0)
		}
		// 已脱敏
		if dataStr, ok := s.Data.(string); ok {
			result.Secrets[s.Kind] = append(result.Secrets[s.Kind], dataStr)
		} else {
			result.Secrets[s.Kind] = append(result.Secrets[s.Kind], "[DETECTED]")
		}
	}

	log.Logger.Debugf("JSluice: found %d URLs, %d APIs, %d secrets from %s",
		len(jsluiceResult.URLs), len(jsluiceResult.APIEndpoints), len(jsluiceResult.Secrets), source)
}

// analyzeWithRegex 使用正则分析 (作为补充)
func (ja *JSAnalyzer) analyzeWithRegex(script, source string, result *JSAnalysisResult) {
	for _, pattern := range ja.patterns {
		matches := pattern.Pattern.FindAllStringSubmatch(script, -1)
		for _, match := range matches {
			if len(match) >= 2 {
				urlStr := match[1]
				if ja.isValidURL(urlStr) {
					extracted := &ExtractedURL{
						URL:       urlStr,
						Source:    source + "_regex",
						Type:      pattern.Type,
						Context:   ja.extractContext(script, match[0]),
						Frequency: 1,
					}
					// 尝试识别HTTP方法
					extracted.Method = ja.detectHTTPMethod(script, match[0])
					result.URLs = append(result.URLs, extracted)

					// 如果是API，创建端点
					if pattern.Type == "api" {
						endpoint := ja.createAPIEndpoint(urlStr, extracted.Method, source+"_regex")
						if endpoint != nil {
							result.APIEndpoints = append(result.APIEndpoints, endpoint)
						}
					}
				}
			}
		}
	}
}

// detectSecretsWithRegex 使用正则检测敏感信息
func (ja *JSAnalyzer) detectSecretsWithRegex(script string, result *JSAnalysisResult) {
	for name, pattern := range sensitivePatterns {
		matches := pattern.FindAllString(script, 10) // 限制数量
		if len(matches) > 0 {
			if result.Secrets[name] == nil {
				result.Secrets[name] = make([]string, 0)
			}
			for _, m := range matches {
				// 脱敏处理
				masked := ja.maskSensitiveData(m)
				result.Secrets[name] = append(result.Secrets[name], masked)
			}
		}
	}
}

// extractInlineScripts 提取内联脚本
func (ja *JSAnalyzer) extractInlineScripts(page *rod.Page) ([]string, error) {
	result, err := page.Eval(`
		(function() {
			const scripts = [];
			document.querySelectorAll('script:not([src])').forEach(s => {
				if (s.textContent && s.textContent.length > 0) {
					scripts.push(s.textContent);
				}
			});
			return scripts;
		})()
	`)
	if err != nil {
		return nil, err
	}

	var scripts []string
	arr := result.Value.Arr()
	for _, item := range arr {
		if s := item.Str(); s != "" {
			scripts = append(scripts, s)
		}
	}
	return scripts, nil
}

// extractExternalScriptURLs 提取外部脚本URL
func (ja *JSAnalyzer) extractExternalScriptURLs(page *rod.Page) ([]string, error) {
	result, err := page.Eval(`
		(function() {
			const urls = [];
			document.querySelectorAll('script[src]').forEach(s => {
				if (s.src) urls.push(s.src);
			});
			return urls;
		})()
	`)
	if err != nil {
		return nil, err
	}

	var urls []string
	arr := result.Value.Arr()
	for _, item := range arr {
		if s := item.Str(); s != "" {
			urls = append(urls, s)
		}
	}
	return urls, nil
}

// extractJSONConfigs 提取JSON配置
func (ja *JSAnalyzer) extractJSONConfigs(page *rod.Page) ([]string, error) {
	result, err := page.Eval(`
		(function() {
			const configs = [];
			// type="application/json" 脚本
			document.querySelectorAll('script[type="application/json"]').forEach(s => {
				if (s.textContent) configs.push(s.textContent);
			});
			// ld+json
			document.querySelectorAll('script[type="application/ld+json"]').forEach(s => {
				if (s.textContent) configs.push(s.textContent);
			});
			// data属性中的JSON
			document.querySelectorAll('[data-config], [data-props], [data-settings]').forEach(el => {
				['data-config', 'data-props', 'data-settings'].forEach(attr => {
					const val = el.getAttribute(attr);
					if (val && val.startsWith('{')) configs.push(val);
				});
			});
			return configs;
		})()
	`)
	if err != nil {
		return nil, err
	}

	var configs []string
	arr := result.Value.Arr()
	for _, item := range arr {
		if s := item.Str(); s != "" {
			configs = append(configs, s)
		}
	}
	return configs, nil
}

// extractGlobalConfigs 提取全局变量配置
func (ja *JSAnalyzer) extractGlobalConfigs(page *rod.Page) ([]string, error) {
	result, err := page.Eval(`
		(function() {
			const configs = [];
			const commonNames = [
				'__INITIAL_STATE__', '__NEXT_DATA__', '__NUXT__',
				'window.__CONFIG__', 'window.__APP_CONFIG__',
				'window.config', 'window.CONFIG', 'window.appConfig',
				'window.__PRELOADED_STATE__', 'window.__DATA__'
			];

			for (const name of commonNames) {
				try {
					const parts = name.split('.');
					let obj = window;
					for (const part of parts) {
						if (part === 'window') continue;
						obj = obj[part];
						if (!obj) break;
					}
					if (obj && typeof obj === 'object') {
						configs.push(JSON.stringify(obj));
					}
				} catch(e) {}
			}
			return configs;
		})()
	`)
	if err != nil {
		return nil, err
	}

	var configs []string
	arr := result.Value.Arr()
	for _, item := range arr {
		if s := item.Str(); s != "" {
			configs = append(configs, s)
		}
	}
	return configs, nil
}

// isValidURL 验证URL是否有效
func (ja *JSAnalyzer) isValidURL(urlStr string) bool {
	if urlStr == "" || len(urlStr) < 2 || len(urlStr) > 2000 {
		return false
	}

	// 跳过数据URL和特殊协议
	if strings.HasPrefix(urlStr, "data:") ||
		strings.HasPrefix(urlStr, "javascript:") ||
		strings.HasPrefix(urlStr, "mailto:") ||
		strings.HasPrefix(urlStr, "tel:") ||
		strings.HasPrefix(urlStr, "blob:") {
		return false
	}

	// 跳过模板字符串
	if strings.Contains(urlStr, "${") || strings.Contains(urlStr, "{{") {
		return false
	}

	// 跳过明显的非URL
	if strings.HasPrefix(urlStr, ".") && !strings.HasPrefix(urlStr, "./") && !strings.HasPrefix(urlStr, "../") {
		return false
	}

	// 跳过纯锚点
	if strings.HasPrefix(urlStr, "#") {
		return false
	}

	return true
}

// extractContext 提取URL的上下文
func (ja *JSAnalyzer) extractContext(script, match string) string {
	idx := strings.Index(script, match)
	if idx < 0 {
		return ""
	}

	start := idx - 50
	if start < 0 {
		start = 0
	}
	end := idx + len(match) + 50
	if end > len(script) {
		end = len(script)
	}

	return script[start:end]
}

// detectHTTPMethod 检测HTTP方法
func (ja *JSAnalyzer) detectHTTPMethod(script, match string) string {
	idx := strings.Index(script, match)
	if idx < 0 {
		return "GET"
	}

	// 检查前100个字符
	start := idx - 100
	if start < 0 {
		start = 0
	}
	context := strings.ToLower(script[start:idx])

	methodPatterns := map[string][]string{
		"POST":   {"post", ".post(", "method: 'post'", `method: "post"`, "method:'post'"},
		"PUT":    {"put", ".put(", "method: 'put'", `method: "put"`},
		"DELETE": {"delete", ".delete(", "method: 'delete'", `method: "delete"`},
		"PATCH":  {"patch", ".patch(", "method: 'patch'", `method: "patch"`},
	}

	for method, patterns := range methodPatterns {
		for _, p := range patterns {
			if strings.Contains(context, p) {
				return method
			}
		}
	}

	return "GET"
}

// createAPIEndpoint 创建API端点
func (ja *JSAnalyzer) createAPIEndpoint(urlStr, method, source string) *APIEndpoint {
	parsed, err := url.Parse(urlStr)
	if err != nil {
		return nil
	}

	endpoint := &APIEndpoint{
		Path:       parsed.Path,
		Method:     method,
		Source:     source,
		Confidence: 0.7,
		Params:     make([]APIParam, 0),
	}

	// 提取查询参数
	for key, values := range parsed.Query() {
		param := APIParam{
			Name: key,
			Type: "query",
		}
		if len(values) > 0 {
			param.Example = values[0]
		}
		endpoint.Params = append(endpoint.Params, param)
	}

	// 提取路径参数
	pathParts := strings.Split(parsed.Path, "/")
	for _, part := range pathParts {
		if strings.HasPrefix(part, ":") || (strings.HasPrefix(part, "{") && strings.HasSuffix(part, "}")) {
			param := APIParam{
				Name:     strings.Trim(strings.Trim(part, ":"), "{}"),
				Type:     "path",
				Required: true,
			}
			endpoint.Params = append(endpoint.Params, param)
		}
	}

	return endpoint
}

// maskSensitiveData 脱敏处理
func (ja *JSAnalyzer) maskSensitiveData(data string) string {
	if len(data) <= 8 {
		return "****"
	}
	return data[:4] + "****" + data[len(data)-4:]
}

// deduplicateURLs URL去重
func (ja *JSAnalyzer) deduplicateURLs(urls []*ExtractedURL) []*ExtractedURL {
	seen := make(map[string]*ExtractedURL)
	for _, u := range urls {
		if existing, ok := seen[u.URL]; ok {
			existing.Frequency++
		} else {
			seen[u.URL] = u
		}
	}

	result := make([]*ExtractedURL, 0, len(seen))
	for _, u := range seen {
		result = append(result, u)
	}
	return result
}

// deduplicateAPIs API去重
func (ja *JSAnalyzer) deduplicateAPIs(apis []*APIEndpoint) []*APIEndpoint {
	seen := make(map[string]*APIEndpoint)
	for _, api := range apis {
		key := api.Method + ":" + api.Path
		if existing, ok := seen[key]; ok {
			// 合并参数
			for _, p := range api.Params {
				found := false
				for _, ep := range existing.Params {
					if ep.Name == p.Name {
						found = true
						break
					}
				}
				if !found {
					existing.Params = append(existing.Params, p)
				}
			}
		} else {
			seen[key] = api
		}
	}

	result := make([]*APIEndpoint, 0, len(seen))
	for _, api := range seen {
		result = append(result, api)
	}
	return result
}

// JSAnalysisResult 分析结果
type JSAnalysisResult struct {
	URLs            []*ExtractedURL
	APIEndpoints    []*APIEndpoint
	ExternalScripts []string
	Secrets         map[string][]string
}

// GetURLs 获取所有提取的URL
func (r *JSAnalysisResult) GetURLs() []string {
	if r == nil {
		return nil
	}
	urls := make([]string, len(r.URLs))
	for i, u := range r.URLs {
		urls[i] = u.URL
	}
	return urls
}

// GetAPIURLs 获取API URL
func (r *JSAnalysisResult) GetAPIURLs() []string {
	if r == nil {
		return nil
	}
	var urls []string
	for _, u := range r.URLs {
		if u.Type == "api" {
			urls = append(urls, u.URL)
		}
	}
	return urls
}

// HasSecrets 检查是否发现敏感信息
func (r *JSAnalysisResult) HasSecrets() bool {
	if r == nil {
		return false
	}
	return len(r.Secrets) > 0
}

// AnalyzeScriptContent 分析脚本内容（用于外部获取的脚本）
func (ja *JSAnalyzer) AnalyzeScriptContent(content, source string) *JSAnalysisResult {
	result := &JSAnalysisResult{
		URLs:         make([]*ExtractedURL, 0),
		APIEndpoints: make([]*APIEndpoint, 0),
		Secrets:      make(map[string][]string),
	}

	ja.analyzeScript(content, source, result)
	result.URLs = ja.deduplicateURLs(result.URLs)
	result.APIEndpoints = ja.deduplicateAPIs(result.APIEndpoints)

	return result
}

// ExtractRoutes 从前端路由配置中提取路由
func (ja *JSAnalyzer) ExtractRoutes(page *rod.Page) ([]string, error) {
	result, err := page.Eval(`
		(function() {
			const routes = new Set();

			// Vue Router
			if (window.__VUE_ROUTER__) {
				const router = window.__VUE_ROUTER__;
				if (router.options && router.options.routes) {
					function extractRoutes(routeList, prefix) {
						routeList.forEach(route => {
							if (route.path) {
								routes.add(prefix + route.path);
							}
							if (route.children) {
								extractRoutes(route.children, prefix + (route.path || ''));
							}
						});
					}
					extractRoutes(router.options.routes, '');
				}
			}

			// React Router (检查常见模式)
			const routePatterns = [
				/path:\s*['"]([^'"]+)['"]/g,
				/to:\s*['"]([^'"]+)['"]/g,
				/route\s*\(\s*['"]([^'"]+)['"]/gi
			];

			document.querySelectorAll('script').forEach(script => {
				const content = script.textContent || '';
				routePatterns.forEach(pattern => {
					let match;
					while ((match = pattern.exec(content)) !== null) {
						if (match[1] && match[1].startsWith('/')) {
							routes.add(match[1]);
						}
					}
				});
			});

			return [...routes];
		})()
	`)

	if err != nil {
		return nil, err
	}

	var routes []string
	arr := result.Value.Arr()
	for _, item := range arr {
		if s := item.Str(); s != "" {
			routes = append(routes, s)
		}
	}
	return routes, nil
}
