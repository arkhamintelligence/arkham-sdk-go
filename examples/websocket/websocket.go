// nolint
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	arkham "github.com/arkhamintelligence/arkham-sdk-go"
)

type pretty struct{ any }

func (s pretty) String() string {
	b, _ := json.MarshalIndent(s.any, "", "  ")
	return string(b)
}

func main() {
	config := arkham.Config{}

	if err := config.LoadFromEnv(); err != nil {
		panic(fmt.Sprintf("Failed to load config from environment: %v", err))
	}

	// Create WebSocket client
	ws := arkham.NewArkhamWebSocket(config)

	// Connect to WebSocket
	ctx := context.Background()
	if err := ws.Connect(ctx); err != nil {
		panic(fmt.Sprintf("Failed to connect to WebSocket: %v", err))
	}

	fmt.Println("Connected to Arkham WebSocket API")
	fmt.Println("Listening for WebSocket data... Press Ctrl+C to exit")

	// Subscribe to BTC_USDT trades
	tradeUnsubscribe, err := ws.Trades(arkham.TradeSubscriptionParams{
		Symbol:   "BTC_USDT",
		Snapshot: true,
	}, func(data arkham.Trade) {
		fmt.Println("Trade received:", pretty{data})
	}, func(data []arkham.Trade) {
		fmt.Println("Trade snapshot received:", pretty{data})
	})
	defer tradeUnsubscribe()
	if err != nil {
		fmt.Printf("Failed to subscribe to trades: %v", err)
	}

	// Subscribe to BTC_USDT ticker
	tickerUnsubscribe, err := ws.Ticker(arkham.TickerSubscriptionParams{
		Symbol:   "BTC_USDT",
		Snapshot: true,
	}, func(data arkham.Ticker) {
		fmt.Println("Ticker received:", pretty{data})
	}, func(data arkham.Ticker) {
		fmt.Println("Ticker snapshot received:", pretty{data})
	})
	defer tickerUnsubscribe()
	if err != nil {
		fmt.Printf("Failed to subscribe to ticker: %v", err)
	}

	// Subscribe to 1-minute candles
	candleUnsubscribe, err := ws.Candles(arkham.CandleSubscriptionParams{
		Symbol: "BTC_USDT",
	}, func(data arkham.Candle) {
		fmt.Println("Candle received:", pretty{data})
	})
	defer candleUnsubscribe()
	if err != nil {
		fmt.Printf("Failed to subscribe to candles: %v", err)
	}

	// Example of executing a command (would require authentication)
	newOrder, err := ws.CreateOrder(ctx, arkham.CreateOrderRequest{
		Symbol: "BTC_USDT",
		Side:   arkham.OrderSideBuy,
		Type:   arkham.OrderTypeLimitGtc,
		Price:  "30000.00",
		Size:   "0.01",
	})

	if err != nil {
		fmt.Println("❌ Failed to execute command:", err)
	} else {
		fmt.Println("✅ Order created successfully:", pretty{newOrder})

		// Example of canceling an order (would require authentication)
		_, err = ws.CancelOrder(ctx, arkham.CancelOrderRequest{
			OrderId: newOrder.OrderId,
		})

		if err != nil {
			fmt.Println("❌ Failed to cancel order:", err)
		} else {
			fmt.Println("✅ Order canceled successfully")
		}
	}

	// Setup graceful shutdown
	c := make(chan os.Signal, 1)
	signal.Notify(c, os.Interrupt, syscall.SIGTERM)

	// Wait for interrupt signal or WebSocket error
	<-c

	fmt.Println("Example completed")
	ws.Close()
}
