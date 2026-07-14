package main

import (
	"context"

	"github.com/AvizNetworks/terraform-provider-fabricapi/internal/provider"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
)

var version string = "1.0.0"

func main() {
	opts := providerserver.ServeOpts{
		Address: "registry.terraform.io/local/fabricapi",
	}

	err := providerserver.Serve(context.Background(), provider.New(version), opts)
	if err != nil {
		panic(err)
	}
}
