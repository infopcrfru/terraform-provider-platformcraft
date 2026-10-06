package provider

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"

	"github.com/infopcrfru/terraform-provider-platformcraft/internal/pcs3"
)

// TestAccBucketPolicyResource_basic: создание, изменение политики, импорт и
// дрифт двух видов: политику заменили и политику удалили мимо Terraform.
//
// Каждый шаг terraform-plugin-testing завершает повторным plan и падает, если
// он не пустой, — так проверяется, что Principal "*" (который API возвращает
// как {"AWS":["*"]}) не даёт вечного диффа.
func TestAccBucketPolicyResource_basic(t *testing.T) {
	bucketName := testAccBucketName(t)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccBucketPolicyConfig(bucketName, "PublicReadV1"),
				Check: resource.ComposeAggregateTestCheckFunc(
					resource.TestCheckResourceAttr("platformcraft_bucket_policy.test", "bucket", bucketName),
					resource.TestCheckResourceAttrSet("platformcraft_bucket_policy.test", "policy"),
				),
			},
			{
				Config: testAccBucketPolicyConfig(bucketName, "PublicReadV2"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("platformcraft_bucket_policy.test", plancheck.ResourceActionUpdate),
					},
				},
			},
			{
				// После импорта в state лежит JSON в том виде, в каком его вернул
				// API, а после apply — в том, в каком он записан в конфиге.
				// Семантически они равны, но строки разные, поэтому policy не сверяем.
				ResourceName:            "platformcraft_bucket_policy.test",
				ImportState:             true,
				ImportStateVerify:       true,
				ImportStateVerifyIgnore: []string{"policy"},
			},
			{
				// Дрифт: политику заменили мимо Terraform -> plan предлагает вернуть свою.
				PreConfig: func() {
					other := fmt.Sprintf(`{"Version":"2012-10-17","Statement":[{"Sid":"ChangedOutside","Effect":"Allow","Principal":"*","Action":["s3:ListBucket"],"Resource":["arn:aws:s3:::%s"]}]}`, bucketName)
					testAccRetry(t, "замена политики мимо Terraform", func(ctx context.Context) error {
						client := testAccS3Client(t)
						if _, err := client.PutBucketPolicy(ctx, &s3.PutBucketPolicyInput{Bucket: aws.String(bucketName), Policy: aws.String(other)}); err != nil {
							return err
						}
						out, err := client.GetBucketPolicy(ctx, &s3.GetBucketPolicyInput{Bucket: aws.String(bucketName)})
						if err != nil {
							return err
						}
						if !strings.Contains(aws.ToString(out.Policy), "ChangedOutside") {
							return fmt.Errorf("новая политика ещё не читается")
						}
						return nil
					})
				},
				Config: testAccBucketPolicyConfig(bucketName, "PublicReadV2"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("platformcraft_bucket_policy.test", plancheck.ResourceActionUpdate),
					},
				},
			},
			{
				// Дрифт: политику удалили мимо Terraform -> plan предлагает создать заново.
				// PlatformCraft после удаления отдаёт пустой документ вместо
				// NoSuchBucketPolicy; провайдер должен считать это отсутствием политики.
				PreConfig: func() {
					testAccRetry(t, "удаление политики мимо Terraform", func(ctx context.Context) error {
						client := testAccS3Client(t)
						if _, err := client.DeleteBucketPolicy(ctx, &s3.DeleteBucketPolicyInput{Bucket: aws.String(bucketName)}); err != nil && !isNoSuchBucketPolicy(err) {
							return err
						}
						out, err := client.GetBucketPolicy(ctx, &s3.GetBucketPolicyInput{Bucket: aws.String(bucketName)})
						if isNoSuchBucketPolicy(err) || (err == nil && pcs3.IsEmptyPolicy(aws.ToString(out.Policy))) {
							return nil
						}
						return fmt.Errorf("политика всё ещё читается после удаления: %v", err)
					})
				},
				Config: testAccBucketPolicyConfig(bucketName, "PublicReadV2"),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("platformcraft_bucket_policy.test", plancheck.ResourceActionCreate),
					},
				},
			},
		},
	})
}

func testAccBucketPolicyConfig(bucketName, sid string) string {
	return testAccConfigBucket(bucketName) + fmt.Sprintf(`
resource "platformcraft_bucket_policy" "test" {
  bucket = platformcraft_bucket.test.bucket
  policy = jsonencode({
    Version = "2012-10-17"
    Statement = [{
      Sid       = %[2]q
      Effect    = "Allow"
      Principal = "*"
      Action    = ["s3:GetObject"]
      Resource  = ["arn:aws:s3:::%[1]s/public/*"]
    }]
  })
}
`, bucketName, sid)
}
