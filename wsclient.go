package arkham

import (
	"bytes"
	"cmp"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"sync"
	"time"

	"github.com/google/uuid"
	"github.com/gorilla/websocket"
)

// Handler represents a callback function for handling WebSocket data
type HandlerFunc func(t WebsocketDataType, data []byte) error

// UnsubscribeFunc represents a function to unsubscribe from a channel
type UnsubscribeFunc func() error

// pendingRequest represents a pending WebSocket request waiting for response
type pendingRequest struct {
	event    chan struct{}
	response []byte
	error    error
}

// ArkhamWebSocket represents a WebSocket client for Arkham API
type ArkhamWebSocket struct {
	// Configuration
	keyPair      *KeyPair
	websocketURL string

	// Connection state
	conn      *websocket.Conn
	end       chan struct{}
	err       error
	connMutex sync.RWMutex

	onError func(err error)

	// Subscription management
	subscriptions map[string]map[string]HandlerFunc
	subMutex      sync.RWMutex

	// Request/response handling
	pendingRequests map[string]*pendingRequest
	requestMutex    sync.RWMutex
}

var ErrNotConnected = errors.New("WebSocket client is not connected")

// NewArkhamWebSocket creates a new WebSocket client instance
func NewArkhamWebSocket(config Config) *ArkhamWebSocket {
	cli := ArkhamWebSocket{
		keyPair:         config.KeyPair,
		subscriptions:   make(map[string]map[string]HandlerFunc),
		pendingRequests: make(map[string]*pendingRequest),
	}
	if config.WebsocketURL != "" {
		cli.websocketURL = config.WebsocketURL
	} else {
		cli.websocketURL = "wss://arkm.com/ws"
	}
	if config.OnWebsocketReaderError != nil {
		cli.onError = config.OnWebsocketReaderError
	} else {
		cli.onError = func(err error) { panic(err) }
	}

	return &cli
}

// Connect establishes WebSocket connection to the server
func (ws *ArkhamWebSocket) Connect(ctx context.Context) error {
	ws.connMutex.Lock()
	defer ws.connMutex.Unlock()

	if ws.end != nil {
		return ErrNotConnected
	}

	// Parse WebSocket URL
	u, err := url.Parse(ws.websocketURL)
	if err != nil {
		return fmt.Errorf("invalid WebSocket URL: %w", err)
	}

	// Create headers with authentication if available
	headers := make(http.Header)
	if err := ws.keyPair.signHeaders("GET", "/ws", nil, headers); err != nil {
		return fmt.Errorf("failed to sign headers: %w", err)
	}

	// Establish WebSocket connection
	dialer := websocket.Dialer{
		HandshakeTimeout: 10 * time.Second,
	}

	conn, resp, err := dialer.Dial(u.String(), headers)
	defer func() {
		if resp != nil && resp.Body != nil {
			resp.Body.Close()
		}
	}()
	if err != nil {
		if resp != nil {
			var buf bytes.Buffer
			if _, err := io.Copy(&buf, resp.Body); err != nil {
				return fmt.Errorf("failed to connect to WebSocket, status: %s, error reading body: %w", resp.Status, err)
			}
			e := new(Error)
			if err := json.Unmarshal(buf.Bytes(), e); err == nil && e.Id != 0 {
				return fmt.Errorf("failed to connect to WebSocket: %w", e)
			}
			return fmt.Errorf("failed to connect to WebSocket, status: %s, body: %s", resp.Status, buf.String())
		}
		return fmt.Errorf("failed to connect to WebSocket: %w", err)
	}

	ws.conn = conn
	ws.end = make(chan struct{})

	// Start message handling goroutines
	go ws.messageReader()

	return nil
}

// Close closes the WebSocket connection
func (ws *ArkhamWebSocket) Close() error {
	ws.connMutex.Lock()
	defer ws.connMutex.Unlock()

	if ws.end == nil {
		return nil
	}

	close(ws.end)

	var err error
	if ws.conn != nil {
		err = ws.conn.Close()
		ws.conn = nil
	}

	ws.end = nil
	return err
}

// Wait blocks until the WebSocket connection is closed
func (ws *ArkhamWebSocket) Wait(ctx context.Context) error {
	ws.connMutex.RLock()
	defer ws.connMutex.RUnlock()
	if ws.end == nil {
		return ErrNotConnected
	}
	select {
	case <-ws.end:
		return ws.err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// subscribe subscribes to a WebSocket channel
func (ws *ArkhamWebSocket) subscribe(channel string, params SubscriptionParams, handler HandlerFunc) (UnsubscribeFunc, error) {
	subscriptionKey, err := ws.getSubscriptionKey(channel, params)
	if err != nil {
		return nil, fmt.Errorf("failed to get subscription key: %w", err)
	}
	handlerId := uuid.New().String()

	ws.subMutex.Lock()
	var request *pendingRequest
	if ws.subscriptions[subscriptionKey] == nil {
		// Create subscription map if it doesn't exist
		ws.subscriptions[subscriptionKey] = make(map[string]HandlerFunc)
		ws.subscriptions[subscriptionKey][handlerId] = handler
		request = ws.makeRequest(subscriptionKey)
		if err := ws.sendSubscribe(subscriptionKey, channel, params); err != nil {
			ws.deleteRequest(subscriptionKey)
			delete(ws.subscriptions, subscriptionKey)
			ws.subMutex.Unlock()
			return nil, fmt.Errorf("failed to send subscribe message: %w", err)
		}
		ws.subMutex.Unlock()
	} else {
		ws.subscriptions[subscriptionKey][handlerId] = handler
		request = ws.getRequest(subscriptionKey)
		ws.subMutex.Unlock()
	}
	// wait for subscription confirmation
	_, err = ws.waitForResponse(context.Background(), request)
	if err != nil {
		return nil, fmt.Errorf("waiting for existing subscribe confirmation: %w", err)
	}
	// Return unsubscribe function
	return func() error {
		return ws.unsubscribe(subscriptionKey, params, handlerId)
	}, nil
}

func (ws *ArkhamWebSocket) unsubscribe(channel string, params SubscriptionParams, handlerId string) error {
	ws.subMutex.Lock()
	defer ws.subMutex.Unlock()

	subscriptionKey, err := ws.getSubscriptionKey(channel, params)
	if err != nil {
		return fmt.Errorf("failed to get subscription key: %w", err)
	}

	if handlers, exists := ws.subscriptions[subscriptionKey]; exists {
		delete(handlers, handlerId)

		// If no more handlers, unsubscribe from channel
		if len(handlers) == 0 {
			// to be here we must have confirmed subscription before
			request := ws.makeRequest(subscriptionKey)
			if err := ws.sendUnsubscribe(subscriptionKey, channel, params); err != nil {
				return fmt.Errorf("failed to send unsubscribe message: %w", err)
			}
			delete(ws.subscriptions, subscriptionKey)
			defer ws.deleteRequest(subscriptionKey)
			_, err = ws.waitForResponse(context.Background(), request)
			if err != nil {
				return fmt.Errorf("failed to wait for unsubscribe confirmation: %w", err)
			}
		}
	}
	return nil
}

// Execute executes a command and waits for response
func (ws *ArkhamWebSocket) execute(ctx context.Context, channel string, params any) ([]byte, error) {
	confirmationId := uuid.New().String()
	request := ws.makeRequest(confirmationId)
	defer ws.deleteRequest(confirmationId)

	// Send execute command
	err := ws.sendExecute(confirmationId, channel, params)
	if err != nil {
		return nil, err
	}

	return ws.waitForResponse(ctx, request)
}

func (ws *ArkhamWebSocket) getRequest(confirmationId string) *pendingRequest {
	ws.requestMutex.RLock()
	defer ws.requestMutex.RUnlock()
	return ws.pendingRequests[confirmationId]
}

func (ws *ArkhamWebSocket) makeRequest(confirmationId string) *pendingRequest {
	request := &pendingRequest{
		event: make(chan struct{}),
	}
	ws.requestMutex.Lock()
	ws.pendingRequests[confirmationId] = request
	ws.requestMutex.Unlock()
	return request
}

func (ws *ArkhamWebSocket) deleteRequest(confirmationId string) {
	ws.requestMutex.Lock()
	delete(ws.pendingRequests, confirmationId)
	ws.requestMutex.Unlock()
}

func (ws *ArkhamWebSocket) waitForResponse(ctx context.Context, request *pendingRequest) ([]byte, error) {
	for {
		select {
		case <-ws.end:
			return nil, ErrNotConnected
		case <-request.event:
			if request.error != nil {
				return nil, request.error
			}
			return request.response, nil
		case <-ctx.Done():
			return nil, ctx.Err()
		}
	}
}

func (ws *ArkhamWebSocket) IsConnected() bool {
	ws.connMutex.RLock()
	defer ws.connMutex.RUnlock()
	if ws.end == nil {
		return false
	}
	select {
	case <-ws.end:
		return false
	default:
		return true
	}
}

// messageReader handles incoming WebSocket messages
func (ws *ArkhamWebSocket) messageReader() {
	for {
		if _, message, err := ws.conn.ReadMessage(); err != nil {
			if !websocket.IsCloseError(err, websocket.CloseGoingAway, websocket.CloseNormalClosure) && ws.IsConnected() {
				ws.err = fmt.Errorf("WebSocket read error: %w", err)
			}
			ws.Close()
			return
		} else if err := ws.handleMessage(message); err != nil {
			ws.onError(fmt.Errorf("WebSocket message handling error: %w", err))
		}
	}
}

type websocketResponse struct {
	Id             ErrorId           `json:"id"`
	Message        string            `json:"message"`
	Name           ErrorName         `json:"name"`
	Channel        string            `json:"channel"`
	ConfirmationId string            `json:"confirmationId"`
	Type           WebsocketDataType `json:"type"`
	Data           json.RawMessage   `json:"data"`
}

// handleMessage processes incoming WebSocket messages
func (ws *ArkhamWebSocket) handleMessage(message []byte) error {
	var response websocketResponse
	if err := json.Unmarshal(message, &response); err != nil {
		return fmt.Errorf("error unmarshaling message: %w", err)
	}
	if response.Channel == "errors" {
		err := &Error{
			Id:      response.Id,
			Message: response.Message,
			Name:    response.Name,
		}
		if response.ConfirmationId != "" {
			return ws.handleResponse(response.ConfirmationId, nil, err)
		}
		return err
	} else if response.Type != "" {
		return ws.handleSubscriptionData(response.ConfirmationId, response.Type, response.Data)
	} else {
		return ws.handleResponse(response.ConfirmationId, response.Data, nil)
	}
}

// handleResponse processes command responses
func (ws *ArkhamWebSocket) handleResponse(confirmationID string, data []byte, err error) error {
	ws.requestMutex.Lock()
	defer ws.requestMutex.Unlock()

	if request, exists := ws.pendingRequests[confirmationID]; exists {
		request.response = data
		request.error = err
		close(request.event)
		return nil
	} else {
		return fmt.Errorf("no pending request for confirmation ID: %s", confirmationID)
	}
}

// handleSubscriptionData processes subscription data
func (ws *ArkhamWebSocket) handleSubscriptionData(key string, t WebsocketDataType, data []byte) error {
	ws.subMutex.RLock()
	defer ws.subMutex.RUnlock()

	if handlers, ok := ws.subscriptions[key]; ok {
		errs := make([]error, 0)
		for _, handler := range handlers {
			errs = append(errs, handler(t, data))
		}
		return errors.Join(errs...)
	}
	return nil
}

// getSubscriptionKey generates a unique key for subscription management
func (ws *ArkhamWebSocket) getSubscriptionKey(channel string, params SubscriptionParams) (string, error) {
	// Add relevant params to key for proper subscription management
	switch params := params.(type) {
	case BalanceSubscriptionParams:
		return fmt.Sprintf("%s:%d", channel, params.SubaccountId), nil
	case OrderStatusSubscriptionParams:
		return fmt.Sprintf("%s:%d", channel, params.SubaccountId), nil
	case PositionSubscriptionParams:
		return fmt.Sprintf("%s:%d", channel, params.SubaccountId), nil
	case MarginSubscriptionParams:
		return fmt.Sprintf("%s:%d", channel, params.SubaccountId), nil
	case TriggerOrderSubscriptionParams:
		return fmt.Sprintf("%s:%d", channel, params.SubaccountId), nil
	case CandleSubscriptionParams:
		return fmt.Sprintf("%s:%s:%s", channel, params.Symbol, cmp.Or(params.Duration, "1m")), nil
	case L2OrderBookSubscriptionParams:
		return fmt.Sprintf("%s:%s:%s", channel, params.Symbol, params.Group), nil
	case L1OrderBookSubscriptionParams:
		return fmt.Sprintf("%s:%s", channel, params.Symbol), nil
	case TickerSubscriptionParams:
		return fmt.Sprintf("%s:%s", channel, params.Symbol), nil
	case TradeSubscriptionParams:
		return fmt.Sprintf("%s:%s", channel, params.Symbol), nil
	default:
		return "", fmt.Errorf("unsupported subscription params type: %T", params)
	}
}

// sendSubscribe sends a subscribe message waiting for confirmation
func (ws *ArkhamWebSocket) sendSubscribe(confirmationId string, channel string, params any) error {
	message := map[string]any{
		"method":         "subscribe",
		"confirmationId": confirmationId,
		"args": map[string]any{
			"channel": channel,
			"params":  params,
		},
	}
	return ws.sendMessage(message)
}

// sendUnsubscribe sends an unsubscribe message
func (ws *ArkhamWebSocket) sendUnsubscribe(confirmationId string, channel string, params any) error {
	message := map[string]any{
		"method":         "unsubscribe",
		"confirmationId": confirmationId,
		"args": map[string]any{
			"channel": channel,
			"params":  params,
		},
	}
	return ws.sendMessage(message)
}

// sendExecute sends an execute message
func (ws *ArkhamWebSocket) sendExecute(confirmationId string, channel string, params any) error {
	message := map[string]any{
		"method":         "execute",
		"confirmationId": confirmationId,
		"args": map[string]any{
			"channel": channel,
			"params":  params,
		},
	}
	return ws.sendMessage(message)
}

// sendMessage sends a message over WebSocket
func (ws *ArkhamWebSocket) sendMessage(message any) error {
	ws.connMutex.RLock()
	defer ws.connMutex.RUnlock()

	err := ws.conn.WriteJSON(message)
	if err != nil {
		return err
	}

	return nil
}

// Ping sends a ping message and waits for pong response
func (ws *ArkhamWebSocket) Ping() error {
	confirmationId := uuid.New().String()
	request := ws.makeRequest(confirmationId)
	defer ws.deleteRequest(confirmationId)
	message := map[string]any{
		"confirmationId": confirmationId,
		"method":         "ping",
	}
	if err := ws.sendMessage(message); err != nil {
		return err
	}
	_, err := ws.waitForResponse(context.Background(), request)
	return err
}
