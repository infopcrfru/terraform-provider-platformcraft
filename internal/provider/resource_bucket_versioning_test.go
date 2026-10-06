package provider

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// TestAccBucketVersioningResource_basic: Enabled -> Suspended (update in place)
// и импорт. Object Lock здесь сознательно не включается: с ним PlatformCraft
// (как и AWS) не даёт перевести версионирование в Suspended.
func TestAccBucketVersioningResource_basic(t *testing.T) {
	bucketName := testAccBucketName(t)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccBucketVersioningConfig(bucketName, "Enabled"),
				Check:  resource.TestCheckResourceAttr("platformcraft_bucket_versioning.test", "status", "Enabled"),
			},
			{
				Config: testAccBucketVersioningConfig(bucketName, "Suspended"),
				Check:  resource.TestCheckResourceAttr("platformcraft_bucket_versioning.test", "status", "Suspended"),
			},
			{
				ResourceName:      "platformcraft_bucket_versioning.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}

func testAccBucketVersioningConfig(bucketName, status string) string {
	return testAccConfigBucket(bucketName) + fmt.Sprintf(`
resource "platformcraft_bucket_versioning" "test" {
  bucket = platformcraft_bucket.test.bucket
  status = %[1]q
}
`, status)
}
