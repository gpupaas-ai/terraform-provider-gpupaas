// Package client wraps gpupaas-go clientset for the Terraform provider.
//
// Resources and data sources retrieve a ProviderData from ConfigureRequest.ProviderData
// to access the typed clientset without holding global state.
package client

import (
	"github.com/gpupaas-ai/gpupaas-go/clientset"
)

// ProviderData is the shared configuration handed to resources and data sources
// via the Configure phase. It holds a configured clientset.Interface.
type ProviderData struct {
	Clientset clientset.Interface
	Endpoint  string
	UserAgent string
}
