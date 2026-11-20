package main

import (
	"flag"
	"fmt"
	"log"

	gpupaas "github.com/gpupaas-ai/gpupaas-go"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6"
	"github.com/hashicorp/terraform-plugin-go/tfprotov6/tf6server"

	"github.com/gpupaas-ai/terraform-provider-gpupaas/internal/brand"
	"github.com/gpupaas-ai/terraform-provider-gpupaas/internal/provider"
)

var version = "dev"

func main() {
	var debug bool
	flag.BoolVar(&debug, "debug", false, "set to true to run the provider with support for debuggers like delve")
	var printBrand bool
	flag.BoolVar(&printBrand, "brand", false, "print the brand this plugin runs as and exit")
	flag.Parse()

	// One build serves every brand: the brand is the plugin's file name (terraform-provider-<type>[_v<version>]),
	// or a build-time pin (see internal/brand.Resolve).
	b, err := brand.Current()
	if err != nil {
		log.Fatal(err.Error())
	}
	if printBrand {
		fmt.Printf("%s %s %s\n", b.Type, b.Registry, gpupaas.ControllerAPI)
		return
	}

	var opts []tf6server.ServeOpt
	if debug {
		opts = append(opts, tf6server.WithManagedDebug())
	}
	if b.Type != brand.Official {
		// debug log lines would otherwise carry the source path of the call, which names the module
		opts = append(opts, tf6server.WithoutLogLocation())
	}
	serve := func() tfprotov6.ProviderServer {
		return brand.Wrap(providerserver.NewProtocol6(provider.NewBranded(version, b)())(), b)
	}
	if err := tf6server.Serve(b.Registry, serve, opts...); err != nil {
		log.Fatal(err.Error())
	}
}
