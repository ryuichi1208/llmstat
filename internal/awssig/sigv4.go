// Package awssig implements just enough of AWS Signature Version 4 to sign
// outgoing http.Request values for the Bedrock runtime API. It intentionally
// avoids pulling in aws-sdk-go-v2 so the resulting binary stays small.
package awssig

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
	"time"
)

type Credentials struct {
	AccessKeyID     string
	SecretAccessKey string
	SessionToken    string
}

type Options struct {
	// IncludeContentSha256Header attaches the payload sha256 as the
	// X-Amz-Content-Sha256 header (required by some AWS services, e.g. S3).
	// When false, only X-Amz-Date is added beyond Authorization.
	IncludeContentSha256Header bool
}

// Sign mutates req in place, attaching SigV4 headers and returning nil on
// success. payload is the request body bytes used in the signature.
func Sign(req *http.Request, payload []byte, service, region string, creds Credentials, now time.Time) error {
	return SignWith(req, payload, service, region, creds, now, Options{
		IncludeContentSha256Header: true,
	})
}

func SignWith(req *http.Request, payload []byte, service, region string, creds Credentials, now time.Time, opts Options) error {
	_, _, _, err := signCommon(req, payload, service, region, creds, now, opts)
	return err
}

// SignAndCanonical is a test helper exposing the canonical request and
// string-to-sign alongside the final signature.
func SignAndCanonical(req *http.Request, payload []byte, service, region string, creds Credentials, now time.Time, opts Options) (canonical, stringToSign, signature string, err error) {
	return signCommon(req, payload, service, region, creds, now, opts)
}

func signCommon(req *http.Request, payload []byte, service, region string, creds Credentials, now time.Time, opts Options) (canonical, stringToSign, signature string, err error) {
	if !req.URL.IsAbs() {
		return "", "", "", fmt.Errorf("awssig: req.URL must be absolute")
	}
	amzDate := now.UTC().Format("20060102T150405Z")
	shortDate := now.UTC().Format("20060102")
	payloadHashHex := hashHex(payload)

	req.Header.Set("X-Amz-Date", amzDate)
	if opts.IncludeContentSha256Header {
		req.Header.Set("X-Amz-Content-Sha256", payloadHashHex)
	}
	if req.Host == "" {
		req.Host = req.URL.Host
	}
	if creds.SessionToken != "" {
		req.Header.Set("X-Amz-Security-Token", creds.SessionToken)
	}

	var signedHeaders string
	canonical, signedHeaders = buildCanonicalRequest(req, payloadHashHex)
	credentialScope := fmt.Sprintf("%s/%s/%s/aws4_request", shortDate, region, service)
	stringToSign = strings.Join([]string{
		"AWS4-HMAC-SHA256",
		amzDate,
		credentialScope,
		hashHex([]byte(canonical)),
	}, "\n")
	signingKey := deriveSigningKey(creds.SecretAccessKey, shortDate, region, service)
	signature = hex.EncodeToString(hmacSHA256(signingKey, []byte(stringToSign)))

	auth := fmt.Sprintf("AWS4-HMAC-SHA256 Credential=%s/%s, SignedHeaders=%s, Signature=%s",
		creds.AccessKeyID, credentialScope, signedHeaders, signature)
	req.Header.Set("Authorization", auth)
	return canonical, stringToSign, signature, nil
}

func buildCanonicalRequest(req *http.Request, payloadHashHex string) (canonical, signedHeaders string) {
	method := req.Method
	canonicalURI := canonicalPath(req.URL.EscapedPath())
	canonicalQuery := canonicalQueryString(req.URL.Query())

	headerKeys := make([]string, 0, len(req.Header)+1)
	headerValues := map[string]string{}

	const hostKey = "host"
	headerKeys = append(headerKeys, hostKey)
	headerValues[hostKey] = stripPort(req.URL.Host)

	for k, vs := range req.Header {
		lk := strings.ToLower(k)
		if lk == "authorization" {
			continue
		}
		headerKeys = append(headerKeys, lk)
		joined := strings.Join(vs, ",")
		headerValues[lk] = trimAll(joined)
	}
	sort.Strings(headerKeys)
	headerKeys = slices.Compact(headerKeys)

	var canonHeaders strings.Builder
	signed := make([]string, 0, len(headerKeys))
	for _, k := range headerKeys {
		canonHeaders.WriteString(k)
		canonHeaders.WriteByte(':')
		canonHeaders.WriteString(headerValues[k])
		canonHeaders.WriteByte('\n')
		signed = append(signed, k)
	}
	signedHeaders = strings.Join(signed, ";")

	canonical = strings.Join([]string{
		method,
		canonicalURI,
		canonicalQuery,
		canonHeaders.String(),
		signedHeaders,
		payloadHashHex,
	}, "\n")
	return canonical, signedHeaders
}

func canonicalPath(p string) string {
	if p == "" {
		return "/"
	}
	return p
}

func canonicalQueryString(values url.Values) string {
	keys := make([]string, 0, len(values))
	for k := range values {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var b strings.Builder
	for i, k := range keys {
		vs := values[k]
		sort.Strings(vs)
		for j, v := range vs {
			if i > 0 || j > 0 {
				b.WriteByte('&')
			}
			b.WriteString(url.QueryEscape(k))
			b.WriteByte('=')
			b.WriteString(url.QueryEscape(v))
		}
	}
	return b.String()
}

func deriveSigningKey(secret, shortDate, region, service string) []byte {
	kDate := hmacSHA256([]byte("AWS4"+secret), []byte(shortDate))
	kRegion := hmacSHA256(kDate, []byte(region))
	kService := hmacSHA256(kRegion, []byte(service))
	return hmacSHA256(kService, []byte("aws4_request"))
}

func hmacSHA256(key, data []byte) []byte {
	m := hmac.New(sha256.New, key)
	m.Write(data)
	return m.Sum(nil)
}

func hashHex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

func stripPort(host string) string {
	if i := strings.IndexByte(host, ':'); i >= 0 {
		return host[:i]
	}
	return host
}

// trimAll collapses repeated inner spaces and trims surrounding whitespace,
// matching the AWS canonical-header requirement.
func trimAll(s string) string {
	s = strings.TrimSpace(s)
	for strings.Contains(s, "  ") {
		s = strings.ReplaceAll(s, "  ", " ")
	}
	return s
}
