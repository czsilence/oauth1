package oauth1

import (
	"errors"
	"io/ioutil"
	"net/http"
	"net/url"
)

const (
	oauthTokenSecretParam       = "oauth_token_secret"
	oauthCallbackConfirmedParam = "oauth_callback_confirmed"
)

// Config represents an OAuth1 consumer's (client's) key and secret, the
// callback URL, and the provider Endpoint to which the consumer corresponds.
type Config struct {
	// Consumer Key (Client Identifier)
	ConsumerKey string
	// Consumer Secret (Client Shared-Secret)
	ConsumerSecret string
	// Callback URL
	CallbackURL string
	// Provider Endpoint specifying OAuth1 endpoint URLs
	Endpoint Endpoint
}

// NewConfig returns a new Config with the given consumer key and secret.
func NewConfig(consumerKey, consumerSecret string) *Config {
	return &Config{
		ConsumerKey:    consumerKey,
		ConsumerSecret: consumerSecret,
	}
}

// Client returns an HTTP client which uses the provided access Token.
func (c *Config) Client(t *Token) *http.Client {
	return NewClient(c, t)
}

// NewClient returns a new http Client which signs requests via OAuth1.
func NewClient(config *Config, token *Token) *http.Client {
	transport := &Transport{
		source: StaticTokenSource(token),
		signer: &Signer{config: config, clock: newRealClock()},
	}
	return &http.Client{Transport: transport}
}

// RequestToken obtains a Request token and secret (temporary credential) by
// POSTing a request (with oauth_callback in the auth header) to the Endpoint
// RequestTokenURL. The response body form is validated to ensure
// oauth_callback_confirmed is true. Returns the request token and secret
// (temporary credentials).
// See RFC 5849 2.1 Temporary Credentials.
func (c *Config) RequestToken() (requestToken, requestSecret string, err error) {
	req, err := http.NewRequest("POST", c.Endpoint.RequestTokenURL, nil)
	if err != nil {
		return "", "", err
	}
	signer := &Signer{config: c, clock: newRealClock()}
	signer.SetRequestTokenAuthHeader(req)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", err
	}
	// when err is nil, resp contains a non-nil resp.Body which must be closed
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", "", err
	}
	// ParseQuery to decode URL-encoded application/x-www-form-urlencoded body
	values, err := url.ParseQuery(string(body))
	if err != nil {
		return "", "", err
	}
	if values.Get(oauthCallbackConfirmedParam) != "true" {
		return "", "", errors.New("oauth_callback_confirmed was not true")
	}
	requestToken = values.Get(oauthTokenParam)
	requestSecret = values.Get(oauthTokenSecretParam)
	if requestToken == "" || requestSecret == "" {
		return "", "", errors.New("RequestToken response missing oauth token or secret")
	}
	return requestToken, requestSecret, nil
}

// AuthorizationURL accepts a request token and returns the *url.URL to the
// Endpoint's authorization page that asks the user (resource owner) for to
// authorize the consumer to act on his/her/its behalf.
// See RFC 5849 2.2 Resource Owner Authorization.
func (c *Config) AuthorizationURL(requestToken string) (*url.URL, error) {
	authorizationURL, err := url.Parse(c.Endpoint.AuthorizeURL)
	if err != nil {
		return nil, err
	}
	values := authorizationURL.Query()
	values.Add(oauthTokenParam, requestToken)
	authorizationURL.RawQuery = values.Encode()
	return authorizationURL, nil
}

// HandleAuthorizationCallback handles an OAuth1 authorization callback GET
// http.Request from a provider server. The oauth_token and oauth_verifier
// query parameters are parsed to return the request token from earlier in the
// flow and the verifier string.
// See RFC 5849 2.2 Resource Owner Authorization.
func (c *Config) HandleAuthorizationCallback(req *http.Request) (requestToken, verifier string, err error) {
	// parse the raw query from the URL into req.Form
	err = req.ParseForm()
	if err != nil {
		return "", "", err
	}
	requestToken = req.Form.Get(oauthTokenParam)
	verifier = req.Form.Get(oauthVerifierParam)
	if requestToken == "" || verifier == "" {
		return "", "", errors.New("callback did not receive an oauth_token or oauth_verifier")
	}
	return requestToken, verifier, nil
}

// AccessToken obtains an access token (token credential) by POSTing a
// request (with oauth_token and oauth_verifier in the auth header) to the
// Endpoint AccessTokenURL. Returns the access token and secret (token
// credentials).
// See RFC 5849 2.3 Token Credentials.
func (c *Config) AccessToken(requestToken, requestSecret, verifier string) (accessToken, accessSecret string, err error) {
	req, err := http.NewRequest("POST", c.Endpoint.AccessTokenURL, nil)
	if err != nil {
		return "", "", err
	}
	signer := &Signer{config: c, clock: newRealClock()}
	signer.SetAccessTokenAuthHeader(req, requestToken, requestSecret, verifier)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", "", err
	}
	// when err is nil, resp contains a non-nil resp.Body which must be closed
	defer resp.Body.Close()
	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return "", "", err
	}
	// ParseQuery to decode URL-encoded application/x-www-form-urlencoded body
	values, err := url.ParseQuery(string(body))
	if err != nil {
		return "", "", err
	}
	accessToken = values.Get(oauthTokenParam)
	accessSecret = values.Get(oauthTokenSecretParam)
	if accessToken == "" || accessSecret == "" {
		return "", "", errors.New("AccessToken response missing access token or secret")
	}
	return accessToken, accessSecret, nil
}
