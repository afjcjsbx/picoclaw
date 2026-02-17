package tools

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

// StockTool allows you to retrieve the current price of a stock.
type StockTool struct {
	baseURL string
}

func NewStockTool() *StockTool {
	return &StockTool{
		baseURL: "https://query1.finance.yahoo.com", // Default URL
	}
}

func (t *StockTool) Name() string {
	return "get_stock_price"
}

func (t *StockTool) Description() string {
	return "Get the current stock price and currency from Yahoo Finance using the ticker symbol (e.g., AAPL, TSLA, MSFT)."
}

func (t *StockTool) Parameters() map[string]interface{} {
	return map[string]interface{}{
		"type": "object",
		"properties": map[string]interface{}{
			"ticker": map[string]interface{}{
				"type":        "string",
				"description": "The stock symbol (ticker) of the company, e.g., AAPL for Apple.",
			},
		},
		"required": []string{"ticker"},
	}
}

func (t *StockTool) Execute(ctx context.Context, args map[string]interface{}) *ToolResult {
	ticker, ok := args["ticker"].(string)
	if !ok || ticker == "" {
		return ErrorResult("The parameter 'ticker' is mandatory")
	}

	url := fmt.Sprintf("%s/v8/finance/chart/%s?interval=1d&range=1d", t.baseURL, ticker)

	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return ErrorResult(fmt.Sprintf("Error creating HTTP request: %v", err))
	}

	// Yahoo Finance blocks requests without User-Agent
	req.Header.Set("User-Agent", "Mozilla/5.0 (Windows NT 10.0; Win64; x64) AppleWebKit/537.36")

	client := &http.Client{Timeout: 10 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return ErrorResult(fmt.Sprintf("Error connecting to Yahoo Finance: %v", err))
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return ErrorResult(fmt.Sprintf("Yahoo Finance returned an error status: %d", resp.StatusCode))
	}

	var result struct {
		Chart struct {
			Result []struct {
				Meta struct {
					Currency           string  `json:"currency"`
					Symbol             string  `json:"symbol"`
					RegularMarketPrice float64 `json:"regularMarketPrice"`
				} `json:"meta"`
			} `json:"result"`
			Error *struct {
				Description string `json:"description"`
			} `json:"error"`
		} `json:"chart"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return ErrorResult(fmt.Sprintf("Error in reading JSON data: %v", err))
	}

	// API-side error checking
	if result.Chart.Error != nil {
		return ErrorResult(fmt.Sprintf("Yahoo API Error: %s", result.Chart.Error.Description))
	}
	if len(result.Chart.Result) == 0 {
		return ErrorResult(fmt.Sprintf("No data found for ticker: %s", ticker))
	}

	// Extract data
	meta := result.Chart.Result[0].Meta

	output := fmt.Sprintf("Yahoo Finance data for %s: Current price %.2f %s.", meta.Symbol, meta.RegularMarketPrice, meta.Currency)

	return UserResult(output)
}
