// Example usage of the Arkham Exchange REST API SDK
//
// This example demonstrates:
// - Getting a list of all pairs
// - Fetching order book data for a specific pair
// - Placing and cancelling orders
// - Account management
//
// To use this example:
// 1. Set ARKHAM_API_KEY and ARKHAM_API_SECRET with your actual credentials
// 2. Build and run: go run examples/rest/rest.go
// nolint
package main

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/arkhamintelligence/arkham-sdk-go"
)

type pretty struct{ any }

func (s pretty) String() string {
	b, _ := json.MarshalIndent(s.any, "", "  ")
	return string(b)
}

func main() {
	config := arkham.Config{}

	if err := config.LoadFromEnv(); err != nil {
		panic(fmt.Sprintf("❌ Failed to load config from environment: %v", err))
	}

	client, err := arkham.NewClient(config)
	if err != nil {
		panic(fmt.Sprintf("❌ Failed to create Arkham client: %v", err))
	}
	fmt.Println("🚀 Initialized Arkham Exchange REST API client...")

	ctx := context.Background()

	// Get a list of all trading pairs
	fmt.Println("Getting all trading pairs...")
	pairs, err := client.GetPairs(ctx)
	if err != nil {
		panic(fmt.Sprintf("❌ Failed to fetch trading pairs: %v", err))
	}
	fmt.Println("Available Trading Pairs:", pretty{pairs})

	// Fetch order book data for a specific pair (e.g., BTC_USDT)
	fmt.Println("Fetching order book for BTC_USDT...")
	orderBook, err := client.GetBook(ctx, arkham.GetBookParams{
		Symbol: "BTC_USDT",
	})
	if err != nil {
		panic(fmt.Sprintf("❌ Failed to fetch order book: %v", err))
	}
	fmt.Println("✅ Order Book for BTC_USDT:", pretty{orderBook})

	// Place a new order
	fmt.Println("Placing a new limit buy order for BTC_USDT...")
	newOrder, err := client.CreateOrder(ctx, arkham.CreateOrderRequest{
		Symbol: "BTC_USDT",
		Side:   arkham.OrderSideBuy,
		Type:   arkham.OrderTypeLimitGtc,
		Price:  "30000.00",
		Size:   "0.001",
	})
	if err != nil {
		panic(fmt.Sprintf("❌ Failed to create order: %v", err))
	}
	fmt.Println("✅ New Order Created:", pretty{newOrder})

	// Cancel the order
	fmt.Println("Cancelling the order...")
	result, err := client.CancelOrder(ctx, arkham.CancelOrderRequest{
		OrderId: newOrder.OrderId,
	})
	if err != nil {
		panic(fmt.Sprintf("❌ Failed to cancel order: %v", err))
	}
	fmt.Println("✅ Order Canceled:", pretty{result})

}
