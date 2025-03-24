## GDPRClientLibrary

This library allows client services to call CincinnatiAI GDPR to file information or delete requests. This only applies to services that use Cincinnati AI's Account services.

### Usage Example

```
package main

import (
	"fmt"
	"log"
	"time"

	"github.com/yourusername/gdprclient" // Replace with your actual package path
)

func main() {
	// Initialize the client with custom retry policy
	client := gdprclient.NewClient(
		"https://api.example.com", // Your API Gateway URL
		"your-api-key",            // Your API key
		gdprclient.WithTimeout(15*time.Second),
		gdprclient.WithEnvironment("Prod"),
		gdprclient.WithRetryPolicy(gdprclient.RetryPolicy{
			MaxRetries:     3,
			InitialBackoff: 200 * time.Millisecond,
			MaxBackoff:     5 * time.Second,
			BackoffFactor:  2.0,
			Jitter:         0.2,
		}),
	)

	// Example 1: Create an information request
	infoRequest, err := client.CreateInfoRequest(gdprclient.CreateInfoRequestInput{
		PartitionKey: "user123",
		Type:         gdprclient.TypeInfoRequest,
		CreatedBy:    "user@example.com",
	})
	if err != nil {
		log.Fatalf("Failed to create info request: %v", err)
	}
	fmt.Printf("Created info request: %+v\n", infoRequest)

	// Example 2: Create a deletion request
	deleteRequest, err := client.CreateDeleteRequest(gdprclient.CreateDeleteRequestInput{
		PartitionKey: "user123",
		Type:         gdprclient.TypeDeleteRequest,
		CreatedBy:    "user@example.com",
	})
	if err != nil {
		log.Fatalf("Failed to create delete request: %v", err)
	}
	fmt.Printf("Created delete request: %+v\n", deleteRequest)

	// Example 3: Fetch an information request
	fetchedInfo, err := client.FetchInfoRequest(gdprclient.FetchRequestInput{
		PartitionKey: "user123",
		RangeKey:     infoRequest.RangeKey,
	})
	if err != nil {
		log.Fatalf("Failed to fetch info request: %v", err)
	}
	fmt.Printf("Fetched info request: %+v\n", fetchedInfo)

	// Example 4: Update a deletion request status
	success, err := client.UpdateDeleteRequest(gdprclient.UpdateRequestInput{
		PartitionKey: "user123",
		RangeKey:     deleteRequest.RangeKey,
		Status:       gdprclient.StatusComplete,
	})
	if err != nil {
		log.Fatalf("Failed to update delete request: %v", err)
	}
	fmt.Printf("Update delete request success: %v\n", success)

	// Example 5: Fetch all info requests for a user
	allRequests, err := client.FetchAllInfoRequests(gdprclient.FetchAllRequestInput{
		PartitionKey: "user123",
	})
	if err != nil {
		log.Fatalf("Failed to fetch all info requests: %v", err)
	}
	fmt.Printf("Found %d info requests\n", len(allRequests.Results))

	// Example 6: Fetch deletion requests by status
	pendingRequests, err := client.FetchDeleteRequestsByStatus(gdprclient.FetchByStatusInput{
		Status: gdprclient.StatusPending,
	})
	if err != nil {
		log.Fatalf("Failed to fetch pending delete requests: %v", err)
	}
	fmt.Printf("Found %d pending delete requests\n", len(pendingRequests.Results))
}
```
