package gcp

import (
	"context"
	"fmt"
	"time"
)

// batchEnableRequest is the body for serviceusage.services.batchEnable.
// serviceIds carries the bare IDs (e.g. "aiplatform.googleapis.com"),
// not fully-qualified names.
type batchEnableRequest struct {
	ServiceIds []string `json:"serviceIds"`
}

// BatchEnableServices enables the named APIs on the project. The call
// is idempotent — already-enabled APIs are a no-op. Returns when the
// operation completes (or the context fires).
//
// services contains entries like "aiplatform.googleapis.com",
// "apikeys.googleapis.com", "generativelanguage.googleapis.com".
func (c *Client) BatchEnableServices(ctx context.Context, projectID string, services []string) error {
	if len(services) == 0 {
		return nil
	}
	url := fmt.Sprintf("%s/v1/projects/%s/services:batchEnable", c.ServiceUsage, projectID)
	var op Operation
	if err := c.do(ctx, "POST", url, &batchEnableRequest{ServiceIds: services}, &op); err != nil {
		return fmt.Errorf("batch enable: %w", err)
	}
	if op.Done {
		if op.Error != nil {
			return op.Error
		}
		return nil
	}
	// serviceusage operations live under serviceusage.googleapis.com,
	// not resourcemanager. Poll there using the operation name returned.
	interval := c.PollInterval
	if interval <= 0 {
		interval = 2 * time.Second
	}
	return c.waitServiceUsageOperation(ctx, op.Name, interval)
}

// waitServiceUsageOperation is the Service Usage analog of
// WaitOperation — same polling shape, different host.
func (c *Client) waitServiceUsageOperation(ctx context.Context, opName string, pollInterval time.Duration) error {
	if pollInterval <= 0 {
		pollInterval = 2 * time.Second
	}
	url := c.ServiceUsage + "/v1/" + opName
	for {
		var op Operation
		if err := c.do(ctx, "GET", url, nil, &op); err != nil {
			return fmt.Errorf("poll serviceusage op: %w", err)
		}
		if op.Done {
			if op.Error != nil {
				return op.Error
			}
			return nil
		}
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(pollInterval):
		}
	}
}
