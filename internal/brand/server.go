package brand

import (
	"context"

	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
)

// Server wraps a provider server so that every schema description and every diagnostic names this brand
// (`acme_project`, `ACME_API_KEY`) instead of the official identity the resource code is written with.
// Data (configuration, plans, state) is never touched, only the human-readable text.
type Server struct {
	tfprotov6.ProviderServer
	b Brand
}

// Wrap returns the server for a brand. The official brand needs no wrapper.
func Wrap(s tfprotov6.ProviderServer, b Brand) tfprotov6.ProviderServer {
	if b.Type == Official {
		return s
	}
	return &Server{ProviderServer: s, b: b}
}

func (s *Server) diags(ds []*tfprotov6.Diagnostic) {
	for _, d := range ds {
		if d != nil {
			d.Summary, d.Detail = s.b.Rewrite(d.Summary), s.b.Rewrite(d.Detail)
		}
	}
}

func (s *Server) block(b *tfprotov6.SchemaBlock) {
	if b == nil {
		return
	}
	b.Description = s.b.Rewrite(b.Description)
	for _, a := range b.Attributes {
		s.attribute(a)
	}
	for _, nb := range b.BlockTypes {
		if nb != nil {
			s.block(nb.Block)
		}
	}
}

func (s *Server) attribute(a *tfprotov6.SchemaAttribute) {
	if a == nil {
		return
	}
	a.Description = s.b.Rewrite(a.Description)
	if a.NestedType != nil {
		for _, n := range a.NestedType.Attributes {
			s.attribute(n)
		}
	}
}

func (s *Server) schema(sc *tfprotov6.Schema) {
	if sc != nil {
		s.block(sc.Block)
	}
}

func (s *Server) GetProviderSchema(ctx context.Context, req *tfprotov6.GetProviderSchemaRequest) (*tfprotov6.GetProviderSchemaResponse, error) {
	resp, err := s.ProviderServer.GetProviderSchema(ctx, req)
	if resp != nil {
		s.schema(resp.Provider)
		s.schema(resp.ProviderMeta)
		for _, sc := range resp.ResourceSchemas {
			s.schema(sc)
		}
		for _, sc := range resp.DataSourceSchemas {
			s.schema(sc)
		}
		s.diags(resp.Diagnostics)
	}
	return resp, err
}

func (s *Server) ValidateProviderConfig(ctx context.Context, req *tfprotov6.ValidateProviderConfigRequest) (*tfprotov6.ValidateProviderConfigResponse, error) {
	resp, err := s.ProviderServer.ValidateProviderConfig(ctx, req)
	if resp != nil {
		s.diags(resp.Diagnostics)
	}
	return resp, err
}

func (s *Server) ConfigureProvider(ctx context.Context, req *tfprotov6.ConfigureProviderRequest) (*tfprotov6.ConfigureProviderResponse, error) {
	resp, err := s.ProviderServer.ConfigureProvider(ctx, req)
	if resp != nil {
		s.diags(resp.Diagnostics)
	}
	return resp, err
}

func (s *Server) ValidateResourceConfig(ctx context.Context, req *tfprotov6.ValidateResourceConfigRequest) (*tfprotov6.ValidateResourceConfigResponse, error) {
	resp, err := s.ProviderServer.ValidateResourceConfig(ctx, req)
	if resp != nil {
		s.diags(resp.Diagnostics)
	}
	return resp, err
}

func (s *Server) ValidateDataResourceConfig(ctx context.Context, req *tfprotov6.ValidateDataResourceConfigRequest) (*tfprotov6.ValidateDataResourceConfigResponse, error) {
	resp, err := s.ProviderServer.ValidateDataResourceConfig(ctx, req)
	if resp != nil {
		s.diags(resp.Diagnostics)
	}
	return resp, err
}

func (s *Server) UpgradeResourceState(ctx context.Context, req *tfprotov6.UpgradeResourceStateRequest) (*tfprotov6.UpgradeResourceStateResponse, error) {
	resp, err := s.ProviderServer.UpgradeResourceState(ctx, req)
	if resp != nil {
		s.diags(resp.Diagnostics)
	}
	return resp, err
}

func (s *Server) ReadResource(ctx context.Context, req *tfprotov6.ReadResourceRequest) (*tfprotov6.ReadResourceResponse, error) {
	resp, err := s.ProviderServer.ReadResource(ctx, req)
	if resp != nil {
		s.diags(resp.Diagnostics)
	}
	return resp, err
}

func (s *Server) PlanResourceChange(ctx context.Context, req *tfprotov6.PlanResourceChangeRequest) (*tfprotov6.PlanResourceChangeResponse, error) {
	resp, err := s.ProviderServer.PlanResourceChange(ctx, req)
	if resp != nil {
		s.diags(resp.Diagnostics)
	}
	return resp, err
}

func (s *Server) ApplyResourceChange(ctx context.Context, req *tfprotov6.ApplyResourceChangeRequest) (*tfprotov6.ApplyResourceChangeResponse, error) {
	resp, err := s.ProviderServer.ApplyResourceChange(ctx, req)
	if resp != nil {
		s.diags(resp.Diagnostics)
	}
	return resp, err
}

func (s *Server) ImportResourceState(ctx context.Context, req *tfprotov6.ImportResourceStateRequest) (*tfprotov6.ImportResourceStateResponse, error) {
	resp, err := s.ProviderServer.ImportResourceState(ctx, req)
	if resp != nil {
		s.diags(resp.Diagnostics)
	}
	return resp, err
}

func (s *Server) ReadDataSource(ctx context.Context, req *tfprotov6.ReadDataSourceRequest) (*tfprotov6.ReadDataSourceResponse, error) {
	resp, err := s.ProviderServer.ReadDataSource(ctx, req)
	if resp != nil {
		s.diags(resp.Diagnostics)
	}
	return resp, err
}

// The methods below are not used by the provider today (no functions, ephemeral resources or resource
// identities). They are wrapped anyway so that adding one of those features cannot leak the official identity;
// TestServerWrapsEveryMethodThatCarriesText fails if a method is missing here.

func (s *Server) GetMetadata(ctx context.Context, req *tfprotov6.GetMetadataRequest) (*tfprotov6.GetMetadataResponse, error) {
	resp, err := s.ProviderServer.GetMetadata(ctx, req)
	if resp != nil {
		s.diags(resp.Diagnostics)
	}
	return resp, err
}

func (s *Server) MoveResourceState(ctx context.Context, req *tfprotov6.MoveResourceStateRequest) (*tfprotov6.MoveResourceStateResponse, error) {
	resp, err := s.ProviderServer.MoveResourceState(ctx, req)
	if resp != nil {
		s.diags(resp.Diagnostics)
	}
	return resp, err
}

func (s *Server) GetFunctions(ctx context.Context, req *tfprotov6.GetFunctionsRequest) (*tfprotov6.GetFunctionsResponse, error) {
	resp, err := s.ProviderServer.GetFunctions(ctx, req)
	if resp != nil {
		s.diags(resp.Diagnostics)
	}
	return resp, err
}

func (s *Server) CallFunction(ctx context.Context, req *tfprotov6.CallFunctionRequest) (*tfprotov6.CallFunctionResponse, error) {
	resp, err := s.ProviderServer.CallFunction(ctx, req)
	if resp != nil && resp.Error != nil {
		resp.Error.Text = s.b.Rewrite(resp.Error.Text)
	}
	return resp, err
}

func (s *Server) GetResourceIdentitySchemas(ctx context.Context, req *tfprotov6.GetResourceIdentitySchemasRequest) (*tfprotov6.GetResourceIdentitySchemasResponse, error) {
	resp, err := s.ProviderServer.GetResourceIdentitySchemas(ctx, req)
	if resp != nil {
		s.diags(resp.Diagnostics)
	}
	return resp, err
}

func (s *Server) UpgradeResourceIdentity(ctx context.Context, req *tfprotov6.UpgradeResourceIdentityRequest) (*tfprotov6.UpgradeResourceIdentityResponse, error) {
	resp, err := s.ProviderServer.UpgradeResourceIdentity(ctx, req)
	if resp != nil {
		s.diags(resp.Diagnostics)
	}
	return resp, err
}

func (s *Server) ValidateEphemeralResourceConfig(ctx context.Context, req *tfprotov6.ValidateEphemeralResourceConfigRequest) (*tfprotov6.ValidateEphemeralResourceConfigResponse, error) {
	resp, err := s.ProviderServer.ValidateEphemeralResourceConfig(ctx, req)
	if resp != nil {
		s.diags(resp.Diagnostics)
	}
	return resp, err
}

func (s *Server) OpenEphemeralResource(ctx context.Context, req *tfprotov6.OpenEphemeralResourceRequest) (*tfprotov6.OpenEphemeralResourceResponse, error) {
	resp, err := s.ProviderServer.OpenEphemeralResource(ctx, req)
	if resp != nil {
		s.diags(resp.Diagnostics)
	}
	return resp, err
}

func (s *Server) RenewEphemeralResource(ctx context.Context, req *tfprotov6.RenewEphemeralResourceRequest) (*tfprotov6.RenewEphemeralResourceResponse, error) {
	resp, err := s.ProviderServer.RenewEphemeralResource(ctx, req)
	if resp != nil {
		s.diags(resp.Diagnostics)
	}
	return resp, err
}

func (s *Server) CloseEphemeralResource(ctx context.Context, req *tfprotov6.CloseEphemeralResourceRequest) (*tfprotov6.CloseEphemeralResourceResponse, error) {
	resp, err := s.ProviderServer.CloseEphemeralResource(ctx, req)
	if resp != nil {
		s.diags(resp.Diagnostics)
	}
	return resp, err
}

func (s *Server) StopProvider(ctx context.Context, req *tfprotov6.StopProviderRequest) (*tfprotov6.StopProviderResponse, error) {
	resp, err := s.ProviderServer.StopProvider(ctx, req)
	if resp != nil {
		resp.Error = s.b.Rewrite(resp.Error)
	}
	return resp, err
}
