package arkham

import (
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	ARKHAM_PROD_API_URL = "https://arkm.com/api"
	ARKHAM_PROD_WS_URL  = "wss://arkm.com/ws"
)

func (e *Error) Error() string {
	return fmt.Sprintf("%s(%d): %s", e.Name, e.Id, e.Message)
}

// RequestEditorFn  is the function signature for the RequestEditor callback function
type RequestEditorFn func(ctx context.Context, req *http.Request) error

// Doer performs HTTP requests.
//
// The standard http.Client implements this interface.
type HttpRequestDoer interface {
	Do(req *http.Request) (*http.Response, error)
}

type KeyPair struct {
	ApiKey    string
	ApiSecret []byte
}

func (kp *KeyPair) signHeaders(method, endpoint string, body []byte, headers http.Header) error {
	if kp == nil {
		return nil
	}
	// Create signature - note timestamp is in microseconds
	expires := strconv.Itoa(int(time.Now().Add(5 * time.Second).UnixMicro()))
	h := hmac.New(sha256.New, kp.ApiSecret)
	_, err := h.Write([]byte(kp.ApiKey + expires + method + endpoint + string(body)))
	if err != nil {
		return fmt.Errorf("creating signature: %w", err)
	}
	signature := base64.StdEncoding.EncodeToString(h.Sum(nil))

	headers.Set("Arkham-Api-Key", kp.ApiKey)
	headers.Set("Arkham-Expires", expires)
	headers.Set("Arkham-Signature", signature)

	return nil
}

// Config contains configuration settings for creating a new client.
type Config struct {
	// Optional KeyPair for signing requests, required for authenticated endpoints
	KeyPair *KeyPair

	// Optional ApiURL to override the server URL provided during client creation
	ApiURL string

	// Doer for performing requests, typically a *http.Client with any
	// customized settings, such as certificate chains or telemetry.
	Client HttpRequestDoer

	// Optional WebSocket URL
	WebsocketURL string

	// Optional error handler for errors reading data from the websocket (default: panic on error)
	OnWebsocketReaderError func(err error)
}

func (c *Config) LoadFromEnv() error {
	c.ApiURL = os.Getenv("ARKHAM_API_URL")
	c.WebsocketURL = os.Getenv("ARKHAM_WS_URL")
	if apiKey, ok := os.LookupEnv("ARKHAM_API_KEY"); ok {
		if b64apiSecret, ok := os.LookupEnv("ARKHAM_API_SECRET"); ok {
			apiSecret, err := base64.StdEncoding.DecodeString(b64apiSecret)
			if err != nil {
				return fmt.Errorf("decoding API secret: %w", err)
			}
			c.KeyPair = &KeyPair{ApiKey: apiKey, ApiSecret: apiSecret}
		} else {
			return errors.New("ARKHAM_API_KEY found but ARKHAM_API_SECRET environment variable is not set")
		}
	}
	return nil
}

// Client The Arkham REST API client
type Client struct {
	keyPair    *KeyPair
	apiURL     string
	httpClient HttpRequestDoer
}

// Creates a new Client, with reasonable defaults
func NewClient(config Config) (*Client, error) {
	// create a client with sane default values
	client := Client{}
	client.keyPair = config.KeyPair
	if config.ApiURL != "" {
		client.apiURL = config.ApiURL
	} else {
		client.apiURL = ARKHAM_PROD_API_URL
	}
	// ensure the server URL always has a trailing slash
	if !strings.HasSuffix(client.apiURL, "/") {
		client.apiURL += "/"
	}
	// create httpClient, if not already present
	if config.Client != nil {
		client.httpClient = config.Client
	} else {
		client.httpClient = http.DefaultClient
	}
	return &client, nil
}
