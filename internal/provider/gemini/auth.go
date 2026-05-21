package gemini

import (
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"crypto/x509"
	"encoding/base64"
	"encoding/json"
	"encoding/pem"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"
)

type ServiceAccount struct {
	Type        string `json:"type"`
	ProjectID   string `json:"project_id"`
	PrivateKey  string `json:"private_key"`
	ClientEmail string `json:"client_email"`
	TokenURI    string `json:"token_uri"`
}

func LoadServiceAccount(path string) (*ServiceAccount, error) {
	b, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("credentials を読めません (%s): %w", path, err)
	}
	var sa ServiceAccount
	if err := json.Unmarshal(b, &sa); err != nil {
		return nil, fmt.Errorf("credentials の JSON parse 失敗 (%s): %w", path, err)
	}
	if sa.Type != "service_account" {
		return nil, fmt.Errorf("credentials の type が service_account ではありません: %q", sa.Type)
	}
	if sa.ClientEmail == "" || sa.PrivateKey == "" {
		return nil, fmt.Errorf("credentials に client_email / private_key が含まれていません")
	}
	if sa.TokenURI == "" {
		sa.TokenURI = "https://oauth2.googleapis.com/token"
	}
	return &sa, nil
}

// FetchAccessToken signs a JWT with the SA's private key and exchanges it for
// an OAuth2 access token at the SA's token endpoint.
func (sa *ServiceAccount) FetchAccessToken(scope string) (string, error) {
	key, err := parseRSAPrivateKey(sa.PrivateKey)
	if err != nil {
		return "", err
	}

	now := time.Now()
	header := map[string]string{"alg": "RS256", "typ": "JWT"}
	claims := map[string]any{
		"iss":   sa.ClientEmail,
		"scope": scope,
		"aud":   sa.TokenURI,
		"iat":   now.Unix(),
		"exp":   now.Add(time.Hour).Unix(),
	}
	headerJSON, _ := json.Marshal(header)
	claimsJSON, _ := json.Marshal(claims)

	signingInput := b64url(headerJSON) + "." + b64url(claimsJSON)
	sum := sha256.Sum256([]byte(signingInput))
	sig, err := rsa.SignPKCS1v15(rand.Reader, key, crypto.SHA256, sum[:])
	if err != nil {
		return "", fmt.Errorf("JWT 署名失敗: %w", err)
	}
	jwt := signingInput + "." + b64url(sig)

	form := url.Values{}
	form.Set("grant_type", "urn:ietf:params:oauth:grant-type:jwt-bearer")
	form.Set("assertion", jwt)

	resp, err := http.PostForm(sa.TokenURI, form)
	if err != nil {
		return "", fmt.Errorf("token endpoint への POST 失敗: %w", err)
	}
	defer resp.Body.Close()
	body, _ := io.ReadAll(resp.Body)
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("token endpoint が %d を返しました: %s", resp.StatusCode, strings.TrimSpace(string(body)))
	}
	var tok struct {
		AccessToken string `json:"access_token"`
		ExpiresIn   int    `json:"expires_in"`
	}
	if err := json.Unmarshal(body, &tok); err != nil {
		return "", fmt.Errorf("token レスポンス parse 失敗: %w", err)
	}
	if tok.AccessToken == "" {
		return "", fmt.Errorf("access_token が空: %s", string(body))
	}
	return tok.AccessToken, nil
}

func parseRSAPrivateKey(pemStr string) (*rsa.PrivateKey, error) {
	block, _ := pem.Decode([]byte(pemStr))
	if block == nil {
		return nil, fmt.Errorf("private_key の PEM デコード失敗")
	}
	if key, err := x509.ParsePKCS1PrivateKey(block.Bytes); err == nil {
		return key, nil
	}
	keyAny, err := x509.ParsePKCS8PrivateKey(block.Bytes)
	if err != nil {
		return nil, fmt.Errorf("private_key parse 失敗: %w", err)
	}
	key, ok := keyAny.(*rsa.PrivateKey)
	if !ok {
		return nil, fmt.Errorf("private_key が RSA ではありません")
	}
	return key, nil
}

func b64url(b []byte) string {
	return base64.RawURLEncoding.EncodeToString(b)
}
