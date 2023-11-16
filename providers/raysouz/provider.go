package raysouz

import (
	resources "github.com/raywall/terraform-provider-raysouz/resources/raysouz"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
)

func Provider() *schema.Provider {
	return &schema.Provider{
		ResourcesMap: map[string]*schema.Resource{
			"raysouz_custom_resource": resources.ResourceCustom(),
		},
	}
}
