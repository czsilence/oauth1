package oauth1

import (
	"bytes"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"encoding/base64"
	"fmt"
	"io/ioutil"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	authorizationHeaderParam  = "Authorization"
	authorizationPrefix       = "OAuth " // trailing space is intentional
	oauthConsumerKeyParam     = "oauth_consumer_key"
	oauthNonceParam           = "oauth_nonce"
	oauthSignatureParam       = "oauth_signature"
	oauthSignatureMethodParam = "oauth_signature_method"
	oauthTimestampParam       = "oauth_timestamp"
	oauthTokenParam           = "oauth_token"
	oauthVersionParam         = "oauth_version"
	oauthCallbackParam        = "oauth_callback"
	oauthVerifierParam        = "oauth_verifier"
	defaultSignatureMethod    = "HMAC-SHA1"
	defaultOauthVersion       = "1.0"
	contentType               = "Content-Type"
	formContentType           = "application/x-www-form-urlencoded"
)

// Signer handles signing requests and setting the authorization header.
type Signer struct {
	config *Config
	clock  clock
	noncer noncer
}

// SetRequestTokenAuthHeader adds the OAuth1 header for the request token
// request (temporary credential) according to RFC 5849 2.1.
func (s *Signer) SetRequestTokenAuthHeader(req *http.Request) error {
	oauthParams := s.commonOAuthParams()
	oauthParams[oauthCallbackParam] = s.config.CallbackURL
	params, err := collectParameters(req, oauthParams)
	if err != nil {
		return err
	}
	signatureBase := signatureBase(req, params)
	signature := signature(s.config.ConsumerSecret, "", signatureBase)
	oauthParams[oauthSignatureParam] = signature
	setAuthorizationHeader(req, authHeaderValue(oauthParams))
	return nil
}

// SetAccessTokenAuthHeader sets the OAuth1 header for the access token request
// (token credential) according to RFC 5849 2.3.
func (s *Signer) SetAccessTokenAuthHeader(req *http.Request, requestToken, requestSecret, verifier string) error {
	oauthParams := s.commonOAuthParams()
	oauthParams[oauthTokenParam] = requestToken
	oauthParams[oauthVerifierParam] = verifier
	params, err := collectParameters(req, oauthParams)
	if err != nil {
		return err
	}
	signatureBase := signatureBase(req, params)
	signature := signature(s.config.ConsumerSecret, requestSecret, signatureBase)
	oauthParams[oauthSignatureParam] = signature
	setAuthorizationHeader(req, authHeaderValue(oauthParams))
	return nil
}

// SetRequestAuthHeader sets the OAuth1 header for making authenticated
// requests with an AccessToken (token credential) according to RFC 5849 3.1.
func (s *Signer) SetRequestAuthHeader(req *http.Request, accessToken *Token) error {
	oauthParams := s.commonOAuthParams()
	oauthParams[oauthTokenParam] = accessToken.Token
	params, err := collectParameters(req, oauthParams)
	if err != nil {
		return err
	}
	signatureBase := signatureBase(req, params)
	signature := signature(s.config.ConsumerSecret, accessToken.TokenSecret, signatureBase)
	oauthParams[oauthSignatureParam] = signature
	setAuthorizationHeader(req, authHeaderValue(oauthParams))
	return nil
}

// commonOAuthParams returns a map of the common OAuth1 protocol parameters,
// excluding the oauth_signature parameter.
func (s *Signer) commonOAuthParams() map[string]string {
	return map[string]string{
		oauthConsumerKeyParam:     s.config.ConsumerKey,
		oauthSignatureMethodParam: defaultSignatureMethod,
		oauthTimestampParam:       strconv.FormatInt(s.epoch(), 10),
		oauthNonceParam:           s.nonce(),
		oauthVersionParam:         defaultOauthVersion,
	}
}

// Returns a base64 encoded random 32 byte string.
func (s *Signer) nonce() string {
	if s.noncer != nil {
		return s.noncer.Nonce()
	}
	b := make([]byte, 32)
	rand.Read(b)
	return base64.StdEncoding.EncodeToString(b)
}

// Returns the Unix epoch seconds.
func (s *Signer) epoch() int64 {
	return s.clock.Now().Unix()
}

// setAuthorizationHeader sets the given headerValue as the request's
// Authorization header.
func setAuthorizationHeader(req *http.Request, headerValue string) {
	req.Header.Set(authorizationHeaderParam, headerValue)
}

// authHeaderValue formats OAuth parameters according to RFC 5849 3.5.1. OAuth
// params are percent encoded, sorted by key (for testability), and joined by
// "=" into pairs. Pairs are joined with a ", " comma separator into a header
// string.
// The given OAuth params should include the "oauth_signature" key.
func authHeaderValue(oauthParams map[string]string) string {
	pairs := sortParameters(encodeParameters(oauthParams))
	return authorizationPrefix + strings.Join(pairs, ", ")
}

// encodeParameters percent encodes parameter keys and values according to
// RFC5849 3.6 and RFC3986 2.1 and returns a new map.
func encodeParameters(params map[string]string) map[string]string {
	encoded := map[string]string{}
	for key, value := range params {
		encoded[PercentEncode(key)] = PercentEncode(value)
	}
	return encoded
}

// sortParameters sorts parameters by key and returns a slice of key=value
// pair strings.
func sortParameters(params map[string]string) []string {
	// sort by key
	keys := make([]string, len(params))
	i := 0
	for key := range params {
		keys[i] = key
		i++
	}
	sort.Strings(keys)
	// parameter join
	pairs := make([]string, len(params))
	for i, key := range keys {
		pairs[i] = fmt.Sprintf("%s=%s", key, params[key])
	}
	return pairs
}

// collectParameters collects request parameters from the request query, OAuth
// parameters (which should exclude oauth_signature), and the request body
// provided the body is single part, form encoded, and the form content type
// header is set. The returned map of collected parameter keys and values
// follow RFC 5849 3.4.1.3, except duplicate parameters are not supported.
func collectParameters(req *http.Request, oauthParams map[string]string) (map[string]string, error) {
	// add oauth, query, and body parameters into params
	params := map[string]string{}
	for key, value := range req.URL.Query() {
		// most backends do not accept duplicate query keys
		params[key] = value[0]
	}
	if req.Body != nil && req.Header.Get(contentType) == formContentType {
		// reads data to a []byte, draining req.Body
		b, err := ioutil.ReadAll(req.Body)
		if err != nil {
			return nil, err
		}
		values, err := url.ParseQuery(string(b))
		if err != nil {
			return nil, err
		}
		for key, value := range values {
			// not supporting params with duplicate keys
			params[key] = value[0]
		}
		// reinitialize Body with ReadCloser over the []byte
		req.Body = ioutil.NopCloser(bytes.NewReader(b))
	}
	for key, value := range oauthParams {
		params[key] = value
	}
	return params, nil
}

// signatureBase combines the uppercase request method, percent encoded base
// string URI, and normalizes the request parameters int a parameter string.
// Returns the OAuth1 signature base string according to RFC5849 3.4.1.
func signatureBase(req *http.Request, params map[string]string) string {
	method := strings.ToUpper(req.Method)
	baseURL := baseURI(req)
	parameterString := normalizedParameterString(params)
	// signature base string constructed accoding to 3.4.1.1
	baseParts := []string{method, PercentEncode(baseURL), PercentEncode(parameterString)}
	return strings.Join(baseParts, "&")
}

// baseURI returns the base string URI of a request according to RFC 5849
// 3.4.1.2. The scheme and host are lowercased, the port is dropped if it
// is 80 or 443, and the path minus query parameters is included.
func baseURI(req *http.Request) string {
	scheme := strings.ToLower(req.URL.Scheme)
	host := strings.ToLower(req.URL.Host)
	if hostPort := strings.Split(host, ":"); len(hostPort) == 2 && (hostPort[1] == "80" || hostPort[1] == "443") {
		host = hostPort[0]
	}
	// TODO: use req.URL.EscapedPath() once Go 1.5 is more generally adopted
	// For now, hacky workaround accomplishes the same internal escaping mode
	// escape(u.Path, encodePath) for proper compliance with the OAuth1 spec.
	path := req.URL.Path
	if path != "" {
		path = strings.Split(req.URL.RequestURI(), "?")[0]
	}
	return fmt.Sprintf("%v://%v%v", scheme, host, path)
}

// parameterString normalizes collected OAuth parameters (which should exclude
// oauth_signature) into a parameter string as defined in RFC 5894 3.4.1.3.2.
// The parameters are encoded, sorted by key, keys and values joined with "&",
// and pairs joined with "=" (e.g. foo=bar&q=gopher).
func normalizedParameterString(params map[string]string) string {
	return strings.Join(sortParameters(encodeParameters(params)), "&")
}

// signature creates a signing key from the consumer and token secrets and
// calculates the HMAC signature bytes of the message using the SHA1 hash.
// Returns the base64 encoded signature.
func signature(consumerSecret, tokenSecret, message string) string {
	signingKey := strings.Join([]string{consumerSecret, tokenSecret}, "&")
	mac := hmac.New(sha1.New, []byte(signingKey))
	mac.Write([]byte(message))
	signatureBytes := mac.Sum(nil)
	return base64.StdEncoding.EncodeToString(signatureBytes)
}

// clock provides a interface for current time providers. A Clock can be used
// in place of calling time.Now() directly.
type clock interface {
	Now() time.Time
}

type realClock struct{}

// newRealClock returns a clock which delegates calls to the time package.
func newRealClock() clock {
	return &realClock{}
}

func (c *realClock) Now() time.Time {
	return time.Now()
}

type noncer interface {
	Nonce() string
}
