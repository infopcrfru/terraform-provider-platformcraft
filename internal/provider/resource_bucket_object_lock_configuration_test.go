package provider

import (
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// TestAccBucketObjectLockConfigurationResource_basic: включение Object Lock
// (GOVERNANCE, 1 день), изменение срока до 2 дней и импорт.
//
// При destroy PlatformCraft (как и AWS) не позволяет выключить Object Lock у
// бакета: ресурс выдаёт предупреждение и уходит из state, бакет удаляется
// вместе с ним. Используется только GOVERNANCE — COMPLIANCE в тестах не
// применяется, его нельзя снять досрочно.
func TestAccBucketObjectLockConfigurationResource_basic(t *testing.T) {
	bucketName := testAccBucketName(t)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccConfigLockBucket(bucketName, 1),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("platformcraft_bucket_object_lock_configuration.test", "mode", "GOVERNANCE"),
					resource.TestCheckResourceAttr("platformcraft_bucket_object_lock_configuration.test", "days", "1"),
					resource.TestCheckResourceAttr("platformcraft_bucket_versioning.test", "status", "Enabled"),
				),
			},
			{
				Config: testAccConfigLockBucket(bucketName, 2),
				Check:  resource.TestCheckResourceAttr("platformcraft_bucket_object_lock_configuration.test", "days", "2"),
			},
			{
				ResourceName:      "platformcraft_bucket_object_lock_configuration.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}
