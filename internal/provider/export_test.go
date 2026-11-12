package provider

import (
	gpupaas "github.com/gpupaas-ai/gpupaas-go"
	"github.com/gpupaas-ai/gpupaas-go/clientset"

	"github.com/gpupaas-ai/terraform-provider-gpupaas/internal/brand"
)

// BuildHTTPClient exposes buildHTTPClient to the external tests.
var BuildHTTPClient = buildHTTPClient

// NewCapturing builds a provider for a brand whose clientset factory records the SDK config it is given.
func NewCapturing(version string, b brand.Brand, capture func(gpupaas.Config)) *GPUProvider {
	return &GPUProvider{version: version, brand: b, clientsetFactory: func(c gpupaas.Config) (clientset.Interface, error) {
		capture(c)
		return nil, nil
	}}
}
