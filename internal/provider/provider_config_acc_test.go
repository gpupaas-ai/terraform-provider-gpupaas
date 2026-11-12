package provider_test

import (
	"strings"
	"testing"
	"time"

	"github.com/gpupaas-ai/gpupaas-go/mock/bmapi"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"

	"github.com/gpupaas-ai/terraform-provider-gpupaas/internal/provider"
)

// withRequestTimeout adds request_timeout to the provider block of a rendered configuration.
func withRequestTimeout(cfg, value string) string {
	return strings.Replace(cfg, `token    = "test"`, "token    = \"test\"\n  request_timeout = "+value, 1)
}

func TestAccProvider_requestTimeout(t *testing.T) {
	srv, _ := newBMMock(t, bmapi.Options{})
	cfg := bmConfig(srv.URL, "", "")
	resource.Test(t, resource.TestCase{
		ProtoV6ProviderFactories: protoFactories(),
		CheckDestroy:             checkBMGone(srv),
		Steps: []resource.TestStep{
			{Config: withRequestTimeout(cfg, `"soon"`), PlanOnly: true, ExpectError: errRe(`Invalid request_timeout`)},
			{Config: withRequestTimeout(cfg, `"0s"`), PlanOnly: true, ExpectError: errRe(`must be greater than zero`)},
			{Config: withRequestTimeout(cfg, `"-5s"`), PlanOnly: true, ExpectError: errRe(`must be greater than zero`)},
			// A valid duration is accepted: the resource is created through a client with that limit.
			{Config: withRequestTimeout(cfg, `"2m"`), Check: resource.TestCheckResourceAttr(bmAddr, "status.phase", "running")},
		},
	})
}

func TestRequestTimeoutReachesTheHTTPClient(t *testing.T) {
	if c := provider.BuildHTTPClient(false, 30*time.Second); c != nil {
		t.Errorf("the default must keep the SDK client, got %+v", c)
	}
	c := provider.BuildHTTPClient(false, 2*time.Minute)
	if c == nil || c.Timeout != 2*time.Minute || c.Transport != nil {
		t.Errorf("a custom timeout must give a plain client with that limit, got %+v", c)
	}
	c = provider.BuildHTTPClient(true, 90*time.Second)
	if c == nil || c.Timeout != 90*time.Second || c.Transport == nil {
		t.Errorf("insecure must keep its transport and the custom limit, got %+v", c)
	}
	if c := provider.BuildHTTPClient(true, 30*time.Second); c == nil || c.Timeout != 30*time.Second {
		t.Errorf("insecure with the default limit, got %+v", c)
	}
}
