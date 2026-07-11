package auth

import (
	"strings"

	codexauth "github.com/router-for-me/CLIProxyAPI/v7/internal/auth/codex"
)

// CodexAccountTypeFromAuth returns the detected Codex account type for auth.
// It prefers explicitly stored account metadata and falls back to the JWT claim.
func CodexAccountTypeFromAuth(auth *Auth) string {
	if auth == nil || !strings.EqualFold(strings.TrimSpace(auth.Provider), "codex") {
		return ""
	}

	for _, key := range []string{"plan_type", "planType", "account_type", "accountType"} {
		if auth.Attributes != nil {
			if accountType := normalizeCodexAccountType(auth.Attributes[key]); accountType != "" {
				return accountType
			}
		}
		if auth.Metadata != nil {
			if accountType := normalizeCodexAccountType(authMetadataString(auth, key)); accountType != "" {
				return accountType
			}
		}
	}

	if accountType := codexAccountTypeFromIDToken(codexIDTokenFromAuth(auth)); accountType != "" {
		return accountType
	}
	if storage, ok := auth.Storage.(*codexauth.CodexTokenStorage); ok && storage != nil {
		return codexAccountTypeFromIDToken(storage.IDToken)
	}
	return ""
}

func normalizeCodexAccountType(accountType string) string {
	return strings.ToLower(strings.TrimSpace(accountType))
}

func codexIDTokenFromAuth(auth *Auth) string {
	if auth == nil {
		return ""
	}
	if auth.Attributes != nil {
		if idToken := strings.TrimSpace(auth.Attributes["id_token"]); idToken != "" {
			return idToken
		}
	}
	if idToken := authMetadataString(auth, "id_token"); idToken != "" {
		return idToken
	}
	if auth.Metadata == nil {
		return ""
	}
	token, ok := auth.Metadata["token"].(map[string]any)
	if !ok {
		return ""
	}
	idToken, _ := token["id_token"].(string)
	return strings.TrimSpace(idToken)
}

func codexAccountTypeFromIDToken(idToken string) string {
	idToken = strings.TrimSpace(idToken)
	if idToken == "" {
		return ""
	}
	claims, err := codexauth.ParseJWTToken(idToken)
	if err != nil || claims == nil {
		return ""
	}
	return normalizeCodexAccountType(claims.CodexAuthInfo.ChatgptPlanType)
}
