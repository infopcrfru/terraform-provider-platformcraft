package provider

import (
	"fmt"
	"testing"
	"time"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
)

// TestAccObjectRetentionResource_basic: retention GOVERNANCE на объекте,
// продление срока (update in place) и импорт. При destroy ресурс сокращает
// retention до «сейчас + 60 секунд» с bypass, объект удаляется с
// bypass_governance_retention.
func TestAccObjectRetentionResource_basic(t *testing.T) {
	bucketName := testAccBucketName(t)
	until1 := testAccRetainUntil(1 * time.Hour)
	until2 := testAccRetainUntil(2 * time.Hour)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccObjectRetentionConfig(bucketName, until1),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("platformcraft_object_retention.test", "mode", "GOVERNANCE"),
					resource.TestCheckResourceAttr("platformcraft_object_retention.test", "retain_until", until1),
					resource.TestCheckResourceAttr("platformcraft_object_retention.test", "id", bucketName+"/hello.txt"),
				),
			},
			{
				Config: testAccObjectRetentionConfig(bucketName, until2),
				Check:  resource.TestCheckResourceAttr("platformcraft_object_retention.test", "retain_until", until2),
			},
			{
				ResourceName:            "platformcraft_object_retention.test",
				ImportState:             true,
				ImportStateId:           bucketName + ",hello.txt",
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"bypass_governance_retention"},
			},
		},
	})
}

func testAccObjectRetentionConfig(bucketName, retainUntil string) string {
	return testAccConfigLockBucket(bucketName, 1) + testAccConfigLockedObject("retention test") + fmt.Sprintf(`
resource "platformcraft_object_retention" "test" {
  bucket                      = platformcraft_object.test.bucket
  key                         = platformcraft_object.test.key
  mode                        = "GOVERNANCE"
  retain_until                = %[1]q
  bypass_governance_retention = true
}
`, retainUntil)
}
