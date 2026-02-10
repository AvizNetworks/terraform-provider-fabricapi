package main

import (
	"context"
	"github.com/hashicorp/terraform-plugin-framework/providerserver"
	"terraform-provider-fabricapi/internal/provider"
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
