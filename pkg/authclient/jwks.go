package authclient

import (
	"context"
	"crypto/rsa"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"math/big"
	"net/http"
	"sync"
	"time"

	"github.com/golang-jwt/jwt/v5"
	"github.com/google/uuid"
)

type Claims struct {
	Email       string   `json:"email"`
	IsSuperuser bool     `json:"is_superuser"`
	IsStaff     bool     `json:"is_staff"`
	Groups      []string `json:"groups"`
	Roles       []string `json:"roles"`
	Permissions []string `json:"permissions"`
	jwt.RegisteredClaims
}

type Verifier struct {
	mu      sync.RWMutex
	keys    map[string]*rsa.PublicKey
	jwksURL string
	issuer  string
	client  *http.Client
}

func NewVerifier(jwksURL, issuer string) *Verifier {
	return &Verifier{
		keys:    make(map[string]*rsa.PublicKey),
		jwksURL: jwksURL,
		issuer:  issuer,
		client:  &http.Client{Timeout: 10 * time.Second},
	}
}

func (v *Verifier) Refresh(ctx context.Context) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, v.jwksURL, nil)
	if err != nil {
		return err
	}
	resp, err := v.client.Do(req)
	if err != nil {
		return fmt.Errorf("fetch jwks: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("jwks status %d", resp.StatusCode)
	}

	var doc struct {
		Keys []struct {
			Kty string `json:"kty"`
			Kid string `json:"kid"`
			N   string `json:"n"`
			E   string `json:"e"`
		} `json:"keys"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&doc); err != nil {
		return fmt.Errorf("decode jwks: %w", err)
	}

	keys := make(map[string]*rsa.PublicKey)
	for _, k := range doc.Keys {
		if k.Kty != "RSA" || k.Kid == "" {
			continue
		}
		pub, err := parseRSAPublicKey(k.N, k.E)
		if err != nil {
			return err
		}
		keys[k.Kid] = pub
	}
	if len(keys) == 0 {
		return fmt.Errorf("no rsa keys in jwks")
	}

	v.mu.Lock()
	v.keys = keys
	v.mu.Unlock()
	return nil
}

func parseRSAPublicKey(nB64, eB64 string) (*rsa.PublicKey, error) {
	nBytes, err := base64.RawURLEncoding.DecodeString(nB64)
	if err != nil {
		return nil, err
	}
	eBytes, err := base64.RawURLEncoding.DecodeString(eB64)
	if err != nil {
		return nil, err
	}
	n := new(big.Int).SetBytes(nBytes)
	e := 0
	for _, b := range eBytes {
		e = e<<8 + int(b)
	}
	return &rsa.PublicKey{N: n, E: e}, nil
}

func (v *Verifier) Verify(tokenString string) (*Claims, uuid.UUID, error) {
	claims := &Claims{}
	token, err := jwt.ParseWithClaims(tokenString, claims, func(t *jwt.Token) (any, error) {
		if t.Method.Alg() != jwt.SigningMethodRS256.Alg() {
			return nil, fmt.Errorf("unexpected signing method")
		}
		kid, _ := t.Header["kid"].(string)
		v.mu.RLock()
		defer v.mu.RUnlock()
		if kid != "" {
			if key, ok := v.keys[kid]; ok {
				return key, nil
			}
		}
		for _, key := range v.keys {
			return key, nil
		}
		return nil, fmt.Errorf("no verification key")
	}, jwt.WithIssuer(v.issuer))
	if err != nil {
		return nil, uuid.Nil, err
	}
	if !token.Valid {
		return nil, uuid.Nil, fmt.Errorf("invalid token")
	}
	if len(claims.Groups) == 0 && len(claims.Roles) > 0 {
		claims.Groups = claims.Roles
	}
	sub, err := uuid.Parse(claims.Subject)
	if err != nil {
		return nil, uuid.Nil, fmt.Errorf("invalid subject")
	}
	return claims, sub, nil
}

func HasPermission(claims *Claims, codename string) bool {
	if claims == nil {
		return false
	}
	if claims.IsSuperuser {
		return true
	}
	for _, p := range claims.Permissions {
		if p == codename {
			return true
		}
	}
	return false
}
