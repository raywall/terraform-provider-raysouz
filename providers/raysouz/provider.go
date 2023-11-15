package raysouz

import (
	resources "terraform-provider-raysouz/resources/raysouz"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

func Provider() *schema.Provider {
	return &schema.Provider{
		ResourcesMap: map[string]*schema.Resource{
			"custom_resource": resources.ResourceCustom(),
		},
	}
}
