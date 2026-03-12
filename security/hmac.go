package security

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net/http"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	headerTimestamp    = "X-Deployer-Timestamp"
	headerNonce        = "X-Deployer-Nonce"
	headerContentSHA   = "X-Deployer-Content-SHA256"
	headerSignature    = "X-Deployer-Signature"
	maxSkewSeconds     = int64(60)
	nonceWindowSeconds = int64(300)
)

type nonceCache struct {
	mu    sync.Mutex
	items map[string]int64
}

var globalNonceCache = &nonceCache{items: make(map[string]int64)}

func (c *nonceCache) seenOrInsert(window int64, nonce string, now int64) bool {
	key := fmt.Sprintf("nonce:%d:%s", window, nonce)
	exp := now + nonceWindowSeconds

	c.mu.Lock()
	defer c.mu.Unlock()

	for k, v := range c.items {
		if v <= now {
			delete(c.items, k)
		}
	}

	if _, ok := c.items[key]; ok {
		return true
	}
	c.items[key] = exp
	return false
}

func sha256Hex(data []byte) string {
	sum := sha256.Sum256(data)
	return hex.EncodeToString(sum[:])
}

func canonicalString(method, uriPath, timestamp, nonce, contentSHA string) string {
	m := strings.ToUpper(method)
	return m + "\n" + uriPath + "\n" + timestamp + "\n" + nonce + "\n" + contentSHA
}

func signHMACBase64(secret, canonical string) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(canonical))
	return base64.StdEncoding.EncodeToString(mac.Sum(nil))
}

func verifyHMACBase64(secret, canonical, signatureB64 string) bool {
	sigBytes, err := base64.StdEncoding.DecodeString(signatureB64)
	if err != nil {
		return false
	}
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write([]byte(canonical))
	expected := mac.Sum(nil)
	return hmac.Equal(expected, sigBytes)
}

func randomNonce() (string, error) {
	b := make([]byte, 16)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	return hex.EncodeToString(b), nil
}

func SignOutgoingRequest(req *http.Request, body []byte, secret string, uriPathForSignature string) error {
	if secret == "" {
		return errors.New("missing signing secret")
	}
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	nonce, err := randomNonce()
	if err != nil {
		return fmt.Errorf("nonce: %w", err)
	}
	contentSHA := sha256Hex(body)
	canonical := canonicalString(req.Method, uriPathForSignature, ts, nonce, contentSHA)
	signature := signHMACBase64(secret, canonical)

	req.Header.Set(headerTimestamp, ts)
	req.Header.Set(headerNonce, nonce)
	req.Header.Set(headerContentSHA, contentSHA)
	req.Header.Set(headerSignature, signature)
	return nil
}

type VerifyResult struct {
	Timestamp string
	Nonce     string
}

func VerifyIncomingRequest(r *http.Request, rawBody []byte, secret string, uriPathForSignature string) (VerifyResult, int, error) {
	if secret == "" {
		return VerifyResult{}, http.StatusForbidden, errors.New("missing signing secret")
	}

	ts := strings.TrimSpace(r.Header.Get(headerTimestamp))
	nonce := strings.TrimSpace(r.Header.Get(headerNonce))
	contentSHA := strings.TrimSpace(r.Header.Get(headerContentSHA))
	signature := strings.TrimSpace(r.Header.Get(headerSignature))

	if ts == "" || nonce == "" || contentSHA == "" || signature == "" {
		return VerifyResult{}, http.StatusUnauthorized, errors.New("missing headers")
	}

	tsInt, err := strconv.ParseInt(ts, 10, 64)
	if err != nil {
		return VerifyResult{}, http.StatusUnauthorized, errors.New("bad timestamp")
	}

	now := time.Now().Unix()
	if now-tsInt > maxSkewSeconds || tsInt-now > maxSkewSeconds {
		return VerifyResult{}, http.StatusUnauthorized, errors.New("timestamp skew")
	}

	computedSHA := sha256Hex(rawBody)
	if !hmac.Equal([]byte(computedSHA), []byte(strings.ToLower(contentSHA))) {
		return VerifyResult{}, http.StatusUnauthorized, errors.New("bad content sha")
	}

	canonical := canonicalString(r.Method, uriPathForSignature, ts, nonce, computedSHA)
	if !verifyHMACBase64(secret, canonical, signature) {
		return VerifyResult{}, http.StatusForbidden, errors.New("bad signature")
	}

	window := tsInt / nonceWindowSeconds
	if globalNonceCache.seenOrInsert(window, nonce, now) {
		return VerifyResult{}, http.StatusForbidden, errors.New("replay")
	}

	return VerifyResult{Timestamp: ts, Nonce: nonce}, 0, nil
}

func ReadRawBodyPreserve(r *http.Request) ([]byte, error) {
	if r.Body == nil {
		return []byte{}, nil
	}
	b, err := io.ReadAll(r.Body)
	if err != nil {
		return nil, err
	}
	r.Body = io.NopCloser(bytes.NewReader(b))
	return b, nil
}

func LogHMACReject(reason string) {
	log.Printf("HMAC auth failed: %s", reason)
}
