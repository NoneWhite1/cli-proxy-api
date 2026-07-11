package auth

import (
	"encoding/base64"
	"testing"
)

func TestCodexAccountTypeFromAuth(t *testing.T) {
	tests := []struct {
		name string
		auth *Auth
		want string
	}{
		{
			name: "attribute",
			auth: &Auth{Provider: "codex", Attributes: map[string]string{"plan_type": " Plus "}},
			want: "plus",
		},
		{
			name: "metadata",
			auth: &Auth{Provider: "codex", Metadata: map[string]any{"accountType": "Free"}},
			want: "free",
		},
		{
			name: "nested token",
			auth: &Auth{Provider: "codex", Metadata: map[string]any{"token": map[string]any{"id_token": codexAccountTypeIDTokenForTest("team")}}},
			want: "team",
		},
		{
			name: "other provider",
			auth: &Auth{Provider: "gemini", Attributes: map[string]string{"plan_type": "plus"}},
			want: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := CodexAccountTypeFromAuth(tt.auth); got != tt.want {
				t.Fatalf("CodexAccountTypeFromAuth() = %q, want %q", got, tt.want)
			}
		})
	}
}

func codexAccountTypeIDTokenForTest(accountType string) string {
	header := base64.RawURLEncoding.EncodeToString([]byte(`{"alg":"none"}`))
	payload := base64.RawURLEncoding.EncodeToString([]byte(`{"https://api.openai.com/auth":{"chatgpt_plan_type":"` + accountType + `"}}`))
	return header + "." + payload + ".signature"
}
