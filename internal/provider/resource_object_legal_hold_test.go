package provider

import (
	"context"
	"fmt"
	"testing"

	"github.com/aws/aws-sdk-go-v2/aws"
	"github.com/aws/aws-sdk-go-v2/service/s3"
	s3types "github.com/aws/aws-sdk-go-v2/service/s3/types"
	"github.com/hashicorp/terraform-plugin-testing/helper/resource"
	"github.com/hashicorp/terraform-plugin-testing/plancheck"
)

// TestAccObjectLegalHoldResource_basic: включение legal hold, выключение,
// импорт и дрифт (legal hold включили мимо Terraform -> plan предлагает update).
func TestAccObjectLegalHoldResource_basic(t *testing.T) {
	bucketName := testAccBucketName(t)

	resource.Test(t, resource.TestCase{
		PreCheck:                 func() { testAccPreCheck(t) },
		ProtoV6ProviderFactories: testAccProtoV6ProviderFactories,
		Steps: []resource.TestStep{
			{
				Config: testAccObjectLegalHoldConfig(bucketName, true),
				Check:  resource.TestCheckResourceAttr("platformcraft_object_legal_hold.test", "enabled", "true"),
			},
			{
				Config: testAccObjectLegalHoldConfig(bucketName, false),
				Check:  resource.TestCheckResourceAttr("platformcraft_object_legal_hold.test", "enabled", "false"),
			},
			{
				ResourceName:      "platformcraft_object_legal_hold.test",
				ImportState:       true,
				ImportStateId:     bucketName + ",hello.txt",
				ImportStateVerify: true,
			},
			{
				PreConfig: func() {
					testAccRetry(t, "включение legal hold мимо Terraform", func(ctx context.Context) error {
						client := testAccS3Client(t)
						_, err := client.PutObjectLegalHold(ctx, &s3.PutObjectLegalHoldInput{
							Bucket:    aws.String(bucketName),
							Key:       aws.String("hello.txt"),
							LegalHold: &s3types.ObjectLockLegalHold{Status: s3types.ObjectLockLegalHoldStatusOn},
						})
						if err != nil {
							return err
						}
						out, err := client.GetObjectLegalHold(ctx, &s3.GetObjectLegalHoldInput{
							Bucket: aws.String(bucketName), Key: aws.String("hello.txt"),
						})
						if err != nil {
							return err
						}
						if out.LegalHold == nil || out.LegalHold.Status != s3types.ObjectLockLegalHoldStatusOn {
							return fmt.Errorf("legal hold ещё не включён")
						}
						return nil
					})
				},
				Config: testAccObjectLegalHoldConfig(bucketName, false),
				ConfigPlanChecks: resource.ConfigPlanChecks{
					PreApply: []plancheck.PlanCheck{
						plancheck.ExpectResourceAction("platformcraft_object_legal_hold.test", plancheck.ResourceActionUpdate),
					},
				},
				Check: resource.TestCheckResourceAttr("platformcraft_object_legal_hold.test", "enabled", "false"),
			},
		},
	})
}

func testAccObjectLegalHoldConfig(bucketName string, enabled bool) string {
	return testAccConfigLockBucket(bucketName, 1) + testAccConfigLockedObject("legal hold test") + fmt.Sprintf(`
resource "platformcraft_object_legal_hold" "test" {
  bucket  = platformcraft_object.test.bucket
  key     = platformcraft_object.test.key
  enabled = %[1]t
}
`, enabled)
}
