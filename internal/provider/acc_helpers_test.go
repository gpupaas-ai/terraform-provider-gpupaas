package provider_test

import (
	"fmt"
	"regexp"
	"strings"

	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"

	"github.com/gpupaas-ai/terraform-provider-gpupaas/internal/provider"
)

// These tests run the real terraform binary against the provider and an in-process
// mock of the platform API. Run them with TF_ACC=1.

const testProject = "system-catalog"

// errRe builds an error pattern that tolerates Terraform wrapping long diagnostics across lines.
func errRe(text string) *regexp.Regexp {
	return regexp.MustCompile(strings.ReplaceAll(regexp.QuoteMeta(text), " ", `\s+`))
}

func protoFactories() map[string]func() (tfprotov6.ProviderServer, error) {
	return map[string]func() (tfprotov6.ProviderServer, error){
		"gpupaas": providerserver.NewProtocol6WithError(provider.New("test")()),
	}
}

func providerBlock(url string) string {
	return fmt.Sprintf("provider \"gpupaas\" {\n  endpoint = %q\n  token    = \"test\"\n}\n", url)
}
