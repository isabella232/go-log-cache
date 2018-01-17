package logcache

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"sync"
	"time"
)

// Oauth2HTTPClient sets the "Authorization" header of any outgoing request.
// It gets a JWT from the configured Oauth2 server. It only gets a new JWT
// when a request comes back with a 401.
type Oauth2HTTPClient struct {
	c            HTTPClient
	oauth2Addr   string
	client       string
	clientSecret string

	mu    sync.Mutex
	token string
}

// NewOauth2HTTPClient creates a new Oauth2HTTPClient.
func NewOauth2HTTPClient(oauth2Addr, client, clientSecret string, opts ...Oauth2Option) *Oauth2HTTPClient {
	c := &Oauth2HTTPClient{
		oauth2Addr:   oauth2Addr,
		client:       client,
		clientSecret: clientSecret,

		c: &http.Client{
			Timeout: 5 * time.Second,
		},
	}

	for _, o := range opts {
		o.configure(c)
	}

	return c
}

// Oauth2Option configures the Oauth2HTTPClient.
type Oauth2Option interface {
	configure(c *Oauth2HTTPClient)
}

// WithOauth2HTTPClient sets the HTTPClient for the Oauth2HTTPClient. It
// defaults to the same default as Client.
func WithOauth2HTTPClient(client HTTPClient) Oauth2Option {
	return oauth2HTTPClientOptionFunc(func(c *Oauth2HTTPClient) {
		c.c = client
	})
}

// oauth2HTTPClientOptionFunc enables a function to be a
// Oauth2Option.
type oauth2HTTPClientOptionFunc func(c *Oauth2HTTPClient)

// configure implements Oauth2Option.
func (f oauth2HTTPClientOptionFunc) configure(c *Oauth2HTTPClient) {
	f(c)
}

// Do implements HTTPClient. It adds the Authorization header to the request
// (unless the header already exists). If the token is expired, it will reach
// out the Oauth2 server and get a new one. The given error CAN be from the
// request to the Oauth2 server.
//
// Do modifies the given Request. It is invalid to use the same Request
// instance on multiple go-routines.
func (c *Oauth2HTTPClient) Do(req *http.Request) (*http.Response, error) {
	if _, ok := req.Header["Authorization"]; ok {
		// Authorization Header is pre-populated, so just do the request.
		return c.c.Do(req)
	}

	token, err := c.getToken()
	if err != nil {
		return nil, err
	}
	req.Header.Set("Authorization", token)

	resp, err := c.c.Do(req)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode == http.StatusUnauthorized {
		c.token = ""
		req.Header.Del("Authorization")
		return c.Do(req)
	}

	return resp, nil
}

func (c *Oauth2HTTPClient) getToken() (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	if c.token != "" {
		return c.token, nil
	}

	req, err := http.NewRequest("POST", c.oauth2Addr, nil)
	if err != nil {
		return "", err
	}
	req.URL.Path = "/oauth/token"

	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")

	v := make(url.Values)
	v.Set("client_id", c.client)
	v.Set("grant_type", "client_credentials")
	req.URL.RawQuery = v.Encode()

	req.URL.User = url.UserPassword(c.client, c.clientSecret)

	resp, err := c.c.Do(req)
	if err != nil {
		return "", err
	}

	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("unexpected status code from Oauth2 server %d", resp.StatusCode)
	}

	token := struct {
		TokenType   string `json:"token_type"`
		AccessToken string `json:"access_token"`
	}{}

	if err := json.NewDecoder(resp.Body).Decode(&token); err != nil {
		return "", fmt.Errorf("failed to unmarshal response from Oauth2 server: %s", err)
	}

	c.token = fmt.Sprintf("%s %s", token.TokenType, token.AccessToken)

	return c.token, nil
}
