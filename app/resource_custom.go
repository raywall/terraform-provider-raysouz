package main

import (
	"fmt"

	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/schema"
	"github.com/hashicorp/terraform-plugin-sdk/v2/helper/validation"
)

func resourceCustom() *schema.Resource {
	return &schema.Resource{
		Create: resourceCustomCreate,
		Read:   resourceCustomRead,
		Update: resourceCustomUpdate,
		Delete: resourceCustomDelete,

		Schema: map[string]*schema.Schema{
			"message": {
				Type:         schema.TypeString,
				Required:     true,
				Description:  "The message for the custom resource.",
				ValidateFunc: validation.StringLenBetween(1, 50),
			},
			"cloud": {
				Type:        schema.TypeString,
				Required:    true,
				Description: "The target cloud (aws, azure, gcp).",
			},
		},
	}
}

func resourceCustomCreate(d *schema.ResourceData, m interface{}) error {
	message := d.Get("message").(string)
	cloud := d.Get("cloud").(string)

	fmt.Printf("Creating custom resource with message: %s for cloud: %s\n", message, cloud)

	// Lógica para criar a role e policy dependendo da nuvem selecionada
	switch cloud {
	case "aws":
		// Implementar a criação da role e policy na AWS
		fmt.Println("Creating AWS resources...")
	case "azure":
		// Implementar a criação da role e policy no Azure
		fmt.Println("Creating Azure resources...")
	case "gcp":
		// Implementar a criação da role e policy no GCP
		fmt.Println("Creating GCP resources...")
	default:
		return fmt.Errorf("Cloud %s not supported", cloud)
	}

	d.SetId(message)
	return nil
}

func resourceCustomRead(d *schema.ResourceData, m interface{}) error {
	message := d.Get("message").(string)
	cloud := d.Get("cloud").(string)
	fmt.Printf("Reading custom resource with message: %s for cloud: %s\n", message, cloud)
	// Lógica para ler informações sobre a role e policy
	return nil
}

func resourceCustomUpdate(d *schema.ResourceData, m interface{}) error {
	message := d.Get("message").(string)
	cloud := d.Get("cloud").(string)
	fmt.Printf("Updating custom resource with message: %s for cloud: %s\n", message, cloud)
	// Lógica para atualizar a role e policy
	return nil
}

func resourceCustomDelete(d *schema.ResourceData, m interface{}) error {
	message := d.Get("message").(string)
	cloud := d.Get("cloud").(string)
	fmt.Printf("Deleting custom resource with message: %s for cloud: %s\n", message, cloud)
	// Lógica para excluir a role e policy
	d.SetId("")
	return nil
}
