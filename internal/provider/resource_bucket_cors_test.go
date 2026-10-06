package provider

import (
	"context"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
)

// TestAccBucketCorsResource_multipleRules: два правила, изменение правила,
// импорт и дрифт (CORS удалили мимо Terraform -> plan предлагает создать заново).
func TestAccBucketCorsResource_multipleRules(t *testing.T) {
	bucketName := testAccBucketName(t)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccBucketCorsConfig(bucketName, 3000),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("platformcraft_bucket_cors.test", "rule.#", "2"),
					resource.TestCheckResourceAttr("platformcraft_bucket_cors.test", "rule.0.max_age_seconds", "3000"),
					resource.TestCheckResourceAttr("platformcraft_bucket_cors.test", "rule.1.max_age_seconds", "600"),
					resource.TestCheckResourceAttr("platformcraft_bucket_cors.test", "rule.1.allowed_origins.0", "https://admin.example.com"),
					resource.TestCheckResourceAttr("platformcraft_bucket_cors.test", "rule.1.allowed_methods.#", "5"),
				),
			},
			{
				Config: testAccBucketCorsConfig(bucketName, 1200),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("platformcraft_bucket_cors.test", plancheck.ResourceActionUpdate),
					},
				},
				Check: resource.TestCheckResourceAttr("platformcraft_bucket_cors.test", "rule.0.max_age_seconds", "1200"),
			},
			{
				ResourceName:      "platformcraft_bucket_cors.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
			{
				PreConfig: func() {
					testAccRetry(t, "удаление CORS мимо Terraform", func(ctx context.Context) error {
						client := testAccS3Client(t)
						if _, err := client.DeleteBucketCors(ctx, &s3.DeleteBucketCorsInput{Bucket: aws.String(bucketName)}); err != nil && !isNoSuchCorsConfiguration(err) {
							return err
						}
						if _, err := client.GetBucketCors(ctx, &s3.GetBucketCorsInput{Bucket: aws.String(bucketName)}); !isNoSuchCorsConfiguration(err) {
							return fmt.Errorf("CORS всё ещё читается после удаления: %v", err)
						}
						return nil
					})
				},
				Config: testAccBucketCorsConfig(bucketName, 1200),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("platformcraft_bucket_cors.test", plancheck.ResourceActionCreate),
					},
				},
			},
		},
	})
}

func testAccBucketCorsConfig(bucketName string, firstMaxAge int) string {
	return testAccConfigBucket(bucketName) + fmt.Sprintf(`
resource "platformcraft_bucket_cors" "test" {
  bucket = platformcraft_bucket.test.bucket

  rule {
    allowed_origins = ["*"]
    allowed_methods = ["GET", "HEAD"]
    allowed_headers = ["*"]
    max_age_seconds = %[1]d
  }

  rule {
    allowed_origins = ["https://admin.example.com"]
    allowed_methods = ["GET", "PUT", "POST", "DELETE", "HEAD"]
    allowed_headers = ["*"]
    max_age_seconds = 600
  }
}
`, firstMaxAge)
}

// TestAccBucketCorsResource_defaultMaxAge: правило без max_age_seconds получает
// значение по умолчанию (3000), и повторный plan после apply пустой.
func TestAccBucketCorsResource_defaultMaxAge(t *testing.T) {
	bucketName := testAccBucketName(t)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccConfigBucket(bucketName) + `
resource "platformcraft_bucket_cors" "test" {
  bucket = platformcraft_bucket.test.bucket

  rule {
    allowed_origins = ["*"]
    allowed_methods = ["GET"]
    allowed_headers = ["*"]
  }
}
`,
				Check: resource.TestCheckResourceAttr("platformcraft_bucket_cors.test", "rule.0.max_age_seconds", "3000"),
			},
			{
				ResourceName:      "platformcraft_bucket_cors.test",
				ImportState:       true,
				ImportStateVerify: true,
			},
		},
	})
}
