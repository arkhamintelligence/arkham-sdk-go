# Arkham Exchange Go SDK

This is the official Go SDK for interacting with the Arkham Exchange API. It provides a convenient way to access various endpoints and perform operations related to Arkham Exchange.

## Installation

To install the SDK, use the following command:

```bash
go get github.com/arkhamintelligence/arkham-sdk-go
```

## Basic Usage

```go
package main

import (
	"context"
	"fmt"

	"github.com/arkhamintelligence/arkham-sdk-go"
)

func main() {
	config := arkham.Config{}

	if err := config.LoadFromEnv(); err != nil {
        fmt.Println("Failed to load config from environment:", err)
        return
	}

    client, err := arkham.NewClient(config)

    if err != nil {
        fmt.Println("Failed to create Arkham client:", err)
        return
	}

    pairs, err := client.GetPairs(context.Background())
    if err != nil {
        fmt.Println("Failed to fetch market data:", err)
        return
    }

    prettyPairs, err := json.MarshalIndent(pairs, "", "  ")
    if err != nil {
        fmt.Println("Failed to marshal market data:", err)
        return
    }
    fmt.Println("Pairs:", string(prettyPairs))
}
