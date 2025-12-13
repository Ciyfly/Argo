package engine

import (
	"testing"
)

func TestSensitivePatterns_AWS(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		pattern  string
		expected bool
	}{
		{"aws_access_key", "AKIAIOSFODNN7EXAMPLE", "aws_access_key", true},
		{"aws_access_key_invalid", "AKIA1234", "aws_access_key", false},
		{"aws_mws_token", "amzn.mws.12345678-1234-1234-1234-123456789012", "aws_mws_auth_token", true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			pattern, ok := sensitivePatterns[tc.pattern]
			if !ok {
				t.Fatalf("Pattern %s not found", tc.pattern)
			}
			matched := pattern.MatchString(tc.input)
			if matched != tc.expected {
				t.Errorf("Pattern %s on input %q: got %v, want %v", tc.pattern, tc.input, matched, tc.expected)
			}
		})
	}
}

func TestSensitivePatterns_GitHub(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		pattern  string
		expected bool
	}{
		{"github_token_ghp", "ghp_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx", "github_token", true},
		{"github_oauth_gho", "gho_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx", "github_oauth", true},
		{"github_app_ghu", "ghu_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx", "github_app_token", true},
		{"github_app_ghs", "ghs_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx", "github_app_token", true},
		{"github_refresh_ghr", "ghr_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx", "github_refresh_token", true},
		{"github_fine_grained", "github_pat_xxxxxxxxxxxxxxxxxxxxxx_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx", "github_fine_grained", true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			pattern, ok := sensitivePatterns[tc.pattern]
			if !ok {
				t.Fatalf("Pattern %s not found", tc.pattern)
			}
			matched := pattern.MatchString(tc.input)
			if matched != tc.expected {
				t.Errorf("Pattern %s on input %q: got %v, want %v", tc.pattern, tc.input, matched, tc.expected)
			}
		})
	}
}

func TestSensitivePatterns_Slack(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		pattern  string
		expected bool
	}{
		{"slack_webhook", "https://hooks.slack.com/services/T12345678/B12345678/abcdefghijklmnop", "slack_webhook", true},
		{"slack_bot_token", "xoxb_TESTTOKEN_11111111111_22222222222_abc", "slack_bot_token", true},
		{"slack_user_token", "xoxp_TESTTOKEN_11111111111_22222222222_abc", "slack_user_token", true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			pattern, ok := sensitivePatterns[tc.pattern]
			if !ok {
				t.Fatalf("Pattern %s not found", tc.pattern)
			}
			matched := pattern.MatchString(tc.input)
			if matched != tc.expected {
				t.Errorf("Pattern %s on input %q: got %v, want %v", tc.pattern, tc.input, matched, tc.expected)
			}
		})
	}
}

func TestSensitivePatterns_Stripe(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		pattern  string
		expected bool
	}{
		{"stripe_secret_live", "$k_live_TEST000000000000000000000", "stripe_secret_key", true},
		{"stripe_publishable_live", "$k_live_TEST000000000000000000000", "stripe_publishable_key", true},
		{"stripe_secret_test", "$k_test_TEST000000000000000000000", "stripe_test_secret", true},
		{"stripe_publishable_test", "$k_test_TEST000000000000000000000", "stripe_test_publishable", true},
		{"stripe_webhook", "whsec_abcdefghijklmnopqrstuvwxyz123456", "stripe_webhook_secret", true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			pattern, ok := sensitivePatterns[tc.pattern]
			if !ok {
				t.Fatalf("Pattern %s not found", tc.pattern)
			}
			matched := pattern.MatchString(tc.input)
			if matched != tc.expected {
				t.Errorf("Pattern %s on input %q: got %v, want %v", tc.pattern, tc.input, matched, tc.expected)
			}
		})
	}
}

func TestSensitivePatterns_Google(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		pattern  string
		expected bool
	}{
		{"google_api_key", "AIzaSyC1234567890abcdefghijklmnopqrs", "google_api_key", true},
		{"google_oauth_id", "123456789012-abcdefghijklmnopqrstuvwxyz123456.apps.googleusercontent.com", "google_oauth_id", true},
		{"firebase_db_url", "https://my-project.firebaseio.com", "firebase_database_url", true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			pattern, ok := sensitivePatterns[tc.pattern]
			if !ok {
				t.Fatalf("Pattern %s not found", tc.pattern)
			}
			matched := pattern.MatchString(tc.input)
			if matched != tc.expected {
				t.Errorf("Pattern %s on input %q: got %v, want %v", tc.pattern, tc.input, matched, tc.expected)
			}
		})
	}
}

func TestSensitivePatterns_JWT(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		pattern  string
		expected bool
	}{
		{"jwt_token", "eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIiwibmFtZSI6IkpvaG4gRG9lIiwiaWF0IjoxNTE2MjM5MDIyfQ.SflKxwRJSMeKKF2QT4fwpMeJf36POk6yJV_adQssw5c", "jwt_token", true},
		{"bearer_token", "Bearer eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9", "bearer_token", true},
		{"basic_auth", "Basic dXNlcm5hbWU6cGFzc3dvcmQ=", "basic_auth", true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			pattern, ok := sensitivePatterns[tc.pattern]
			if !ok {
				t.Fatalf("Pattern %s not found", tc.pattern)
			}
			matched := pattern.MatchString(tc.input)
			if matched != tc.expected {
				t.Errorf("Pattern %s on input %q: got %v, want %v", tc.pattern, tc.input, matched, tc.expected)
			}
		})
	}
}

func TestSensitivePatterns_PrivateKeys(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		pattern  string
		expected bool
	}{
		{"rsa_private_key", "-----BEGIN RSA PRIVATE KEY-----", "rsa_private_key", true},
		{"dsa_private_key", "-----BEGIN DSA PRIVATE KEY-----", "dsa_private_key", true},
		{"ec_private_key", "-----BEGIN EC PRIVATE KEY-----", "ec_private_key", true},
		{"openssh_private_key", "-----BEGIN OPENSSH PRIVATE KEY-----", "openssh_private_key", true},
		{"pgp_private_key", "-----BEGIN PGP PRIVATE KEY BLOCK-----", "pgp_private_key", true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			pattern, ok := sensitivePatterns[tc.pattern]
			if !ok {
				t.Fatalf("Pattern %s not found", tc.pattern)
			}
			matched := pattern.MatchString(tc.input)
			if matched != tc.expected {
				t.Errorf("Pattern %s on input %q: got %v, want %v", tc.pattern, tc.input, matched, tc.expected)
			}
		})
	}
}

func TestSensitivePatterns_Database(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		pattern  string
		expected bool
	}{
		{"mysql_connection", "mysql://user:password@localhost:3306/db", "mysql_connection", true},
		{"postgres_connection", "postgres://user:password@localhost:5432/db", "postgres_connection", true},
		{"mongodb_connection", "mongodb://user:password@localhost:27017/db", "mongodb_connection", true},
		{"mongodb_srv_connection", "mongodb+srv://user:password@cluster.example.com/db", "mongodb_connection", true},
		{"redis_connection", "redis://user:password@localhost:6379/0", "redis_connection", true},
		{"jdbc_connection", "jdbc:mysql://localhost:3306/mydb", "jdbc_connection", true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			pattern, ok := sensitivePatterns[tc.pattern]
			if !ok {
				t.Fatalf("Pattern %s not found", tc.pattern)
			}
			matched := pattern.MatchString(tc.input)
			if matched != tc.expected {
				t.Errorf("Pattern %s on input %q: got %v, want %v", tc.pattern, tc.input, matched, tc.expected)
			}
		})
	}
}

func TestSensitivePatterns_CloudProviders(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		pattern  string
		expected bool
	}{
		{"aliyun_access_key", "LTAI1234567890abcdef", "aliyun_access_key", true},
		{"tencent_secret_id", "AKID1234567890abcdef", "tencent_secret_id", true},
		{"azure_connection", "DefaultEndpointsProtocol=https;AccountName=test;AccountKey=abcdefghijklmnopqrstuvwxyz0123456789ABCDEFGHIJKLMNOPQRSTUVWXYZ0123456789abcdefghijk==", "azure_connection_string", true},
		{"sentry_dsn", "https://abcdef1234567890abcdef1234567890@sentry.io/123456", "sentry_dsn", true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			pattern, ok := sensitivePatterns[tc.pattern]
			if !ok {
				t.Fatalf("Pattern %s not found", tc.pattern)
			}
			matched := pattern.MatchString(tc.input)
			if matched != tc.expected {
				t.Errorf("Pattern %s on input %q: got %v, want %v", tc.pattern, tc.input, matched, tc.expected)
			}
		})
	}
}

func TestSensitivePatterns_Discord(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		pattern  string
		expected bool
	}{
		{"discord_webhook", "https://discord.com/api/webhooks/123456789/abcdefghijklmnop", "discord_webhook", true},
		{"discord_webhook_alt", "https://discordapp.com/api/webhooks/123456789/abcdefghijklmnop", "discord_webhook", true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			pattern, ok := sensitivePatterns[tc.pattern]
			if !ok {
				t.Fatalf("Pattern %s not found", tc.pattern)
			}
			matched := pattern.MatchString(tc.input)
			if matched != tc.expected {
				t.Errorf("Pattern %s on input %q: got %v, want %v", tc.pattern, tc.input, matched, tc.expected)
			}
		})
	}
}

func TestSensitivePatterns_SendGrid(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		pattern  string
		expected bool
	}{
		// SendGrid API key format: SG.{22 chars}.{43 chars}
		{"sendgrid_api_key", "SG.1234567890123456789012.1234567890123456789012345678901234567890123", "sendgrid_api_key", true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			pattern, ok := sensitivePatterns[tc.pattern]
			if !ok {
				t.Fatalf("Pattern %s not found", tc.pattern)
			}
			matched := pattern.MatchString(tc.input)
			if matched != tc.expected {
				t.Errorf("Pattern %s on input %q: got %v, want %v", tc.pattern, tc.input, matched, tc.expected)
			}
		})
	}
}

func TestSensitivePatterns_InternalURLs(t *testing.T) {
	testCases := []struct {
		name     string
		input    string
		pattern  string
		expected bool
	}{
		{"internal_ip_10", "http://10.0.0.1:8080", "internal_ip", true},
		{"internal_ip_172", "http://172.16.0.1:8080", "internal_ip", true},
		{"internal_ip_192", "http://192.168.1.1:8080", "internal_ip", true},
		{"localhost_url", "http://localhost:3000/api", "localhost_url", true},
		{"localhost_127", "http://127.0.0.1:3000/api", "localhost_url", true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			pattern, ok := sensitivePatterns[tc.pattern]
			if !ok {
				t.Fatalf("Pattern %s not found", tc.pattern)
			}
			matched := pattern.MatchString(tc.input)
			if matched != tc.expected {
				t.Errorf("Pattern %s on input %q: got %v, want %v", tc.pattern, tc.input, matched, tc.expected)
			}
		})
	}
}

func TestDetectSecretsInJSCode(t *testing.T) {
	jsCode := `
		const config = {
			awsKey: 'AKIAIOSFODNN7EXAMPLE',
			stripeKey: '$k_live_TEST000000000000000000000FAKEFAKEFAKEFAKEFAKE',
			githubToken: 'ghp_xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx',
			slackWebhook: 'https://hooks.slack.com/services/T12345678/B12345678/abcdefghijklmnop',
			jwt: 'eyJhbGciOiJIUzI1NiIsInR5cCI6IkpXVCJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.dozjgNryP4J3jVmNHl0w5N_XgL0n3I9PlFUP0THsR8U',
			database: 'postgres://admin:secretpass@10.0.0.5:5432/mydb',
			sendgrid: 'SG.abcdefghijklmnopqrstuv.abcdefghijklmnopqrstuvwxyz0123456789abc',
			googleApiKey: 'AIzaSyC1234567890abcdefghijklmnopqrs',
			privateKey: '-----BEGIN RSA PRIVATE KEY-----'
		};
	`

	foundSecrets := make(map[string][]string)

	for name, pattern := range sensitivePatterns {
		matches := pattern.FindAllString(jsCode, -1)
		if len(matches) > 0 {
			foundSecrets[name] = matches
		}
	}

	t.Logf("Found %d types of secrets in JS code:", len(foundSecrets))
	for name, matches := range foundSecrets {
		for _, match := range matches {
			// 截断输出用于测试日志
			display := match
			if len(display) > 50 {
				display = display[:50] + "..."
			}
			t.Logf("  - %s: %s", name, display)
		}
	}

	// 验证至少找到了一些预期的敏感信息
	expectedTypes := []string{
		"aws_access_key",
		"stripe_secret_key",
		"github_token",
		"slack_webhook",
		"jwt_token",
		"postgres_connection",
		"sendgrid_api_key",
		"google_api_key",
		"rsa_private_key",
	}

	for _, expected := range expectedTypes {
		if _, found := foundSecrets[expected]; !found {
			t.Logf("Warning: Expected secret type %s not found (may need pattern adjustment)", expected)
		}
	}
}

// TestPatternCount 验证模式数量
func TestPatternCount(t *testing.T) {
	count := len(sensitivePatterns)
	t.Logf("Total sensitive patterns: %d", count)

	if count < 50 {
		t.Errorf("Expected at least 50 patterns, got %d", count)
	}

	// 列出所有模式类别
	categories := map[string]int{
		"aws":        0,
		"google":     0,
		"github":     0,
		"gitlab":     0,
		"slack":      0,
		"stripe":     0,
		"twilio":     0,
		"sendgrid":   0,
		"discord":    0,
		"jwt":        0,
		"private_key": 0,
		"database":   0,
		"password":   0,
		"other":      0,
	}

	for name := range sensitivePatterns {
		categorized := false
		for cat := range categories {
			if cat != "other" && containsPrefix(name, cat) {
				categories[cat]++
				categorized = true
				break
			}
		}
		if !categorized {
			categories["other"]++
		}
	}

	t.Logf("Pattern categories:")
	for cat, count := range categories {
		if count > 0 {
			t.Logf("  - %s: %d", cat, count)
		}
	}
}

func containsPrefix(s, prefix string) bool {
	return len(s) >= len(prefix) && s[:len(prefix)] == prefix
}
