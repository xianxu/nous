package gcp

import (
	"context"
	"fmt"
)

// BillingInfo is the subset of cloudbilling.projects.getBillingInfo
// charon cares about. BillingAccountName is empty when the project
// has no billing account linked.
type BillingInfo struct {
	Name               string `json:"name"` // e.g. "projects/my-proj/billingInfo"
	ProjectID          string `json:"projectId"`
	BillingAccountName string `json:"billingAccountName,omitempty"` // "billingAccounts/XXXXXX-XXXXXX-XXXXXX"
	BillingEnabled     bool   `json:"billingEnabled"`
}

// GetBillingInfo reports whether the project has a billing account
// attached. AI Studio works without billing (free tier); Vertex
// requires it. Charon uses this to surface a non-fatal warning at
// project-setup time instead of letting the user hit BILLING_DISABLED
// at first Vertex call.
//
// Returns the BillingInfo even when billing is disabled — callers
// inspect BillingEnabled themselves.
func (c *Client) GetBillingInfo(ctx context.Context, projectID string) (*BillingInfo, error) {
	url := fmt.Sprintf("%s/v1/projects/%s/billingInfo", c.CloudBilling, projectID)
	var info BillingInfo
	if err := c.do(ctx, "GET", url, nil, &info); err != nil {
		return nil, fmt.Errorf("get billing info: %w", err)
	}
	return &info, nil
}
