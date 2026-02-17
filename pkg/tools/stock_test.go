package tools

import (
	"context"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestStockTool_NameAndDescription(t *testing.T) {
	tool := NewStockTool()

	if tool.Name() != "get_stock_price" {
		t.Errorf("Expected name 'get_stock_price', got '%s'", tool.Name())
	}

	if tool.Description() == "" {
		t.Error("Expected description to not be empty")
	}
}

func TestStockTool_Parameters(t *testing.T) {
	tool := NewStockTool()
	params := tool.Parameters()

	if params["type"] != "object" {
		t.Errorf("Expected type 'object', got '%v'", params["type"])
	}

	req, ok := params["required"].([]string)
	if !ok || len(req) == 0 || req[0] != "ticker" {
		t.Error("Expected 'ticker' to be in required parameters")
	}
}

func TestStockTool_MissingTicker(t *testing.T) {
	tool := NewStockTool()
	ctx := context.Background()

	// We pass empty args
	args := map[string]interface{}{}
	result := tool.Execute(ctx, args)

	if !result.IsError {
		t.Error("Expected error when 'ticker' is missing")
	}

	if !strings.Contains(result.ForLLM, "mandatory") {
		t.Errorf("Expected error message about missing ticker, got: %s", result.ForLLM)
	}
}

func TestStockTool_Success(t *testing.T) {
	mockResponse := `{
		"chart": {
			"result": [
				{
					"meta": {
						"currency": "USD",
						"symbol": "AAPL",
						"regularMarketPrice": 150.50
					}
				}
			],
			"error": null
		}
	}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.URL.Path, "AAPL") {
			t.Errorf("Expected request to contain 'AAPL', got %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(mockResponse))
	}))
	defer server.Close()

	tool := &StockTool{baseURL: server.URL}
	ctx := context.Background()
	args := map[string]interface{}{
		"ticker": "AAPL",
	}

	result := tool.Execute(ctx, args)

	if result.IsError {
		t.Errorf("Expected success, got error: %s", result.ForLLM)
	}

	expectedOutput := "Yahoo Finance data for AAPL: Current price 150.50 USD."
	if !strings.Contains(result.ForUser, expectedOutput) {
		t.Errorf("Expected ForUser to contain '%s', got: %s", expectedOutput, result.ForUser)
	}
}

func TestStockTool_HttpError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
	}))
	defer server.Close()

	tool := &StockTool{baseURL: server.URL}
	ctx := context.Background()
	args := map[string]interface{}{
		"ticker": "INVALID",
	}

	result := tool.Execute(ctx, args)

	if !result.IsError {
		t.Error("Expected error for 404 response")
	}

	if !strings.Contains(result.ForLLM, "404") {
		t.Errorf("Expected error to contain '404', got: %s", result.ForLLM)
	}
}

func TestStockTool_ApiError(t *testing.T) {
	mockResponse := `{
		"chart": {
			"result": null,
			"error": {
				"description": "Not Found"
			}
		}
	}`

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(mockResponse))
	}))
	defer server.Close()

	tool := &StockTool{baseURL: server.URL}
	ctx := context.Background()
	args := map[string]interface{}{
		"ticker": "UNKNOWN_TICKER",
	}

	result := tool.Execute(ctx, args)

	if !result.IsError {
		t.Error("Expected error when API returns an error description")
	}

	if !strings.Contains(result.ForLLM, "Not Found") {
		t.Errorf("Expected error message to contain 'Not Found', got: %s", result.ForLLM)
	}
}

func TestStockTool_InvalidJson(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		w.Write([]byte(`{ "broken json": `))
	}))
	defer server.Close()

	tool := &StockTool{baseURL: server.URL}
	ctx := context.Background()
	args := map[string]interface{}{
		"ticker": "AAPL",
	}

	result := tool.Execute(ctx, args)

	if !result.IsError {
		t.Error("Expected error when JSON is invalid")
	}

	if !strings.Contains(result.ForLLM, "Error in reading JSON data") {
		t.Errorf("Expected JSON parse error, got: %s", result.ForLLM)
	}
}
