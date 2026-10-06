package provider

import (
	"fmt"
	"testing"

	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
)

// TestAccObjectCopyResource_basic: серверная копия объекта в другой бакет под
// ключом с пробелами и кириллицей (проверяет URL-кодирование x-amz-copy-source),
// смена источника (update in place) и импорт.
func TestAccObjectCopyResource_basic(t *testing.T) {
	srcBucket := testAccBucketName(t)
	dstBucket := testAccBucketName(t)
	dstKey := "копии/hello copy.txt"

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccObjectCopyConfig(srcBucket, dstBucket, "src/исходник 1.txt", dstKey),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("platformcraft_object_copy.test", "id", dstBucket+"/"+dstKey),
					resource.TestCheckResourceAttrSet("platformcraft_object_copy.test", "etag"),
					testAccCheckObjectContent(t, dstBucket, dstKey, "content one"),
				),
			},
			{
				Config: testAccObjectCopyConfig(srcBucket, dstBucket, "src/second.txt", dstKey),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("platformcraft_object_copy.test", plancheck.ResourceActionUpdate),
					},
				},
				Check: testAccCheckObjectContent(t, dstBucket, dstKey, "content two"),
			},
			{
				// Источник копии в самом объекте не хранится, поэтому после импорта
				// source_* неизвестны и не сверяются.
				ResourceName:            "platformcraft_object_copy.test",
				ImportState:             true,
				ImportStateId:           dstBucket + "," + dstKey,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"source_bucket", "source_key", "bypass_governance_retention"},
			},
		},
	})
}

func testAccObjectCopyConfig(srcBucket, dstBucket, sourceKey, dstKey string) string {
	return fmt.Sprintf(`
resource "platformcraft_bucket" "src" {
  bucket        = %[1]q
  force_destroy = true
}

resource "platformcraft_bucket" "dst" {
  bucket        = %[2]q
  force_destroy = true
}

resource "platformcraft_object" "one" {
  bucket  = platformcraft_bucket.src.bucket
  key     = "src/исходник 1.txt"
  content = "content one"
}

resource "platformcraft_object" "two" {
  bucket  = platformcraft_bucket.src.bucket
  key     = "src/second.txt"
  content = "content two"
}

resource "platformcraft_object_copy" "test" {
  source_bucket = platformcraft_bucket.src.bucket
  source_key    = %[3]q
  bucket        = platformcraft_bucket.dst.bucket
  key           = %[4]q

  depends_on = [platformcraft_object.one, platformcraft_object.two]
}
`, srcBucket, dstBucket, sourceKey, dstKey)
}
