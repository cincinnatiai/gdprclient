package gdprclient

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log"
	"math"
	"math/rand"
	"net/http"
	"time"
)

// Constants for GDPR request types and statuses
const (
	TypeInfoRequest   = "INFO_REQUEST"
	TypeDeleteRequest = "DELETE_REQUEST"

	StatusPending  = "PENDING"
	StatusComplete = "COMPLETE"
	StatusFailed   = "FAILED"
	StatusDeleted  = "DELETED"
)

// RetryPolicy defines the retry behavior for failed requests
type RetryPolicy struct {
	MaxRetries     int           // Maximum number of retries
	InitialBackoff time.Duration // Initial backoff duration
	MaxBackoff     time.Duration // Maximum backoff duration
	BackoffFactor  float64       // Multiplication factor for backoff duration after each retry
	Jitter         float64       // Jitter factor (0-1) to randomize backoff duration
}

// DefaultRetryPolicy provides reasonable default values for retry
var DefaultRetryPolicy = RetryPolicy{
	MaxRetries:     3,
	InitialBackoff: 100 * time.Millisecond,
	MaxBackoff:     10 * time.Second,
	BackoffFactor:  2.0,
	Jitter:         0.2,
}

// Client represents a GDPR service client
type Client struct {
	baseURL     string
	apiKey      string
	httpClient  *http.Client
	environment string
	retryPolicy RetryPolicy
}

// ClientOption is a function that configures a Client
type ClientOption func(*Client)

// NewClient creates a new GDPR service client
func NewClient(baseURL, apiKey string, options ...ClientOption) *Client {
	client := &Client{
		baseURL: baseURL,
		apiKey:  apiKey,
		httpClient: &http.Client{
			Timeout: 10 * time.Second,
		},
		environment: "Prod", // Default to production
		retryPolicy: DefaultRetryPolicy,
	}

	// Apply options
	for _, option := range options {
		option(client)
	}

	return client
}

// WithTimeout sets the HTTP client timeout
func WithTimeout(timeout time.Duration) ClientOption {
	return func(c *Client) {
		c.httpClient.Timeout = timeout
	}
}

// WithEnvironment sets the environment
func WithEnvironment(env string) ClientOption {
	return func(c *Client) {
		c.environment = env
	}
}

// WithTransport sets a custom transport for the HTTP client
func WithTransport(transport http.RoundTripper) ClientOption {
	return func(c *Client) {
		c.httpClient.Transport = transport
	}
}

// WithRetryPolicy sets a custom retry policy
func WithRetryPolicy(policy RetryPolicy) ClientOption {
	return func(c *Client) {
		c.retryPolicy = policy
	}
}

// WithMaxRetries sets the maximum number of retries
func WithMaxRetries(maxRetries int) ClientOption {
	return func(c *Client) {
		c.retryPolicy.MaxRetries = maxRetries
	}
}

// Response is the generic response structure
type Response struct {
	StatusCode int         `json:"statusCode"`
	Message    string      `json:"message,omitempty"`
	Data       interface{} `json:"data,omitempty"`
}

// InfoRequest represents a data info request
type InfoRequest struct {
	PartitionKey string `json:"partition_key"`
	RangeKey     string `json:"range_key,omitempty"`
	Type         string `json:"type"`
	Status       string `json:"status,omitempty"`
	Created      string `json:"created,omitempty"`
	Modified     string `json:"modified,omitempty"`
	CreatedBy    string `json:"created_by"`
}

// DeleteRequest represents a data deletion request
type DeleteRequest struct {
	PartitionKey string `json:"partition_key"`
	RangeKey     string `json:"range_key,omitempty"`
	Type         string `json:"type"`
	Status       string `json:"status,omitempty"`
	Created      string `json:"created,omitempty"`
	Modified     string `json:"modified,omitempty"`
	CreatedBy    string `json:"created_by"`
}

// CreateInfoRequestInput is the input for creating an info request
type CreateInfoRequestInput struct {
	PartitionKey string `json:"partition_key"`
	Type         string `json:"type"`
	CreatedBy    string `json:"created_by"`
	ApiKey       string `json:"api_key,omitempty"`
}

// CreateDeleteRequestInput is the input for creating a deletion request
type CreateDeleteRequestInput struct {
	PartitionKey string `json:"partition_key"`
	Type         string `json:"type"`
	CreatedBy    string `json:"created_by"`
	ApiKey       string `json:"api_key,omitempty"`
}

// FetchRequestInput is the input for fetching a request
type FetchRequestInput struct {
	PartitionKey string `json:"partition_key"`
	RangeKey     string `json:"range_key"`
	ApiKey       string `json:"api_key,omitempty"`
}

// UpdateRequestInput is the input for updating a request
type UpdateRequestInput struct {
	PartitionKey string `json:"partition_key"`
	RangeKey     string `json:"range_key"`
	Type         string `json:"type,omitempty"`
	Status       string `json:"status,omitempty"`
	ApiKey       string `json:"api_key,omitempty"`
}

// TODO March 24, 2025 Correct the camelcase and make them underscore

// ShouldRetry determines if a request should be retried based on the status code and error
func ShouldRetry(statusCode int, err error) bool {
	// Retry on network errors
	if err != nil {
		// Check for timeout, connection refused, or other temporary network errors
		if errors.Is(err, context.DeadlineExceeded) ||
			errors.Is(err, context.Canceled) ||
			err.Error() == "connection refused" ||
			err.Error() == "no such host" {
			return true
		}
	}

	// Retry on 5xx server errors, except 501 Not Implemented
	if statusCode >= 500 && statusCode != 501 {
		return true
	}

	// Retry on 429 Too Many Requests
	if statusCode == 429 {
		return true
	}

	return false
}

// calculateBackoff determines the backoff duration for a retry attempt
func (c *Client) calculateBackoff(attempt int) time.Duration {
	// Calculate base backoff with exponential increase
	backoff := float64(c.retryPolicy.InitialBackoff) * math.Pow(c.retryPolicy.BackoffFactor, float64(attempt))

	// Apply jitter
	if c.retryPolicy.Jitter > 0 {
		jitter := rand.Float64() * c.retryPolicy.Jitter
		backoff = backoff * (1 + jitter)
	}

	// Cap at max backoff
	if backoff > float64(c.retryPolicy.MaxBackoff) {
		backoff = float64(c.retryPolicy.MaxBackoff)
	}

	return time.Duration(backoff)
}

// doRequestWithRetry performs an HTTP request with retries according to the retry policy
func (c *Client) doRequestWithRetry(req *http.Request) (*http.Response, error) {
	var resp *http.Response
	var err error

	for attempt := 0; attempt <= c.retryPolicy.MaxRetries; attempt++ {
		// Clone the request to make it reusable for retries
		reqClone := req.Clone(req.Context())

		// If this is a retry, add a header indicating the retry attempt
		if attempt > 0 {
			reqClone.Header.Set("X-Retry-Attempt", fmt.Sprintf("%d", attempt))
		}

		resp, err = c.httpClient.Do(reqClone)

		// If no error and successful status code, return the response
		if err == nil && (resp.StatusCode < 500 && resp.StatusCode != 429) {
			return resp, nil
		}

		// Check if we should retry
		statusCode := 0
		if resp != nil {
			statusCode = resp.StatusCode
			// Make sure to close the response body before retrying
			resp.Body.Close()
		}

		if !ShouldRetry(statusCode, err) || attempt >= c.retryPolicy.MaxRetries {
			break
		}

		// Calculate backoff duration and wait
		backoff := c.calculateBackoff(attempt)
		time.Sleep(backoff)
	}

	// Return the last response or error
	return resp, err
}

// FetchAllRequestInput is the input for fetching all requests
type FetchAllRequestInput struct {
	PartitionKey string `json:"partitionKey"`
	LastRangeKey string `json:"lastRangeKey,omitempty"`
	ApiKey       string `json:"apiKey,omitempty"`
}

// FetchByTypeInput is the input for fetching requests by type
type FetchByTypeInput struct {
	Type         string `json:"type"`
	LastRangeKey string `json:"lastRangeKey,omitempty"`
	ApiKey       string `json:"apiKey,omitempty"`
}

// FetchByStatusInput is the input for fetching requests by status
type FetchByStatusInput struct {
	Status       string `json:"status"`
	LastRangeKey string `json:"lastRangeKey,omitempty"`
	ApiKey       string `json:"apiKey,omitempty"`
}

// FetchByCreatorInput is the input for fetching requests by creator
type FetchByCreatorInput struct {
	CreatedBy    string `json:"createdBy"`
	LastRangeKey string `json:"lastRangeKey,omitempty"`
	ApiKey       string `json:"apiKey,omitempty"`
}

// DeleteRequestInput is the input for deleting a request
type DeleteRequestInput struct {
	PartitionKey string `json:"partitionKey"`
	RangeKey     string `json:"rangeKey"`
	IsHardDelete bool   `json:"isHardDelete"`
	ApiKey       string `json:"apiKey,omitempty"`
}

// PaginatedResponse is a response containing paginated results
type PaginatedResponse struct {
	Results      []interface{} `json:"results"`
	LastRangeKey string        `json:"lastRangeKey,omitempty"`
}

// CreateInfoRequest creates a new info request
func (c *Client) CreateInfoRequest(input CreateInfoRequestInput) (*InfoRequest, error) {
	// Use client's API key if not provided in input
	if input.ApiKey == "" {
		input.ApiKey = c.apiKey
	}

	body, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %v", err)
	}

	req, err := http.NewRequest("POST", fmt.Sprintf("%s/gdpr?action=create", c.baseURL), bytes.NewBuffer(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.doRequestWithRetry(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %v", err)
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(responseBody))
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("GDPR service returned error: %s, StatusCode: %v", resp.Body, resp.StatusCode)
	}

	log.Printf("GDPRClientLibrary.CreateInfo - Response Body: %s", string(responseBody))
	var infoRequest InfoRequest
	if jsonErr := json.Unmarshal(responseBody, &infoRequest); err != nil {
		return nil, fmt.Errorf("failed to unmarshal data: %v", jsonErr)
	}
	return &infoRequest, nil
}

// CreateDeleteRequest creates a new deletion request
func (c *Client) CreateDeleteRequest(input CreateDeleteRequestInput) (*DeleteRequest, error) {
	// Use client's API key if not provided in input
	if input.ApiKey == "" {
		input.ApiKey = c.apiKey
	}

	body, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %v", err)
	}

	req, err := http.NewRequest("POST", fmt.Sprintf("%s/gdpr?controller=delete&action=create", c.baseURL), bytes.NewBuffer(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.doRequestWithRetry(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %v", err)
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(responseBody))
	}

	var response Response
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %v", err)
	}

	if response.StatusCode != 200 {
		return nil, fmt.Errorf("GDPR service returned error: %s", response.Message)
	}

	// Convert response.Data to DeleteRequest
	dataJSON, err := json.Marshal(response.Data)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal data: %v", err)
	}

	var deleteRequest DeleteRequest
	if err := json.Unmarshal(dataJSON, &deleteRequest); err != nil {
		return nil, fmt.Errorf("failed to unmarshal data: %v", err)
	}

	return &deleteRequest, nil
}

// FetchInfoRequest fetches an info request by ID
func (c *Client) FetchInfoRequest(input FetchRequestInput) (*InfoRequest, error) {
	// Use client's API key if not provided in input
	if input.ApiKey == "" {
		input.ApiKey = c.apiKey
	}

	body, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %v", err)
	}

	req, err := http.NewRequest("POST", fmt.Sprintf("%s/gdpr?action=fetch", c.baseURL), bytes.NewBuffer(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.doRequestWithRetry(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %v", err)
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(responseBody))
	}

	var response Response
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %v", err)
	}

	if response.StatusCode == 404 {
		return nil, errors.New("info request not found")
	}

	if response.StatusCode != 200 {
		return nil, fmt.Errorf("GDPR service returned error: %s", response.Message)
	}

	// Convert response.Data to InfoRequest
	dataJSON, err := json.Marshal(response.Data)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal data: %v", err)
	}

	var infoRequest InfoRequest
	if err := json.Unmarshal(dataJSON, &infoRequest); err != nil {
		return nil, fmt.Errorf("failed to unmarshal data: %v", err)
	}

	return &infoRequest, nil
}

// FetchDeleteRequest fetches a delete request by ID
func (c *Client) FetchDeleteRequest(input FetchRequestInput) (*DeleteRequest, error) {
	// Use client's API key if not provided in input
	if input.ApiKey == "" {
		input.ApiKey = c.apiKey
	}

	body, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %v", err)
	}

	req, err := http.NewRequest("POST", fmt.Sprintf("%s/gdpr?controller=delete&action=fetch", c.baseURL), bytes.NewBuffer(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %v", err)
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(responseBody))
	}

	var response Response
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %v", err)
	}

	if response.StatusCode == 404 {
		return nil, errors.New("delete request not found")
	}

	if response.StatusCode != 200 {
		return nil, fmt.Errorf("GDPR service returned error: %s", response.Message)
	}

	// Convert response.Data to DeleteRequest
	dataJSON, err := json.Marshal(response.Data)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal data: %v", err)
	}

	var deleteRequest DeleteRequest
	if err := json.Unmarshal(dataJSON, &deleteRequest); err != nil {
		return nil, fmt.Errorf("failed to unmarshal data: %v", err)
	}

	return &deleteRequest, nil
}

// UpdateInfoRequest updates an info request
func (c *Client) UpdateInfoRequest(input UpdateRequestInput) (bool, error) {
	// Use client's API key if not provided in input
	if input.ApiKey == "" {
		input.ApiKey = c.apiKey
	}

	body, err := json.Marshal(input)
	if err != nil {
		return false, fmt.Errorf("failed to marshal request body: %v", err)
	}

	req, err := http.NewRequest("POST", fmt.Sprintf("%s/gdpr?action=update", c.baseURL), bytes.NewBuffer(body))
	if err != nil {
		return false, fmt.Errorf("failed to create request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.doRequestWithRetry(req)
	if err != nil {
		return false, fmt.Errorf("failed to send request: %v", err)
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, fmt.Errorf("failed to read response body: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(responseBody))
	}

	var response Response
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return false, fmt.Errorf("failed to unmarshal response: %v", err)
	}

	if response.StatusCode != 200 {
		return false, fmt.Errorf("GDPR service returned error: %s", response.Message)
	}

	return true, nil
}

// UpdateDeleteRequest updates a delete request
func (c *Client) UpdateDeleteRequest(input UpdateRequestInput) (bool, error) {
	// Use client's API key if not provided in input
	if input.ApiKey == "" {
		input.ApiKey = c.apiKey
	}

	body, err := json.Marshal(input)
	if err != nil {
		return false, fmt.Errorf("failed to marshal request body: %v", err)
	}

	req, err := http.NewRequest("POST", fmt.Sprintf("%s/gdpr?controller=delete&action=update", c.baseURL), bytes.NewBuffer(body))
	if err != nil {
		return false, fmt.Errorf("failed to create request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return false, fmt.Errorf("failed to send request: %v", err)
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, fmt.Errorf("failed to read response body: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(responseBody))
	}

	var response Response
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return false, fmt.Errorf("failed to unmarshal response: %v", err)
	}

	if response.StatusCode != 200 {
		return false, fmt.Errorf("GDPR service returned error: %s", response.Message)
	}

	return true, nil
}

// DeleteRequest deletes a request (info or delete)
func (c *Client) DeleteInfoRequest(input DeleteRequestInput) (bool, error) {
	// Use client's API key if not provided in input
	if input.ApiKey == "" {
		input.ApiKey = c.apiKey
	}

	body, err := json.Marshal(input)
	if err != nil {
		return false, fmt.Errorf("failed to marshal request body: %v", err)
	}

	req, err := http.NewRequest("POST", fmt.Sprintf("%s/gdpr?action=delete", c.baseURL), bytes.NewBuffer(body))
	if err != nil {
		return false, fmt.Errorf("failed to create request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.doRequestWithRetry(req)
	if err != nil {
		return false, fmt.Errorf("failed to send request: %v", err)
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, fmt.Errorf("failed to read response body: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(responseBody))
	}

	var response Response
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return false, fmt.Errorf("failed to unmarshal response: %v", err)
	}

	if response.StatusCode != 200 {
		return false, fmt.Errorf("GDPR service returned error: %s", response.Message)
	}

	return true, nil
}

// DeleteRequest deletes a request (info or delete)
func (c *Client) DeleteRequest(input DeleteRequestInput) (bool, error) {
	// Use client's API key if not provided in input
	if input.ApiKey == "" {
		input.ApiKey = c.apiKey
	}

	body, err := json.Marshal(input)
	if err != nil {
		return false, fmt.Errorf("failed to marshal request body: %v", err)
	}

	req, err := http.NewRequest("POST", fmt.Sprintf("%s/gdpr?controller=delete&action=delete", c.baseURL), bytes.NewBuffer(body))
	if err != nil {
		return false, fmt.Errorf("failed to create request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.doRequestWithRetry(req)
	if err != nil {
		return false, fmt.Errorf("failed to send request: %v", err)
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return false, fmt.Errorf("failed to read response body: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(responseBody))
	}

	var response Response
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return false, fmt.Errorf("failed to unmarshal response: %v", err)
	}

	if response.StatusCode != 200 {
		return false, fmt.Errorf("GDPR service returned error: %s", response.Message)
	}

	return true, nil
}

// FetchAllInfoRequests fetches all info requests for a partition key
func (c *Client) FetchAllInfoRequests(input FetchAllRequestInput) (*PaginatedResponse, error) {
	// Use client's API key if not provided in input
	if input.ApiKey == "" {
		input.ApiKey = c.apiKey
	}

	body, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %v", err)
	}

	req, err := http.NewRequest("POST", fmt.Sprintf("%s/gdpr?action=fetchAll", c.baseURL), bytes.NewBuffer(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.doRequestWithRetry(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %v", err)
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(responseBody))
	}

	var response Response
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %v", err)
	}

	if response.StatusCode != 200 {
		return nil, fmt.Errorf("GDPR service returned error: %s", response.Message)
	}

	// Convert response.Data to PaginatedResponse
	dataJSON, err := json.Marshal(response.Data)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal data: %v", err)
	}

	var paginatedResponse PaginatedResponse
	if err := json.Unmarshal(dataJSON, &paginatedResponse); err != nil {
		return nil, fmt.Errorf("failed to unmarshal data: %v", err)
	}

	return &paginatedResponse, nil
}

// FetchInfoRequestsByType fetches info requests by type
func (c *Client) FetchInfoRequestsByType(input FetchByTypeInput) (*PaginatedResponse, error) {
	// Use client's API key if not provided in input
	if input.ApiKey == "" {
		input.ApiKey = c.apiKey
	}

	body, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %v", err)
	}

	req, err := http.NewRequest("POST", fmt.Sprintf("%s/gdpr?action=fetchByType", c.baseURL), bytes.NewBuffer(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.doRequestWithRetry(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %v", err)
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(responseBody))
	}

	var response Response
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %v", err)
	}

	if response.StatusCode != 200 {
		return nil, fmt.Errorf("GDPR service returned error: %s", response.Message)
	}

	// Convert response.Data to PaginatedResponse
	dataJSON, err := json.Marshal(response.Data)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal data: %v", err)
	}

	var paginatedResponse PaginatedResponse
	if err := json.Unmarshal(dataJSON, &paginatedResponse); err != nil {
		return nil, fmt.Errorf("failed to unmarshal data: %v", err)
	}

	return &paginatedResponse, nil
}

// FetchDeleteRequestsByStatus fetches delete requests by status
func (c *Client) FetchDeleteRequestsByStatus(input FetchByStatusInput) (*PaginatedResponse, error) {
	// Use client's API key if not provided in input
	if input.ApiKey == "" {
		input.ApiKey = c.apiKey
	}

	body, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %v", err)
	}

	req, err := http.NewRequest("POST", fmt.Sprintf("%s/gdpr?controller=delete&action=fetchByStatus", c.baseURL), bytes.NewBuffer(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.doRequestWithRetry(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %v", err)
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(responseBody))
	}

	var response Response
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %v", err)
	}

	if response.StatusCode != 200 {
		return nil, fmt.Errorf("GDPR service returned error: %s", response.Message)
	}

	// Convert response.Data to PaginatedResponse
	dataJSON, err := json.Marshal(response.Data)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal data: %v", err)
	}

	var paginatedResponse PaginatedResponse
	if err := json.Unmarshal(dataJSON, &paginatedResponse); err != nil {
		return nil, fmt.Errorf("failed to unmarshal data: %v", err)
	}

	return &paginatedResponse, nil
}

// FetchRequestsByCreator fetches requests by creator
func (c *Client) FetchRequestsByCreator(input FetchByCreatorInput) (*PaginatedResponse, error) {
	// Use client's API key if not provided in input
	if input.ApiKey == "" {
		input.ApiKey = c.apiKey
	}

	body, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %v", err)
	}

	req, err := http.NewRequest("POST", fmt.Sprintf("%s/gdpr?action=fetchByCreator", c.baseURL), bytes.NewBuffer(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.doRequestWithRetry(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %v", err)
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(responseBody))
	}

	var response Response
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %v", err)
	}

	if response.StatusCode != 200 {
		return nil, fmt.Errorf("GDPR service returned error: %s", response.Message)
	}

	// Convert response.Data to PaginatedResponse
	dataJSON, err := json.Marshal(response.Data)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal data: %v", err)
	}

	var paginatedResponse PaginatedResponse
	if err := json.Unmarshal(dataJSON, &paginatedResponse); err != nil {
		return nil, fmt.Errorf("failed to unmarshal data: %v", err)
	}

	return &paginatedResponse, nil
}

// FetchRequestsByCreator fetches requests by creator
func (c *Client) FetchDeleteRequestsByCreator(input FetchByCreatorInput) (*PaginatedResponse, error) {
	// Use client's API key if not provided in input
	if input.ApiKey == "" {
		input.ApiKey = c.apiKey
	}

	body, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal request body: %v", err)
	}

	req, err := http.NewRequest("POST", fmt.Sprintf("%s/gdpr?controller=delete&action=fetchByCreator", c.baseURL), bytes.NewBuffer(body))
	if err != nil {
		return nil, fmt.Errorf("failed to create request: %v", err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := c.doRequestWithRetry(req)
	if err != nil {
		return nil, fmt.Errorf("failed to send request: %v", err)
	}
	defer resp.Body.Close()

	responseBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("failed to read response body: %v", err)
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("request failed with status %d: %s", resp.StatusCode, string(responseBody))
	}

	var response Response
	if err := json.Unmarshal(responseBody, &response); err != nil {
		return nil, fmt.Errorf("failed to unmarshal response: %v", err)
	}

	if response.StatusCode != 200 {
		return nil, fmt.Errorf("GDPR service returned error: %s", response.Message)
	}

	// Convert response.Data to PaginatedResponse
	dataJSON, err := json.Marshal(response.Data)
	if err != nil {
		return nil, fmt.Errorf("failed to marshal data: %v", err)
	}

	var paginatedResponse PaginatedResponse
	if err := json.Unmarshal(dataJSON, &paginatedResponse); err != nil {
		return nil, fmt.Errorf("failed to unmarshal data: %v", err)
	}

	return &paginatedResponse, nil
}
