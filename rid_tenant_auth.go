package main

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"
)

type ridClaimsContextKey struct{}

// RIDTenantClaims holds verified mw-rid JWT tenant and plan claims.
type RIDTenantClaims struct {
	Subject   string `json:"sub,omitempty"`
	Issuer    string `json:"iss,omitempty"`
	TenantID  string `json:"tenant_id"`
	PlanTier  string `json:"plan_tier"`
	ExpiresAt int64  `json:"exp"`
}

// SignRIDToken creates an HS256 JWT for mw-rid tenant claims.
func SignRIDToken(claims RIDTenantClaims, hmacSecret []byte) (string, error) {
	if len(hmacSecret) == 0 {
		return "", errors.New("empty rid hmac secret")
	}
	headerBytes, err := json.Marshal(map[string]string{"alg": "HS256", "typ": "JWT"})
	if err != nil {
		return "", err
	}
	payloadBytes, err := json.Marshal(claims)
	if err != nil {
		return "", err
	}
	header := base64.RawURLEncoding.EncodeToString(headerBytes)
	payload := base64.RawURLEncoding.EncodeToString(payloadBytes)
	signingInput := header + "." + payload

	mac := hmac.New(sha256.New, hmacSecret)
	mac.Write([]byte(signingInput))
	sig := base64.RawURLEncoding.EncodeToString(mac.Sum(nil))
	return signingInput + "." + sig, nil
}

// ParseAndVerifyRIDToken validates an HS256 mw-rid JWT and enforces tenant_id & plan_tier fail-closed.
func ParseAndVerifyRIDToken(token string, hmacSecret []byte, now time.Time) (RIDTenantClaims, error) {
	var claims RIDTenantClaims
	if len(hmacSecret) == 0 {
		return claims, errors.New("rid jwt secret not configured")
	}
	parts := strings.Split(strings.TrimSpace(token), ".")
	if len(parts) != 3 {
		return claims, errors.New("malformed jwt")
	}

	headerJSON, err := base64.RawURLEncoding.DecodeString(parts[0])
	if err != nil {
		return claims, errors.New("invalid jwt header encoding")
	}
	var header struct {
		Alg string `json:"alg"`
	}
	if err := json.Unmarshal(headerJSON, &header); err != nil || header.Alg != "HS256" {
		return claims, errors.New("unsupported jwt algorithm")
	}

	signingInput := parts[0] + "." + parts[1]
	mac := hmac.New(sha256.New, hmacSecret)
	mac.Write([]byte(signingInput))
	expectedSig := mac.Sum(nil)

	presentedSig, err := base64.RawURLEncoding.DecodeString(parts[2])
	if err != nil || len(presentedSig) != len(expectedSig) || subtle.ConstantTimeCompare(presentedSig, expectedSig) != 1 {
		return claims, errors.New("invalid jwt signature")
	}

	payloadJSON, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return claims, errors.New("invalid jwt payload encoding")
	}
	if err := json.Unmarshal(payloadJSON, &claims); err != nil {
		return claims, errors.New("invalid jwt payload")
	}

	if claims.ExpiresAt <= now.Unix() {
		return claims, errors.New("jwt expired")
	}
	if !templateSlugPattern.MatchString(claims.TenantID) {
		return claims, fmt.Errorf("missing or invalid tenant_id claim")
	}
	if _, known := paasPlanQuotas[claims.PlanTier]; !known {
		return claims, fmt.Errorf("missing or unknown plan_tier claim %q", claims.PlanTier)
	}
	return claims, nil
}

func ridTenantClaimsFromContext(ctx context.Context) (RIDTenantClaims, bool) {
	claims, ok := ctx.Value(ridClaimsContextKey{}).(RIDTenantClaims)
	return claims, ok
}

// requireRIDTenantAuth enforces mw-rid JWT tenant_id and plan_tier claims fail-closed.
func requireRIDTenantAuth(hmacSecret []byte, nowFn func() time.Time, next http.HandlerFunc) http.HandlerFunc {
	if nowFn == nil {
		nowFn = time.Now
	}
	return func(w http.ResponseWriter, r *http.Request) {
		token := strings.TrimSpace(r.Header.Get("X-MW-RID-Token"))
		if token == "" {
			authz := r.Header.Get("Authorization")
			if strings.HasPrefix(authz, "Bearer ") {
				token = strings.TrimSpace(strings.TrimPrefix(authz, "Bearer "))
			}
		}
		if token == "" {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "missing rid token"})
			return
		}

		claims, err := ParseAndVerifyRIDToken(token, hmacSecret, nowFn())
		if err != nil {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusUnauthorized)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": err.Error()})
			return
		}

		ctx := context.WithValue(r.Context(), ridClaimsContextKey{}, claims)
		next(w, r.WithContext(ctx))
	}
}
