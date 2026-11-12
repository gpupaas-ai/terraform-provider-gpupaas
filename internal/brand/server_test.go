package brand_test

import (
	"context"
	"testing"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"

	"github.com/gpupaas-ai/terraform-provider-gpupaas/internal/brand"
)

// stub is a provider server that answers with the official identity in its text and in its data.
type stub struct{ tfprotov6.ProviderServer }

func (stub) ReadResource(context.Context, *tfprotov6.ReadResourceRequest) (*tfprotov6.ReadResourceResponse, error) {
	return &tfprotov6.ReadResourceResponse{
		Private: []byte("gpupaas_private_data"),
		Diagnostics: []*tfprotov6.Diagnostic{{
			Severity:  tfprotov6.DiagnosticSeverityError,
			Summary:   "gpupaas_project failed",
			Detail:    "Set GPUPAAS_API_KEY; the project gpupaas-ai/gpupaas on GPU PaaS",
			Attribute: nil,
		}},
	}, nil
}

func (stub) CallFunction(context.Context, *tfprotov6.CallFunctionRequest) (*tfprotov6.CallFunctionResponse, error) {
	return &tfprotov6.CallFunctionResponse{Error: &tfprotov6.FunctionError{Text: "bad gpupaas_ call"}}, nil
}

func (stub) StopProvider(context.Context, *tfprotov6.StopProviderRequest) (*tfprotov6.StopProviderResponse, error) {
	return &tfprotov6.StopProviderResponse{Error: "stopping gpupaas_x"}, nil
}

func TestServerRewritesTextAndLeavesDataAlone(t *testing.T) {
	ctx := context.Background()
	srv := brand.Wrap(stub{}, brand.MustNew("acme"))

	rr, err := srv.ReadResource(ctx, &tfprotov6.ReadResourceRequest{})
	if err != nil {
		t.Fatal(err)
	}
	d := rr.Diagnostics[0]
	if d.Summary != "acme_project failed" || d.Detail != "Set ACME_API_KEY; the project gpupaas-ai/gpupaas on GPU PaaS" {
		t.Errorf("diagnostic text: %q / %q", d.Summary, d.Detail)
	}
	if string(rr.Private) != "gpupaas_private_data" {
		t.Errorf("data must never be rewritten, got %q", rr.Private)
	}
	if cf, _ := srv.CallFunction(ctx, &tfprotov6.CallFunctionRequest{}); cf.Error.Text != "bad acme_ call" {
		t.Errorf("function error: %q", cf.Error.Text)
	}
	if sp, _ := srv.StopProvider(ctx, &tfprotov6.StopProviderRequest{}); sp.Error != "stopping acme_x" {
		t.Errorf("stop error: %q", sp.Error)
	}

	// the official brand is not wrapped at all
	official := brand.Wrap(stub{}, brand.MustNew("gpupaas"))
	rr, _ = official.ReadResource(ctx, &tfprotov6.ReadResourceRequest{})
	if rr.Diagnostics[0].Summary != "gpupaas_project failed" {
		t.Errorf("official text changed: %q", rr.Diagnostics[0].Summary)
	}
}
