package provider

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// TestAccBucketAclResource_basic: private -> public-read -> private и импорт.
// Canned ACL не восстанавливается однозначно из списка грантов, поэтому после
// импорта поле acl пустое и в ImportStateVerify не сверяется.
func TestAccBucketAclResource_basic(t *testing.T) {
	bucketName := testAccBucketName(t)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccBucketAclConfig(bucketName, "private"),
				Check:  resource.TestCheckResourceAttr("platformcraft_bucket_acl.test", "acl", "private"),
			},
			{
				Config: testAccBucketAclConfig(bucketName, "public-read"),
				Check:  resource.TestCheckResourceAttr("platformcraft_bucket_acl.test", "acl", "public-read"),
			},
			{
				Config: testAccBucketAclConfig(bucketName, "private"),
				Check:  resource.TestCheckResourceAttr("platformcraft_bucket_acl.test", "acl", "private"),
			},
			{
				ResourceName:            "platformcraft_bucket_acl.test",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"acl"},
			},
		},
	})
}

func testAccBucketAclConfig(bucketName, acl string) string {
	return testAccConfigBucket(bucketName) + fmt.Sprintf(`
resource "platformcraft_bucket_acl" "test" {
  bucket = platformcraft_bucket.test.bucket
  acl    = %[1]q
}
`, acl)
}
